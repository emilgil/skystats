package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// notifier is the process-wide notification sender, initialised in main().
var notifier *NotificationService

// watchSendConcurrency bounds how many watch notifications may be in flight at
// once. Each one holds a pool connection and can sit on a 5-second HTTP POST,
// and a broad watch can start matching dozens of aircraft on a single tick, so
// without a bound they would starve the rest of the tick of connections and
// fire a burst the notification service will rate-limit anyway.
const watchSendConcurrency = 4

type NotificationService struct {
	pg     *postgres
	client *http.Client

	// watchSends is a counting semaphore over the watch notification workers.
	// It is nil in tests that build the service directly; a nil semaphore just
	// means unbounded, which is what a single-shot unit test wants.
	watchSends chan struct{}

	// photos resolves the attachment picture. Nil in tests that build the
	// service directly, which then simply send without one.
	photos *photoLookup
}

func NewNotificationService(pg *postgres) *NotificationService {
	return &NotificationService{
		pg:         pg,
		client:     &http.Client{Timeout: 5 * time.Second},
		watchSends: make(chan struct{}, watchSendConcurrency),
		photos:     newPhotoLookup(),
	}
}

// photoURL resolves an attachment photo for hex, or "" when there is none or
// the service was built without a lookup.
func (n *NotificationService) photoURL(hex string) string {
	if n.photos == nil {
		return ""
	}
	return n.photos.photoURL(hex)
}

// NotificationConfig is a snapshot of the notification-related user_settings.
type NotificationConfig struct {
	Enabled         bool
	APIURL          string
	ConfigKey       string
	Groups          map[string]bool // "Mil","Gov","Pol","Civ"
	Records         map[string]bool // 7 record categories
	CooldownMinutes int
}

// recordBest is the current #1 all-time holder for a record category.
type recordBest struct {
	Hex          string
	Flight       string
	Registration string
	Type         string
	FirstSeen    time.Time
	MetricValue  float64
}

type apprisePayload struct {
	Body   string `json:"body"`
	Title  string `json:"title"`
	Type   string `json:"type"`
	Format string `json:"format"`
	Attach string `json:"attach,omitempty"`
}

var groupDisplay = map[string]string{
	"Mil": "Military",
	"Gov": "Government",
	"Pol": "Police",
	"Civ": "Civilian",
}

var recordDisplay = map[string]struct{ Name, Metric, Unit string }{
	"fastest":        {"Fastest", "Ground speed", "kt"},
	"slowest":        {"Slowest", "Ground speed", "kt"},
	"highest":        {"Highest", "Altitude", "ft"},
	"lowest":         {"Lowest", "Altitude", "ft"},
	"furthest_flown": {"Furthest flown", "Distance flown", "km"},
	"longest_route":  {"Longest route", "Route distance", "km"},
	"most_remaining": {"Most remaining", "Distance remaining", "km"},
}

func improved(oldVal, newVal float64, keepMax bool) bool {
	if keepMax {
		return newVal > oldVal
	}
	return newVal < oldVal
}

func (n *NotificationService) loadConfig() NotificationConfig {
	return NotificationConfig{
		Enabled:   getBoolSetting(n.pg, "notifications_enabled", false),
		APIURL:    strings.TrimRight(getStringSetting(n.pg, "apprise_api_url", ""), "/"),
		ConfigKey: getStringSetting(n.pg, "apprise_config_key", ""),
		Groups: map[string]bool{
			"Mil": getBoolSetting(n.pg, "notify_group_mil", true),
			"Gov": getBoolSetting(n.pg, "notify_group_gov", true),
			"Pol": getBoolSetting(n.pg, "notify_group_pol", true),
			"Civ": getBoolSetting(n.pg, "notify_group_civ", true),
		},
		Records: map[string]bool{
			"fastest":        getBoolSetting(n.pg, "notify_record_fastest", true),
			"slowest":        getBoolSetting(n.pg, "notify_record_slowest", true),
			"highest":        getBoolSetting(n.pg, "notify_record_highest", true),
			"lowest":         getBoolSetting(n.pg, "notify_record_lowest", true),
			"furthest_flown": getBoolSetting(n.pg, "notify_record_furthest_flown", true),
			"longest_route":  getBoolSetting(n.pg, "notify_record_longest_route", true),
			"most_remaining": getBoolSetting(n.pg, "notify_record_most_remaining", true),
		},
		CooldownMinutes: getIntSetting(n.pg, "notification_cooldown_minutes", 60),
	}
}

