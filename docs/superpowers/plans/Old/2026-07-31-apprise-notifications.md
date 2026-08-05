# Apprise Notifications Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Send Apprise push notifications when a new "interesting" aircraft is seen or a new all-time record is set, configured from a new **Notifications** tab under Settings.

**Architecture:** A process-wide `NotificationService` (Go, `core/notifications.go`) reads notification settings from `user_settings`, builds a markdown message, and POSTs it to a user-run Apprise API server in **stateful** mode (`POST {url}/notify/{key}`). Two trigger points call it in background goroutines: `updateInterestingSeen()` (interesting aircraft) and `writeRecords()` (all-time records). A `notification_log` table provides per-ICAO cooldown, per-flight-session record dedupe, and an audit trail. The Svelte Settings modal gains a Notifications tab.

**Tech Stack:** Go 1.x (`package main`, pgx v5, gin, zerolog), PostgreSQL (golang-migrate SQL files), Svelte 5 + Tailwind/DaisyUI.

## Global Constraints

- Go code lives in `core/` as a single `package main`. Build with `cd core && go build -o skystats-daemon`. Test with `cd core && go test ./...`.
- Migrations are numbered `.up.sql`/`.down.sql` pairs in `migrations/`, applied automatically at startup. Next number is **000014**.
- Apprise integration is **stateful only**: store an API base URL + a config key; never store raw service URLs/tokens. Endpoint: `POST {apiURL}/notify/{key}`, JSON body `{"body","title","type":"info","format":"markdown","attach"?}`. HTTP client timeout: **5s**.
- The 4 interesting groups are exactly `Mil`, `Gov`, `Pol`, `Civ` (column `interesting_aircraft_seen.group`). The 7 record categories are exactly `fastest, slowest, highest, lowest, furthest_flown, longest_route, most_remaining` (`recordCategories` in `core/records-meta.go`). Record notifications fire for **`all_time` only**.
- Record dedupe key: `(category, hex, first_seen)`. Interesting cooldown key: `icao (hex)` within `notification_cooldown_minutes` (default 60).
- All Apprise POSTs run in background goroutines so a slow server never blocks the main ticker loop.
- Commit messages use conventional-commit prefixes and end with the trailer:
  `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`

---

## File Structure

- Create `migrations/000014_add_notifications.up.sql` / `.down.sql` — `notification_log` table + 15 default settings.
- Create `core/notifications.go` — `NotificationService`, `NotificationConfig`, `recordBest`, message builders, HTTP send, gate/cooldown/dedupe logic, log writes, package-level `notifier`.
- Create `core/notifications_test.go` — unit tests for pure logic + `httptest` send.
- Modify `core/records-jobs.go` — add `getStringSetting` / `getBoolSetting` helpers next to `getIntSetting`.
- Modify `core/core.go` — initialise the package-level `notifier` in `main()`.
- Modify `core/stats-interesting.go` — dispatch `notifier.NotifyBatch` after inserting new interesting rows.
- Modify `core/records-ingest.go` — add `allTimeBest`, detect a beaten all-time record in `writeRecords`, dispatch `notifier.NotifyRecord`.
- Modify `core/api.go` — add `POST /api/notifications/test`.
- Modify `web/src/components/Settings.svelte` — add the Notifications menu item, form, save + test actions.

---

## Task 1: Migration 000014 — notification_log table + settings

**Files:**
- Create: `migrations/000014_add_notifications.up.sql`
- Create: `migrations/000014_add_notifications.down.sql`

**Interfaces:**
- Produces: table `notification_log(id, kind, category, icao, first_seen, metric_value, title, body, target, status, http_status, error, created_at)`; 15 `user_settings` rows (keys listed below).

- [ ] **Step 1: Write the up migration**

Create `migrations/000014_add_notifications.up.sql`:

