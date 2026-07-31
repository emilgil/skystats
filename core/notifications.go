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

type NotificationService struct {
	pg     *postgres
	client *http.Client
}

func NewNotificationService(pg *postgres) *NotificationService {
	return &NotificationService{
		pg:     pg,
		client: &http.Client{Timeout: 5 * time.Second},
	}
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
func (n *NotificationService) send(apiURL, key string, p apprisePayload) (int, error) {
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