// send POSTs a payload to {apiURL}/notify/{key}. Returns the HTTP status (0 if
// no response) and an error on failure.
//
// When the payload carries an attachment and Apprise answers 400, the
// attachment is dropped and the message is sent once more. Apprise refuses the
// whole notification rather than delivering the text alone whenever it cannot
// use the attachment — an unreachable image URL, or a deployment with
// attachments switched off, both surface as "Bad Attachment". The picture is
// decoration; losing the alert over it is not acceptable, so it is better to
// arrive without one. Only one retry, and only when there is something to drop.
func (n *NotificationService) send(apiURL, key string, p apprisePayload) (int, error) {
	status, err := n.post(apiURL, key, p)
	if err == nil || status != http.StatusBadRequest || p.Attach == "" {
		return status, err
	}

	log.Warn().Msgf("Apprise rejected the attachment %q with status 400; resending without it", p.Attach)
	p.Attach = ""
	return n.post(apiURL, key, p)
}

// post performs one POST of the payload, with no retry.
func (n *NotificationService) post(apiURL, key string, p apprisePayload) (int, error) {
	apiURL = strings.TrimRight(apiURL, "/")
	if apiURL == "" || key == "" {
		return 0, fmt.Errorf("apprise api url or config key not set")
	}
	p.Type = "info"
	p.Format = "markdown"
	buf, err := json.Marshal(p)
	if err != nil {
		return 0, err
	}
	url := fmt.Sprintf("%s/notify/%s", apiURL, key)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("apprise returned status %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

func formatMetric(v float64) string { return fmt.Sprintf("%.0f", v) }

// buildInterestingMessage returns (title, body, attach) for an interesting sighting.
func buildInterestingMessage(a InterestingAircraft, distanceKm *float64, routeFrom, routeTo string) (string, string, string) {
	category := groupDisplay[a.Group.String]
	if category == "" {
		category = a.Group.String
	}
	name := a.R
	if strings.TrimSpace(name) == "" {
		name = strings.TrimSpace(a.Flight)
	}
	if name == "" && a.Registration.Valid {
		name = a.Registration.String
	}
	if name == "" {
		name = a.Hex
	}
	title := fmt.Sprintf("✈️ %s: %s", category, name)

	var b strings.Builder
	if a.Type.Valid && a.Type.String != "" {
		fmt.Fprintf(&b, "Type: %s\n", a.Type.String)
	}
	if a.Operator.Valid && a.Operator.String != "" {
		fmt.Fprintf(&b, "Operator: %s\n", a.Operator.String)
	}
	if f := strings.TrimSpace(a.Flight); f != "" {
		fmt.Fprintf(&b, "Callsign: %s\n", f)
	}
	if a.AltBaro != 0 {
		fmt.Fprintf(&b, "Altitude: %d ft\n", a.AltBaro)
	}
	if a.Gs != 0 {
		fmt.Fprintf(&b, "Speed: %s kt\n", formatMetric(a.Gs))
	}
	if distanceKm != nil {
		fmt.Fprintf(&b, "Distance: %s km\n", formatMetric(*distanceKm))
	}
	if routeFrom != "" && routeTo != "" {
		fmt.Fprintf(&b, "Route: %s → %s\n", routeFrom, routeTo)
	}
	if a.Link.Valid && a.Link.String != "" {
		fmt.Fprintf(&b, "Link: %s\n", a.Link.String)
	}

	attach := ""
	if a.ImageLink1.Valid {
		attach = a.ImageLink1.String
	}
	return title, strings.TrimRight(b.String(), "\n"), attach
}

// buildRecordMessage returns (title, body) for a new all-time record.
func buildRecordMessage(category string, best recordBest, prevValue float64, hasPrev bool) (string, string) {
	d := recordDisplay[category]
	title := fmt.Sprintf("🏆 New all-time record: %s", d.Name)

	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s %s\n", d.Metric, formatMetric(best.MetricValue), d.Unit)
	if hasPrev {
		fmt.Fprintf(&b, "Previous: %s %s\n", formatMetric(prevValue), d.Unit)
	}
	reg := best.Registration
	if reg == "" {
		reg = best.Hex
	}
	if best.Type != "" {
		fmt.Fprintf(&b, "Aircraft: %s (%s)\n", reg, best.Type)
	} else {
		fmt.Fprintf(&b, "Aircraft: %s\n", reg)
	}
	if f := strings.TrimSpace(best.Flight); f != "" {
		fmt.Fprintf(&b, "Callsign: %s\n", f)
	}
	return title, strings.TrimRight(b.String(), "\n")
}

func (n *NotificationService) logAttempt(kind, category, icao string, firstSeen *time.Time, metricValue *float64, title, body, target, status string, httpStatus int, errMsg string) {
	_, err := n.pg.db.Exec(context.Background(), `
		INSERT INTO notification_log
			(kind, category, icao, first_seen, metric_value, title, body, target, status, http_status, error)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,''))`,
		kind, category, icao, firstSeen, metricValue, title, body, target, status, httpStatus, errMsg)
	if err != nil {
		log.Error().Err(err).Msg("logAttempt() - failed to write notification_log")
	}
}

func (n *NotificationService) interestingOnCooldown(hex string, cooldownMinutes int) bool {
	if cooldownMinutes <= 0 {
		return false
	}
	var exists bool
	err := n.pg.db.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1 FROM notification_log
			WHERE kind='interesting' AND icao=$1 AND status='sent'
			  AND created_at > NOW() - make_interval(mins => $2)
		)`, hex, cooldownMinutes).Scan(&exists)
	if err != nil {
		log.Error().Err(err).Msg("interestingOnCooldown() - query failed")
		return false
	}
	return exists
}

func (n *NotificationService) recordAlreadyNotified(category, hex string, firstSeen time.Time) bool {
	var exists bool
	err := n.pg.db.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1 FROM notification_log
			WHERE kind='record' AND category=$1 AND icao=$2 AND first_seen=$3 AND status='sent'
		)`, category, hex, firstSeen).Scan(&exists)
	if err != nil {
		log.Error().Err(err).Msg("recordAlreadyNotified() - query failed")
		return false
	}
	return exists
}