```sql
CREATE TABLE notification_log (
    id           SERIAL PRIMARY KEY,
    kind         TEXT NOT NULL,          -- 'interesting' | 'record'
    category     TEXT,                   -- interesting: Mil/Gov/Pol/Civ ; record: highest/fastest/...
    icao         TEXT,                   -- aircraft hex
    first_seen   TIMESTAMPTZ,            -- record dedupe: identifies the flight-session
    metric_value DOUBLE PRECISION,       -- record: the value that set the record
    title        TEXT,
    body         TEXT,
    target       TEXT,                   -- apprise config key used
    status       TEXT NOT NULL,          -- 'sent' | 'failed'
    http_status  INTEGER,
    error        TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_notification_log_icao_created ON notification_log (icao, created_at);
CREATE INDEX idx_notification_log_record_dedupe ON notification_log (kind, category, icao, first_seen);

INSERT INTO user_settings (setting_key, setting_value, description) VALUES
    ('notifications_enabled',        'false', 'Master on/off for Apprise notifications'),
    ('apprise_api_url',              '',      'Base URL of the Apprise API server'),
    ('apprise_config_key',           '',      'Saved config key on the Apprise server'),
    ('notify_group_mil',             'true',  'Notify for military aircraft'),
    ('notify_group_gov',             'true',  'Notify for government aircraft'),
    ('notify_group_pol',             'true',  'Notify for police aircraft'),
    ('notify_group_civ',             'true',  'Notify for civilian (interesting) aircraft'),
    ('notification_cooldown_minutes','60',    'Per-ICAO cooldown between interesting notifications'),
    ('notify_record_fastest',        'true',  'Notify on new all-time fastest'),
    ('notify_record_slowest',        'true',  'Notify on new all-time slowest'),
    ('notify_record_highest',        'true',  'Notify on new all-time highest'),
    ('notify_record_lowest',         'true',  'Notify on new all-time lowest'),
    ('notify_record_furthest_flown', 'true',  'Notify on new all-time furthest flown'),
    ('notify_record_longest_route',  'true',  'Notify on new all-time longest route'),
    ('notify_record_most_remaining', 'true',  'Notify on new all-time most remaining')
ON CONFLICT (setting_key) DO NOTHING;
```

- [ ] **Step 2: Write the down migration**

Create `migrations/000014_add_notifications.down.sql`:

```sql
DELETE FROM user_settings WHERE setting_key IN (
    'notifications_enabled','apprise_api_url','apprise_config_key',
    'notify_group_mil','notify_group_gov','notify_group_pol','notify_group_civ',
    'notification_cooldown_minutes',
    'notify_record_fastest','notify_record_slowest','notify_record_highest','notify_record_lowest',
    'notify_record_furthest_flown','notify_record_longest_route','notify_record_most_remaining'
);

DROP TABLE IF EXISTS notification_log;
```

- [ ] **Step 3: Verify the migration applies**

Ensure a dev Postgres is running and `.env` is populated, then start the daemon so migrations run:

Run: `cd core && go build -o skystats-daemon && DOCKER_ENV=true ./skystats-daemon 2>&1 | head -40`
Expected: log lines showing migrations run without error (no "Error initialising or migrating the database"). Stop it with Ctrl-C.

Then confirm with psql (adjust connection to your `.env`):
Run: `psql "$DATABASE_URL" -c '\d notification_log' -c "SELECT setting_key FROM user_settings WHERE setting_key LIKE 'notify_%' OR setting_key LIKE 'apprise_%';"`
Expected: the table description prints; 11 `notify_*`/`apprise_*` keys are listed.

- [ ] **Step 4: Commit**

```bash
git add migrations/000014_add_notifications.up.sql migrations/000014_add_notifications.down.sql
git commit -m "feat: add notification_log table and notification settings

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 2: Settings helpers (string + bool)

**Files:**
- Modify: `core/records-jobs.go` (add two functions after `getIntSetting`)

**Interfaces:**
- Consumes: `postgres` struct with `.db` (pgx pool), `user_settings(setting_key, setting_value)`.
- Produces: `getStringSetting(pg *postgres, key, def string) string`, `getBoolSetting(pg *postgres, key string, def bool) bool`.

- [ ] **Step 1: Add the helpers**

In `core/records-jobs.go`, immediately after the `getIntSetting` function (ends at line 24), add:

```go
// getStringSetting reads a string user_settings value, returning def on any error.
func getStringSetting(pg *postgres, key, def string) string {
	var val string
	err := pg.db.QueryRow(context.Background(),
		`SELECT setting_value FROM user_settings WHERE setting_key = $1`, key).Scan(&val)
	if err != nil {
		return def
	}
	return val
}

// getBoolSetting reads a boolean user_settings value ("true"/"false"),
// returning def on any error.
func getBoolSetting(pg *postgres, key string, def bool) bool {
	var val string
	err := pg.db.QueryRow(context.Background(),
		`SELECT setting_value FROM user_settings WHERE setting_key = $1`, key).Scan(&val)
	if err != nil {
		return def
	}
	return val == "true"
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd core && go build ./...`
Expected: builds with no errors. (The functions are unused for now; Go allows unused package-level functions.)

- [ ] **Step 3: Commit**

```bash
git add core/records-jobs.go
git commit -m "feat: add string and bool user_settings helpers

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 3: NotificationService core + unit tests

**Files:**
- Create: `core/notifications.go`
- Create: `core/notifications_test.go`

**Interfaces:**
- Consumes: `postgres`, `InterestingAircraft` (models.go), `getDistance([]float64) *float64` (aircraft.go), `getStringSetting`/`getBoolSetting`/`getIntSetting` (records-jobs.go), `recordCategory` (records-meta.go).
- Produces:
  - `var notifier *NotificationService`
  - `func NewNotificationService(pg *postgres) *NotificationService`
  - `type recordBest struct { Hex, Flight, Registration, Type string; FirstSeen time.Time; MetricValue float64 }`
  - `func (n *NotificationService) NotifyBatch(aircraft []InterestingAircraft)`
  - `func (n *NotificationService) NotifyRecord(category string, best recordBest, prevValue float64, hasPrev bool)`
  - `func (n *NotificationService) SendTest(apiURL, key string) error`
  - `func improved(oldVal, newVal float64, keepMax bool) bool`

- [ ] **Step 1: Write the failing tests**

Create `core/notifications_test.go`:

```go
package main

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestImproved(t *testing.T) {
	if !improved(100, 120, true) {
		t.Error("keepMax: 120 should beat 100")
	}
	if improved(100, 100, true) {
		t.Error("keepMax: equal is not an improvement")
	}
	if !improved(500, 300, false) {
		t.Error("keepMin: 300 should beat 500")
	}
	if improved(300, 500, false) {
		t.Error("keepMin: 500 does not beat 300")
	}
}

func TestBuildInterestingMessage(t *testing.T) {
	dist := 12.4
	a := InterestingAircraft{
		Group:    sql.NullString{String: "Mil", Valid: true},
		R:        "SE-ABC",
		Flight:   "SVK123",
		Operator: sql.NullString{String: "Swedish Air Force", Valid: true},
		Type:     sql.NullString{String: "JAS 39 Gripen", Valid: true},
		Link:     sql.NullString{String: "https://example/x", Valid: true},
		ImageLink1: sql.NullString{String: "https://img/1.jpg", Valid: true},
		AltBaro:  25000,
		Gs:       420,
	}
	title, body, attach := buildInterestingMessage(a, &dist, "ARN", "LHR")

	if title != "✈️ Military: SE-ABC" {
		t.Errorf("title = %q", title)
	}
	for _, want := range []string{
		"Type: JAS 39 Gripen", "Operator: Swedish Air Force", "Callsign: SVK123",
		"Altitude: 25000 ft", "Speed: 420 kt", "Distance: 12 km",
		"Route: ARN → LHR", "Link: https://example/x",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q in:\n%s", want, body)
		}
	}
	if attach != "https://img/1.jpg" {
		t.Errorf("attach = %q", attach)
	}
}

func TestBuildInterestingMessageOmitsMissing(t *testing.T) {
	a := InterestingAircraft{
		Group: sql.NullString{String: "Civ", Valid: true},
		Hex:   "abc123",
	}
	title, body, attach := buildInterestingMessage(a, nil, "", "")
	if title != "✈️ Civilian: abc123" {
		t.Errorf("title = %q", title)
	}
	if strings.Contains(body, "Route:") || strings.Contains(body, "Distance:") {
		t.Errorf("body should omit unknown fields:\n%s", body)
	}
	if attach != "" {
		t.Errorf("attach should be empty, got %q", attach)
	}
}

func TestBuildRecordMessage(t *testing.T) {
	best := recordBest{Registration: "N12345", Type: "B738", Flight: "SAS1", MetricValue: 45000}
	title, body := buildRecordMessage("highest", best, 44200, true)
	if title != "🏆 New all-time record: Highest" {
		t.Errorf("title = %q", title)
	}
	for _, want := range []string{"Altitude: 45000 ft", "Previous: 44200 ft", "Aircraft: N12345 (B738)", "Callsign: SAS1"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q in:\n%s", want, body)
		}
	}
}

func TestSendPostsStatefulPayload(t *testing.T) {
	var gotPath, gotBody, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := &NotificationService{client: &http.Client{Timeout: 5 * time.Second}}
	status, err := n.send(srv.URL, "skystats", apprisePayload{Title: "T", Body: "B"})
	if err != nil {
		t.Fatalf("send error: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("status = %d", status)
	}
	if gotPath != "/notify/skystats" {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.HasPrefix(gotCT, "application/json") {
		t.Errorf("content-type = %q", gotCT)
	}
	var p apprisePayload
	if err := json.Unmarshal([]byte(gotBody), &p); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, gotBody)
	}
	if p.Type != "info" || p.Format != "markdown" {
		t.Errorf("type/format = %q/%q", p.Type, p.Format)
	}
}

func TestSendRejectsMissingConfig(t *testing.T) {
	n := &NotificationService{client: &http.Client{Timeout: time.Second}}
	if _, err := n.send("", "key", apprisePayload{Body: "b"}); err == nil {
		t.Error("expected error for empty url")
	}
	if _, err := n.send("http://x", "", apprisePayload{Body: "b"}); err == nil {
		t.Error("expected error for empty key")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd core && go test ./... 2>&1 | head -20`
Expected: compile failure — `undefined: improved`, `undefined: buildInterestingMessage`, `apprisePayload`, etc.

- [ ] **Step 3: Write the implementation**

Create `core/notifications.go`:

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd core && go test ./... -run 'Improved|BuildInteresting|BuildRecord|Send' -v`
Expected: all listed tests PASS.

- [ ] **Step 5: Commit**

```bash
git add core/notifications.go core/notifications_test.go
git commit -m "feat: add NotificationService with Apprise stateful send

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 4: Initialise notifier + interesting-aircraft trigger

**Files:**
- Modify: `core/core.go` (after `UpsertPlaneAlertDb`, before `NewAPIServer`)
- Modify: `core/stats-interesting.go` (after the batch insert loop, before `MarkProcessed`)

**Interfaces:**
- Consumes: `notifier` and `NewNotificationService` (Task 3), `NotifyBatch` (Task 3).

- [ ] **Step 1: Initialise the notifier in main()**

In `core/core.go`, find (around line 90-94):

```go
	log.Info().Msg("Checking if interesting aircraft reference data needs updating from plane-alert-db")
	if err := UpsertPlaneAlertDb(pg); err != nil {
		log.Error().Msgf("Error updating interesting aircraft data: %v", err)
		os.Exit(1)
	}
```

Immediately after that block, add:

```go
	// Initialise the process-wide notification sender used by the interesting
	// and record triggers.
	notifier = NewNotificationService(pg)
```

- [ ] **Step 2: Dispatch interesting notifications**

In `core/stats-interesting.go`, find the end of `updateInterestingSeen` (around lines 195-202):

```go
	for i := 0; i < len(interestingAircrafts); i++ {
		_, err := br.Exec()
		if err != nil {
			log.Error().Err(err).Msg("insertRegistrations() - Unable to insert data")
		}
	}

	MarkProcessed(pg, "interesting_processed", aircrafts)
```

Insert the dispatch between the loop and `MarkProcessed`:

```go
	for i := 0; i < len(interestingAircrafts); i++ {
		_, err := br.Exec()
		if err != nil {
			log.Error().Err(err).Msg("insertRegistrations() - Unable to insert data")
		}
	}

	if notifier != nil && len(interestingAircrafts) > 0 {
		toNotify := append([]InterestingAircraft(nil), interestingAircrafts...)
		go notifier.NotifyBatch(toNotify)
	}

	MarkProcessed(pg, "interesting_processed", aircrafts)
```

- [ ] **Step 3: Verify it compiles**

Run: `cd core && go build ./... && go test ./... -run Nothing`
Expected: builds clean; `go test` reports `ok` (no tests match `Nothing`, which is fine — this confirms the package still compiles under the test build).

- [ ] **Step 4: Commit**

```bash
git add core/core.go core/stats-interesting.go
git commit -m "feat: dispatch Apprise notifications for interesting aircraft

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 5: All-time record detection + trigger

**Files:**
- Modify: `core/records-ingest.go` (add `allTimeBest`; wrap the write in `writeRecords`)

**Interfaces:**
- Consumes: `recordBest`, `improved`, `notifier`, `NotifyRecord` (Task 3); `recordCategory.bestFirstSQL()` and `.KeepMax`/`.Name` (records-meta.go).
- Produces: `func allTimeBest(pg *postgres, meta recordCategory) (recordBest, bool)`.

- [ ] **Step 1: Add allTimeBest helper**

In `core/records-ingest.go`, add this function at the end of the file:

```go
// allTimeBest returns the current #1 all_time record for a category, and false
// when the bucket is empty. metric_value is cast to float8 to match the read path.
func allTimeBest(pg *postgres, meta recordCategory) (recordBest, bool) {
	query := fmt.Sprintf(`
		SELECT hex, flight, registration, type, first_seen, metric_value::float8
		FROM records
		WHERE category = $1 AND period_type = 'all_time'
		ORDER BY metric_value %s, first_seen ASC
		LIMIT 1`, meta.bestFirstSQL())

	var b recordBest
	err := pg.db.QueryRow(context.Background(), query, meta.Name).Scan(
		&b.Hex, &b.Flight, &b.Registration, &b.Type, &b.FirstSeen, &b.MetricValue)
	if err != nil {
		return recordBest{}, false
	}
	return b, true
}
```

- [ ] **Step 2: Detect a beaten record inside writeRecords**

In `core/records-ingest.go`, `writeRecords` currently reads (lines 68-90):

```go
func writeRecords(pg *postgres, category string, candidates []recordCandidate) {
	if len(candidates) == 0 {
		return
	}
	meta, ok := recordCategories[category]
	if !ok {
		log.Error().Msgf("writeRecords() - unknown category %s", category)
		return
	}

	now := time.Now()
```

Add the "old best" read right after the `meta` lookup:

```go
func writeRecords(pg *postgres, category string, candidates []recordCandidate) {
	if len(candidates) == 0 {
		return
	}
	meta, ok := recordCategories[category]
	if !ok {
		log.Error().Msgf("writeRecords() - unknown category %s", category)
		return
	}

	oldBest, hadOld := allTimeBest(pg, meta)

	now := time.Now()
```

Then, at the very end of `writeRecords` (after the trim loop, lines 115-117):

```go
	for period := range affected {
		trimRecordsBucket(pg, meta, period, 100)
	}
}
```

add the detection + dispatch before the closing brace:

```go
	for period := range affected {
		trimRecordsBucket(pg, meta, period, 100)
	}

	// Notify when a candidate has beaten the previous all-time #1. A missing
	// previous best (fresh install) is treated as a silent baseline.
	if notifier != nil && hadOld {
		if newBest, hasNew := allTimeBest(pg, meta); hasNew && improved(oldBest.MetricValue, newBest.MetricValue, meta.KeepMax) {
			prev := oldBest.MetricValue
			go notifier.NotifyRecord(category, newBest, prev, true)
		}
	}
}
```

- [ ] **Step 3: Verify it compiles**

Run: `cd core && go build ./... && go vet ./...`
Expected: builds and vets clean.

- [ ] **Step 4: Commit**

```bash
git add core/records-ingest.go
git commit -m "feat: notify on new all-time records

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 6: Test-notification API endpoint

**Files:**
- Modify: `core/api.go` (register route in `Start()`; add handler)

**Interfaces:**
- Consumes: `notifier`, `SendTest` (Task 3).
- Produces: `POST /api/notifications/test` → `200 {"status":"sent"}` or error JSON.

- [ ] **Step 1: Register the route**

In `core/api.go` `Start()`, after the settings group (lines 134-138):

```go
			settings := api.Group("/settings")
			{
				settings.GET("", s.getSettings)
				settings.PUT("", s.updateSettings)
			}
```

add:

```go
			notifications := api.Group("/notifications")
			{
				notifications.POST("/test", s.testNotification)
			}
```

- [ ] **Step 2: Add the handler**

At the end of `core/api.go`, add:

```go
func (s *APIServer) testNotification(c *gin.Context) {
	var body struct {
		AppriseAPIURL    string `json:"apprise_api_url"`
		AppriseConfigKey string `json:"apprise_config_key"`
	}
	_ = c.ShouldBindJSON(&body) // body is optional; falls back to saved settings

	if notifier == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "notifier not initialised"})
		return
	}
	if err := notifier.SendTest(body.AppriseAPIURL, body.AppriseConfigKey); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "sent"})
}
```

- [ ] **Step 3: Verify it compiles**

Run: `cd core && go build ./...`
Expected: builds clean.

- [ ] **Step 4: Verify the endpoint end-to-end**

Start a throwaway Apprise-like sink and the daemon, then curl the endpoint. In one terminal:

Run: `python3 -m http.server 9911`

In another, with a dev DB and `.env` ready, temporarily set the two settings and start the daemon:

Run: `psql "$DATABASE_URL" -c "UPDATE user_settings SET setting_value='true' WHERE setting_key='notifications_enabled'; UPDATE user_settings SET setting_value='http://localhost:9911' WHERE setting_key='apprise_api_url'; UPDATE user_settings SET setting_value='skystats' WHERE setting_key='apprise_config_key';"`
Run: `cd core && DOCKER_ENV=true ./skystats-daemon &`
Run: `curl -s -X POST localhost:8080/api/notifications/test -H 'Content-Type: application/json' -d '{}'`
Expected: `{"status":"sent"}`, and the `python3 -m http.server` terminal logs a `POST /notify/skystats` request. (The simple server returns 200, so send succeeds.) Stop both processes afterward.

- [ ] **Step 5: Commit**

```bash
git add core/api.go
git commit -m "feat: add POST /api/notifications/test endpoint

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 7: Notifications tab in Settings.svelte

**Files:**
- Modify: `web/src/components/Settings.svelte`

**Interfaces:**
- Consumes: `settings` store (`load`/`save`), the 15 settings keys, `POST /api/notifications/test`.

- [ ] **Step 1: Add state and the menu item**

In the `<script>` block of `web/src/components/Settings.svelte`, add the menu item to `menuItems` (currently `display`, `cards`, `about`) so it reads:

```js
    const menuItems = [
        { id: 'display', label: 'Display' },
        { id: 'cards', label: 'Cards' },
        { id: 'notifications', label: 'Notifications' },
        { id: 'about', label: 'About' }
    ];
```

Add these state variables next to the existing `let routeTableLimit;` declarations:

```js
    let notificationsEnabled = false;
    let appriseApiUrl = '';
    let appriseConfigKey = '';
    let notifyGroupMil = true, notifyGroupGov = true, notifyGroupPol = true, notifyGroupCiv = true;
    let notifyRecordFastest = true, notifyRecordSlowest = true, notifyRecordHighest = true, notifyRecordLowest = true;
    let notifyRecordFurthestFlown = true, notifyRecordLongestRoute = true, notifyRecordMostRemaining = true;
    let notificationCooldownMinutes = 60;
    let notificationsChanged = false;
    let isTestingNotification = false;
    let testResult = null;
```

- [ ] **Step 2: Add reactive initialisation from the store**

After the existing `$: if (!settingsChanged) { ... }` block, add a separate block:

```js
    $: if (!notificationsChanged) {
        if ($settings.notifications_enabled) notificationsEnabled = $settings.notifications_enabled.setting_value === 'true';
        if ($settings.apprise_api_url) appriseApiUrl = $settings.apprise_api_url.setting_value;
        if ($settings.apprise_config_key) appriseConfigKey = $settings.apprise_config_key.setting_value;
        if ($settings.notify_group_mil) notifyGroupMil = $settings.notify_group_mil.setting_value === 'true';
        if ($settings.notify_group_gov) notifyGroupGov = $settings.notify_group_gov.setting_value === 'true';
        if ($settings.notify_group_pol) notifyGroupPol = $settings.notify_group_pol.setting_value === 'true';
        if ($settings.notify_group_civ) notifyGroupCiv = $settings.notify_group_civ.setting_value === 'true';
        if ($settings.notify_record_fastest) notifyRecordFastest = $settings.notify_record_fastest.setting_value === 'true';
        if ($settings.notify_record_slowest) notifyRecordSlowest = $settings.notify_record_slowest.setting_value === 'true';
        if ($settings.notify_record_highest) notifyRecordHighest = $settings.notify_record_highest.setting_value === 'true';
        if ($settings.notify_record_lowest) notifyRecordLowest = $settings.notify_record_lowest.setting_value === 'true';
        if ($settings.notify_record_furthest_flown) notifyRecordFurthestFlown = $settings.notify_record_furthest_flown.setting_value === 'true';
        if ($settings.notify_record_longest_route) notifyRecordLongestRoute = $settings.notify_record_longest_route.setting_value === 'true';
        if ($settings.notify_record_most_remaining) notifyRecordMostRemaining = $settings.notify_record_most_remaining.setting_value === 'true';
        if ($settings.notification_cooldown_minutes) notificationCooldownMinutes = parseInt($settings.notification_cooldown_minutes.setting_value);
    }
```

- [ ] **Step 3: Add change/save/test handlers**

After the existing `saveSettings` function, add:

```js
    function handleNotificationChange() {
        notificationsChanged = true;
        testResult = null;
    }

    async function saveNotificationSettings() {
        isSaving = true;
        const updates = {
            notifications_enabled: notificationsEnabled.toString(),
            apprise_api_url: appriseApiUrl ?? '',
            apprise_config_key: appriseConfigKey ?? '',
            notify_group_mil: notifyGroupMil.toString(),
            notify_group_gov: notifyGroupGov.toString(),
            notify_group_pol: notifyGroupPol.toString(),
            notify_group_civ: notifyGroupCiv.toString(),
            notification_cooldown_minutes: notificationCooldownMinutes.toString(),
            notify_record_fastest: notifyRecordFastest.toString(),
            notify_record_slowest: notifyRecordSlowest.toString(),
            notify_record_highest: notifyRecordHighest.toString(),
            notify_record_lowest: notifyRecordLowest.toString(),
            notify_record_furthest_flown: notifyRecordFurthestFlown.toString(),
            notify_record_longest_route: notifyRecordLongestRoute.toString(),
            notify_record_most_remaining: notifyRecordMostRemaining.toString()
        };
        const success = await settings.save(updates);
        if (success) notificationsChanged = false;
        isSaving = false;
    }

    async function testNotification() {
        isTestingNotification = true;
        testResult = null;
        try {
            const response = await fetch('/api/notifications/test', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    apprise_api_url: appriseApiUrl ?? '',
                    apprise_config_key: appriseConfigKey ?? ''
                })
            });
            if (response.ok) {
                testResult = { ok: true, message: 'Test notification sent!' };
            } else {
                const data = await response.json().catch(() => ({}));
                testResult = { ok: false, message: data.error || 'Failed to send test notification' };
            }
        } catch (e) {
            testResult = { ok: false, message: e.message };
        }
        isTestingNotification = false;
    }