func (n *NotificationService) lookupRoute(callsign string) (from, to string) {
	if callsign == "" {
		return "", ""
	}
	var origin, dest sql.NullString
	err := n.pg.db.QueryRow(context.Background(), `
		SELECT origin_iata_code, destination_iata_code
		FROM route_data WHERE route_callsign = $1 LIMIT 1`, callsign).Scan(&origin, &dest)
	if err != nil {
		return "", ""
	}
	return origin.String, dest.String
}

// NotifyBatch sends interesting-aircraft notifications for freshly-inserted sightings.
func (n *NotificationService) NotifyBatch(aircraft []InterestingAircraft) {
	cfg := n.loadConfig()
	if !cfg.Enabled || cfg.APIURL == "" || cfg.ConfigKey == "" {
		return
	}
	for _, a := range aircraft {
		group := a.Group.String
		if !cfg.Groups[group] {
			continue
		}
		if n.interestingOnCooldown(a.Hex, cfg.CooldownMinutes) {
			continue
		}
		var dist *float64
		if a.Lat != 0 || a.Lon != 0 {
			dist = getDistance([]float64{a.Lon, a.Lat})
		}
		from, to := n.lookupRoute(strings.TrimSpace(a.Flight))
		title, body, attach := buildInterestingMessage(a, dist, from, to)
		// Planespotters wins over the picture stored with the plane-alert entry.
		// Those are cdn.jetphotos.com URLs that answer a server-side fetch with
		// 403, so Apprise cannot attach them; the stored one is kept only as a
		// fallback for the aircraft Planespotters has never seen.
		if photo := n.photoURL(a.Hex); photo != "" {
			attach = photo
		}
		httpStatus, sendErr := n.send(cfg.APIURL, cfg.ConfigKey, apprisePayload{Body: body, Title: title, Attach: attach})
		status, errMsg := "sent", ""
		if sendErr != nil {
			status, errMsg = "failed", sendErr.Error()
			log.Error().Err(sendErr).Msgf("Interesting notification failed for %s", a.Hex)
		}
		n.logAttempt("interesting", group, a.Hex, nil, nil, title, body, cfg.ConfigKey, status, httpStatus, errMsg)
	}
}

// NotifyRecord sends a notification for a newly-set all-time record.
func (n *NotificationService) NotifyRecord(category string, best recordBest, prevValue float64, hasPrev bool) {
	cfg := n.loadConfig()
	if !cfg.Enabled || cfg.APIURL == "" || cfg.ConfigKey == "" {
		return
	}
	if !cfg.Records[category] {
		return
	}
	if n.recordAlreadyNotified(category, best.Hex, best.FirstSeen) {
		return
	}
	title, body := buildRecordMessage(category, best, prevValue, hasPrev)
	httpStatus, sendErr := n.send(cfg.APIURL, cfg.ConfigKey, apprisePayload{Body: body, Title: title})
	status, errMsg := "sent", ""
	if sendErr != nil {
		status, errMsg = "failed", sendErr.Error()
		log.Error().Err(sendErr).Msgf("Record notification failed for %s/%s", category, best.Hex)
	}
	fs := best.FirstSeen
	mv := best.MetricValue
	n.logAttempt("record", category, best.Hex, &fs, &mv, title, body, cfg.ConfigKey, status, httpStatus, errMsg)
}

// SendTest sends a fixed test message. Empty apiURL/key fall back to saved settings.
func (n *NotificationService) SendTest(apiURL, key string) error {
	if strings.TrimSpace(apiURL) == "" {
		apiURL = getStringSetting(n.pg, "apprise_api_url", "")
	}
	if strings.TrimSpace(key) == "" {
		key = getStringSetting(n.pg, "apprise_config_key", "")
	}
	httpStatus, err := n.send(apiURL, key, apprisePayload{
		Title: "✈️ Skystats test",
		Body:  "This is a test notification from Skystats.",
	})
	status, errMsg := "sent", ""
	if err != nil {
		status, errMsg = "failed", err.Error()
	}
	n.logAttempt("test", "", "", nil, nil, "✈️ Skystats test", "This is a test notification from Skystats.", key, status, httpStatus, errMsg)
	return err
}

// buildWatchMessage returns (title, body) for an aircraft that has started
// matching a watch. Fields with no data are omitted rather than shown empty.
func buildWatchMessage(watchName string, s watchSubject) (string, string) {

	name := firstNonEmpty(s.Registration, strings.TrimSpace(s.Callsign), s.Hex)
	title := fmt.Sprintf("👁 Watch \"%s\": %s", watchName, name)

	var b strings.Builder
	if f := strings.TrimSpace(s.Callsign); f != "" {
		fmt.Fprintf(&b, "Callsign: %s\n", f)
	}
	if s.TypeCode != "" {
		fmt.Fprintf(&b, "Type: %s\n", s.TypeCode)
	}
	if s.Model != "" {
		fmt.Fprintf(&b, "Model: %s\n", s.Model)
	}
	if s.Registration != "" {
		fmt.Fprintf(&b, "Registration: %s\n", s.Registration)
	}
	if s.Airline != "" {
		fmt.Fprintf(&b, "Airline: %s\n", s.Airline)
	}
	if len(s.Origin) > 0 && len(s.Destination) > 0 {
		fmt.Fprintf(&b, "Route: %s → %s\n", s.Origin[len(s.Origin)-1], s.Destination[len(s.Destination)-1])
	}
	if s.HasAltitude {
		fmt.Fprintf(&b, "Altitude: %s ft\n", formatMetric(s.AltitudeFt))
	}
	if s.HasSpeed {
		fmt.Fprintf(&b, "Speed: %s kt\n", formatMetric(s.SpeedKt))
	}
	if s.HasPosition {
		fmt.Fprintf(&b, "Distance: %s km\n", formatMetric(s.DistanceKm))
	}
	if s.Squawk != "" {
		fmt.Fprintf(&b, "Squawk: %s\n", s.Squawk)
	}
	if s.FirstSeenEver {
		fmt.Fprintf(&b, "First time ever seen\n")
	}

	return title, strings.TrimRight(b.String(), "\n")
}