```

- [ ] **Step 4: Add the tab markup**

In the template, after the `{:else if activeMenuItem === 'cards'}` block and before `{:else if activeMenuItem === 'about'}`, insert:

```svelte
                    {:else if activeMenuItem === 'notifications'}
                        <h4 class="text-lg font-semibold mb-6">Notification Settings</h4>

                        <form id="notification-settings-form" class="space-y-6">
                            <label class="flex items-center gap-3">
                                <input type="checkbox" bind:checked={notificationsEnabled} on:change={handleNotificationChange} class="checkbox" />
                                <span class="text-m">Enable Apprise notifications</span>
                            </label>

                            <div>
                                <p class="text-xl font-extralight tracking-wider mb-2">Apprise connection</p>
                                <p class="text-m text-base-content/70 mb-2">Apprise API URL</p>
                                <input type="url" bind:value={appriseApiUrl} on:input={handleNotificationChange}
                                    placeholder="http://192.168.1.10:8000" class="input w-full max-w-md" />
                                <p class="text-m text-base-content/70 mt-3 mb-2">Config key</p>
                                <input type="text" bind:value={appriseConfigKey} on:input={handleNotificationChange}
                                    placeholder="skystats" class="input w-full max-w-md" />
                            </div>

                            <div>
                                <p class="text-xl font-extralight tracking-wider mb-2">Interesting categories</p>
                                <div class="grid grid-cols-2 gap-2 max-w-md">
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyGroupMil} on:change={handleNotificationChange} /><span class="text-sm">Military</span></label>
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyGroupGov} on:change={handleNotificationChange} /><span class="text-sm">Government</span></label>
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyGroupPol} on:change={handleNotificationChange} /><span class="text-sm">Police</span></label>
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyGroupCiv} on:change={handleNotificationChange} /><span class="text-sm">Civilian</span></label>
                                </div>
                            </div>

                            <div>
                                <p class="text-xl font-extralight tracking-wider mb-2">All-time records</p>
                                <div class="grid grid-cols-2 gap-2 max-w-md">
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyRecordFastest} on:change={handleNotificationChange} /><span class="text-sm">Fastest</span></label>
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyRecordSlowest} on:change={handleNotificationChange} /><span class="text-sm">Slowest</span></label>
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyRecordHighest} on:change={handleNotificationChange} /><span class="text-sm">Highest</span></label>
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyRecordLowest} on:change={handleNotificationChange} /><span class="text-sm">Lowest</span></label>
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyRecordFurthestFlown} on:change={handleNotificationChange} /><span class="text-sm">Furthest flown</span></label>
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyRecordLongestRoute} on:change={handleNotificationChange} /><span class="text-sm">Longest route</span></label>
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyRecordMostRemaining} on:change={handleNotificationChange} /><span class="text-sm">Most remaining</span></label>
                                </div>
                            </div>

                            <div>
                                <p class="text-xl font-extralight tracking-wider mb-2">Cooldown</p>
                                <p class="text-m text-base-content/70 mb-2">Minutes to wait before notifying about the same interesting aircraft again</p>
                                <input type="number" bind:value={notificationCooldownMinutes} on:input={handleNotificationChange} min="1" step="1" class="input w-20" />
                            </div>

                            <div class="flex items-center gap-3">
                                <button type="button" class="btn btn-outline" on:click={testNotification} disabled={isTestingNotification}>
                                    {isTestingNotification ? 'Sending...' : 'Send test notification'}
                                </button>
                                {#if testResult}
                                    <span class="text-sm {testResult.ok ? 'text-success' : 'text-error'}">{testResult.message}</span>
                                {/if}
                            </div>
                        </form>
```

- [ ] **Step 5: Add the Save button for the notifications tab**

Find the footer action block (currently only shown for `display`):

```svelte
                {#if activeMenuItem === 'display'}
                    <div class="modal-action justify-end">
                        <button
                            class="btn btn-primary"
                            on:click={saveSettings}
                            disabled={!settingsChanged || isSaving}
                        >
                            {isSaving ? 'Saving...' : 'Save'}
                        </button>
                    </div>
                {/if}
```

Immediately after it, add:

```svelte
                {#if activeMenuItem === 'notifications'}
                    <div class="modal-action justify-end">
                        <button
                            class="btn btn-primary"
                            on:click={saveNotificationSettings}
                            disabled={!notificationsChanged || isSaving}
                        >
                            {isSaving ? 'Saving...' : 'Save'}
                        </button>
                    </div>
                {/if}
```

- [ ] **Step 6: Verify the frontend builds**

Run: `cd web && npm run build`
Expected: Vite build completes with no errors.

- [ ] **Step 7: Commit**

```bash
git add web/src/components/Settings.svelte
git commit -m "feat: add Notifications tab to Settings

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 8: End-to-end verification

**Files:** none (verification only)

- [ ] **Step 1: Full build**

Run: `cd core && go build -o skystats-daemon && go test ./... && cd ../web && npm run build`
Expected: Go builds, all Go tests pass, frontend builds.

- [ ] **Step 2: Live smoke test against a real Apprise server**

With a dev DB + `.env`, start the daemon (`cd core && DOCKER_ENV=true ./skystats-daemon`), open the UI, go to **Settings → Notifications**, enter your Apprise API URL + config key, enable notifications, tick some categories/records, **Save**, then **Send test notification**.
Expected: the test lands on your Apprise target (Discord/Telegram/etc.), and `SELECT kind, status, http_status FROM notification_log ORDER BY created_at DESC LIMIT 5;` shows a `test`/`sent` row.

- [ ] **Step 3: (Optional) Force a record notification**

Temporarily lower an all-time record so the next flight beats it, e.g.:
Run: `psql "$DATABASE_URL" -c "UPDATE records SET metric_value = 1 WHERE category='highest' AND period_type='all_time';"`
Then wait for the 120s motion tick with any aircraft overhead.
Expected: a `record`/`sent` row appears in `notification_log` and a `🏆 New all-time record: Highest` message arrives. (Restore/ignore the tweaked value afterward — the next genuine record will re-establish it.)

- [ ] **Step 4: No commit needed** (verification only). If you made doc notes, commit them separately.