// NotifyWatch sends the Apprise notification for a watch match and records the
// hit. The history row is written whether or not sending is enabled, capped or
// successful, so the Watches tab shows hits even without Apprise configured.
//
// cfg is loaded once per tick by evaluateWatches and passed in rather than read
// here, and allowSend is false when the per-tick cap has already been spent —
// the row is still written, only the push is dropped.
func (n *NotificationService) NotifyWatch(cfg NotificationConfig, w Watch, s watchSubject, allowSend bool) {

	if n.watchSends != nil {
		n.watchSends <- struct{}{}
		defer func() { <-n.watchSends }()
	}

	title, body := buildWatchMessage(w.Name, s)

	snapshot, err := json.Marshal(map[string]any{
		"callsign":          s.Callsign,
		"registration":      s.Registration,
		"type_code":         s.TypeCode,
		"model":             s.Model,
		"manufacturer":      s.Manufacturer,
		"country":           s.Country,
		"airline":           s.Airline,
		"origin":            s.Origin,
		"destination":       s.Destination,
		"squawk":            s.Squawk,
		"altitude_ft":       s.AltitudeFt,
		"speed_kt":          s.SpeedKt,
		"distance_km":       s.DistanceKm,
		"vertical_rate_fpm": s.VerticalRateFpm,
		"first_seen_ever":   s.FirstSeenEver,
	})
	if err != nil {
		log.Error().Err(err).Msg("NotifyWatch() - unable to marshal snapshot")
		snapshot = []byte("{}")
	}

	// Resolve what will happen to this match before anything is written, so the
	// history row can record the reason when no POST is attempted at all.
	var key, sendError string
	send := false
	switch {
	case !allowSend:
		sendError = "suppressed: per-tick notification cap reached"
	case !cfg.Enabled:
		sendError = "notifications are disabled"
	case cfg.APIURL == "":
		sendError = "apprise api url is not set"
	default:
		key = firstNonEmpty(strings.TrimSpace(w.AppriseKey), cfg.ConfigKey)
		if key == "" {
			sendError = "apprise config key is not set"
		} else {
			send = true
		}
	}

	// The history row goes in before the POST, not after. The POST can take up
	// to five seconds, and if the user deletes the watch inside that window an
	// INSERT afterwards fails the watch_id foreign key and the hit is lost
	// entirely — even though the message was delivered. Written first, the row
	// merely has its watch_id set to NULL by the delete (ON DELETE SET NULL)
	// and survives with watch_name intact, which is the behaviour the feature
	// promises: deleting a watch keeps its history.
	var id int
	err = n.pg.db.QueryRow(context.Background(), `
		INSERT INTO watch_notifications
			(watch_id, watch_name, hex, flight, registration, snapshot, apprise_success, apprise_error)
		VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6, false, NULLIF($7, ''))
		RETURNING id`,
		w.ID, w.Name, s.Hex, strings.TrimSpace(s.Callsign), s.Registration, snapshot, sendError).Scan(&id)
	recorded := err == nil
	if err != nil {
		log.Error().Err(err).Msg("NotifyWatch() - failed to write watch_notifications")
	}

	if !send {
		return
	}

	// Looked up only once the message is definitely going out — a suppressed or
	// disabled notification has no reason to reach across the network.
	attach := n.photoURL(s.Hex)

	success := false
	if _, err := n.send(cfg.APIURL, key, apprisePayload{Title: title, Body: body, Attach: attach}); err != nil {
		sendError = err.Error()
		log.Error().Err(err).Msgf("Watch notification failed for watch %d / %s", w.ID, s.Hex)
	} else {
		success, sendError = true, ""
	}

	if !recorded {
		return
	}

	_, err = n.pg.db.Exec(context.Background(), `
		UPDATE watch_notifications
		SET apprise_success = $2, apprise_error = NULLIF($3, '')
		WHERE id = $1`, id, success, sendError)
	if err != nil {
		log.Error().Err(err).Msg("NotifyWatch() - failed to update watch_notifications outcome")
	}
}
