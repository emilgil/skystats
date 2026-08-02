# Watches & Notifications Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the user define any number of "watches" — rules matched against the aircraft Skystats already tracks — and receive one Apprise notification each time an aircraft starts matching a watch.

**Architecture:** Watch evaluation piggybacks on the existing 2s ingest tick. `updateAircraftDatabase()` already holds the live readsb snapshot and (after Task 3) fetches the Postgres-side enrichment once per tick; both Current Sightings and the new watch engine consume that same snapshot, so watches add no table scan. Each aircraft is flattened into a `watchSubject`, matched against the cached watch definitions in memory, and the resulting match set is diffed against the previous tick's set. A match that *starts* writes a `watch_active_matches` row, a `watch_notifications` history row, and fires Apprise via the existing `NotificationService`. A match that has not been re-confirmed for 10 minutes is dropped, so the aircraft can notify again on its next sighting.

**Tech Stack:** Go (package `main` in `core/`, pgx v5, gin, zerolog), PostgreSQL (golang-migrate), Svelte 5 + Tailwind 4/DaisyUI (Vite).

## Global Constraints

- Module root is the repo root (`go.mod` at `/mnt/c/temp/github/claude/skystats/go.mod`, module `github.com/tomcarman/skystats`). Build/test from the repo root: `go build ./...`, `go test ./...`. Local Go is at `~/.local/go/bin/go` on the WSL box — ensure it is on PATH or use the full path.
- All Go code for this feature lives in package `main` in `core/`.
- **UI language is English** (decided with the user): the tab is `Watches`, not "Bevakningar". All user-visible strings in this feature are English, matching `Current Sightings` / `Record Holders` / `Interesting Aircraft`.
- **Deleting a watch keeps its notification history** (decided with the user): `watch_notifications.watch_id` is `ON DELETE SET NULL` and every row also stores `watch_name` as text so history stays readable.
- Next migration number is **000015** (`000014_add_notifications` is the latest). Migrations auto-run at daemon startup.
- Exact field keys (stored in `watch_conditions.field`): `manufacturer`, `type_code`, `model`, `country`, `airline`, `origin`, `destination`, `registration`, `hex`, `distance_km`, `altitude_ft`, `speed_kt`, `squawk`, `first_seen_ever`, `vertical_rate_fpm`, `callsign`.
- Exact operator keys (stored in `watch_conditions.operator`): `equals`, `contains`, `starts_with`, `over`, `under`, `in_list`, `is_true`.
- Exact combinator values: `AND`, `OR`.
- Missing data never matches: a condition whose subject value is absent evaluates to `false`, never `true` and never an error.
- A watch with zero conditions never matches, for both `AND` and `OR`. The API rejects saving one.
- Watch notifications are gated by the existing global `notifications_enabled` setting plus `apprise_api_url` / `apprise_config_key`. The **history row is always written** even when sending is off, so the Watches tab shows hits without Apprise configured.
- This repo has no DB/HTTP/Svelte test harness. Pure Go logic is unit-tested with `go test ./...`; DB/API/frontend tasks are verified by a successful `go build ./...` + `cd web && npm run build`, then the deploy-and-observe protocol in Task 12.
- Do not commit to `main` directly. Task 1 creates the branch `feat/watches`; every task commits to it.

---

### Task 1: Migration — watch tables and known_aircraft

**Files:**
- Create: `migrations/000015_add_watches.up.sql`
- Create: `migrations/000015_add_watches.down.sql`

**Interfaces:**
- Consumes: nothing.
- Produces: tables `watches`, `watch_conditions`, `watch_active_matches`, `watch_notifications`, `known_aircraft`. All later tasks read/write these.

- [ ] **Step 1: Create the feature branch**

```bash
cd /mnt/c/temp/github/claude/skystats
git checkout -b feat/watches
```

- [ ] **Step 2: Confirm 000015 is really the next free number**

Run: `ls migrations/ | sort | tail -5`
Expected: highest pair is `000014_add_notifications.{up,down}.sql`. If something higher exists, use the next free number consistently everywhere in this plan.

- [ ] **Step 3: Write the up migration**

Create `migrations/000015_add_watches.up.sql`:

```sql
-- User-defined watches: rules matched against tracked aircraft, one Apprise
-- notification per sighting that starts matching.
CREATE TABLE watches (
    id          SERIAL PRIMARY KEY,
    name        TEXT NOT NULL,
    enabled     BOOLEAN NOT NULL DEFAULT true,
    combinator  TEXT NOT NULL DEFAULT 'AND' CHECK (combinator IN ('AND', 'OR')),
    apprise_key TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Flat list of criteria per watch. No nested groups by design.
CREATE TABLE watch_conditions (
    id       SERIAL PRIMARY KEY,
    watch_id INTEGER NOT NULL REFERENCES watches(id) ON DELETE CASCADE,
    field    TEXT NOT NULL,
    operator TEXT NOT NULL,
    value    TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_watch_conditions_watch ON watch_conditions (watch_id);

-- Which aircraft currently match which watch. A row is created when a match
-- starts (and only then is a notification sent) and removed when the match has
-- not been re-confirmed for the grace window.
CREATE TABLE watch_active_matches (
    watch_id   INTEGER NOT NULL REFERENCES watches(id) ON DELETE CASCADE,
    hex        TEXT NOT NULL,
    matched_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (watch_id, hex)
);

-- Hit history. watch_id goes NULL if the watch is deleted; watch_name keeps
-- the row readable.
CREATE TABLE watch_notifications (
    id              SERIAL PRIMARY KEY,
    watch_id        INTEGER REFERENCES watches(id) ON DELETE SET NULL,
    watch_name      TEXT NOT NULL,
    hex             TEXT NOT NULL,
    flight          TEXT,
    registration    TEXT,
    snapshot        JSONB,
    notified_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    apprise_success BOOLEAN NOT NULL DEFAULT false,
    apprise_error   TEXT
);

CREATE INDEX idx_watch_notifications_notified_at ON watch_notifications (notified_at DESC);
CREATE INDEX idx_watch_notifications_watch ON watch_notifications (watch_id, notified_at DESC);

-- Permanent "have we ever seen this hex" archive, independent of any retention
-- applied to aircraft_data or flight_history. Never pruned.
CREATE TABLE known_aircraft (
    hex           TEXT PRIMARY KEY,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Backfill from the full sighting archive so existing aircraft are not
-- reported as first-ever sightings after this migration runs.
INSERT INTO known_aircraft (hex, first_seen_at)
SELECT hex, MIN(first_seen)
FROM aircraft_data
WHERE hex IS NOT NULL AND hex <> '' AND first_seen IS NOT NULL
GROUP BY hex
ON CONFLICT (hex) DO NOTHING;
```

- [ ] **Step 4: Write the down migration**

Create `migrations/000015_add_watches.down.sql`:

```sql
DROP TABLE IF EXISTS known_aircraft;
DROP TABLE IF EXISTS watch_notifications;
DROP TABLE IF EXISTS watch_active_matches;
DROP TABLE IF EXISTS watch_conditions;
DROP TABLE IF EXISTS watches;
```

- [ ] **Step 5: Verify the SQL parses**

The daemon runs migrations at startup, so a syntax error would only surface there. Sanity-check the file pair exists and is non-empty:

Run: `wc -l migrations/000015_add_watches.up.sql migrations/000015_add_watches.down.sql`
Expected: up ≈ 65 lines, down = 5 lines, no errors.

- [ ] **Step 6: Commit**

```bash
git add migrations/000015_add_watches.up.sql migrations/000015_add_watches.down.sql
git commit -m "feat: migration for watches, conditions, matches, hit history and known_aircraft"
```

---

### Task 2: Field registry, subject model and matching predicates

This is the heart of the feature and it is pure: no database, no network. It is fully unit-tested.

**Files:**
- Create: `core/watches-match.go`
- Test: `core/watches-match_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces (used by Tasks 5, 6, 7, 8):
  - `type Watch struct { ID int; Name string; Enabled bool; Combinator string; AppriseKey string; Conditions []WatchCondition; CreatedAt, UpdatedAt time.Time }`
  - `type WatchCondition struct { ID, WatchID int; Field, Operator, Value string }`
  - `type watchSubject struct { ... }` (full definition in Step 3)
  - `func matchWatch(w Watch, s watchSubject) bool`
  - `func matchCondition(c WatchCondition, s watchSubject) bool`
  - `func validateWatch(w Watch) error`
  - `func watchFieldList() []watchField` and `type watchField struct { Key, Label, Kind, Unit, Hint string; Operators []string }`

- [ ] **Step 1: Write the failing tests**

Create `core/watches-match_test.go`:

```go
package main

import "testing"

func subject() watchSubject {
	return watchSubject{
		Hex:             "4ca7b5",
		Callsign:        "SAS1234",
		Registration:    "SE-RTM",
		TypeCode:        "B38M",
		Model:           "Boeing 737 MAX 8",
		Manufacturer:    "Boeing",
		Country:         "Sweden",
		Airline:         "Scandinavian Airlines",
		AirlineCodes:    []string{"SAS", "SK"},
		Origin:          []string{"ESSA", "ARN"},
		Destination:     []string{"EKCH", "CPH"},
		Squawk:          "2000",
		DistanceKm:      42.5,
		HasPosition:     true,
		AltitudeFt:      31000,
		HasAltitude:     true,
		SpeedKt:         450,
		HasSpeed:        true,
		VerticalRateFpm: -1200,
		FirstSeenEver:   false,
	}
}

func cond(field, operator, value string) WatchCondition {
	return WatchCondition{Field: field, Operator: operator, Value: value}
}

func TestMatchConditionStringOperators(t *testing.T) {
	s := subject()
	cases := []struct {
		name string
		c    WatchCondition
		want bool
	}{
		{"equals is case-insensitive", cond("manufacturer", "equals", "boeing"), true},
		{"equals rejects a partial value", cond("manufacturer", "equals", "boe"), false},
		{"contains matches a substring", cond("model", "contains", "MAX"), true},
		{"contains rejects an absent substring", cond("model", "contains", "Airbus"), false},
		{"starts_with matches a prefix", cond("callsign", "starts_with", "SAS"), true},
		{"starts_with rejects a non-prefix", cond("callsign", "starts_with", "RYR"), false},
		{"in_list matches one entry", cond("hex", "in_list", "abc123, 4ca7b5 ,def456"), true},
		{"in_list rejects an absent entry", cond("hex", "in_list", "abc123,def456"), false},
		{"registration equals", cond("registration", "equals", "SE-RTM"), true},
		{"type_code equals", cond("type_code", "equals", "b38m"), true},
		{"country equals", cond("country", "equals", "Sweden"), true},
		{"airline contains", cond("airline", "contains", "scandinavian"), true},
	}
	for _, tc := range cases {
		if got := matchCondition(tc.c, s); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestMatchConditionAirlineMatchesCodeOrName(t *testing.T) {
	s := subject()
	if !matchCondition(cond("airline", "equals", "SAS"), s) {
		t.Error("airline equals should match the ICAO code")
	}
	if !matchCondition(cond("airline", "equals", "SK"), s) {
		t.Error("airline equals should match the IATA code")
	}
	if matchCondition(cond("airline", "equals", "RYR"), s) {
		t.Error("airline equals should not match an unrelated code")
	}
}

func TestMatchConditionRouteMatchesIcaoOrIata(t *testing.T) {
	s := subject()
	if !matchCondition(cond("origin", "equals", "ESSA"), s) {
		t.Error("origin should match the ICAO code")
	}
	if !matchCondition(cond("origin", "equals", "arn"), s) {
		t.Error("origin should match the IATA code, case-insensitively")
	}
	if !matchCondition(cond("destination", "equals", "CPH"), s) {
		t.Error("destination should match the IATA code")
	}
	if matchCondition(cond("destination", "equals", "LHR"), s) {
		t.Error("destination should not match an unrelated airport")
	}
}

func TestMatchConditionNumericOperators(t *testing.T) {
	s := subject()
	cases := []struct {
		name string
		c    WatchCondition
		want bool
	}{
		{"distance under", cond("distance_km", "under", "50"), true},
		{"distance over", cond("distance_km", "over", "50"), false},
		{"altitude over", cond("altitude_ft", "over", "30000"), true},
		{"altitude under", cond("altitude_ft", "under", "30000"), false},
		{"speed over", cond("speed_kt", "over", "400"), true},
		{"vertical rate is signed, under", cond("vertical_rate_fpm", "under", "-1000"), true},
		{"vertical rate is signed, over", cond("vertical_rate_fpm", "over", "1000"), false},
		{"unparseable value never matches", cond("altitude_ft", "over", "high"), false},
		{"empty value never matches", cond("altitude_ft", "over", ""), false},
	}
	for _, tc := range cases {
		if got := matchCondition(tc.c, s); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestMatchConditionMissingDataNeverMatches(t *testing.T) {
	empty := watchSubject{Hex: "4ca7b5"}
	cases := []WatchCondition{
		cond("manufacturer", "contains", "Boeing"),
		cond("manufacturer", "equals", ""),
		cond("country", "equals", "Sweden"),
		cond("origin", "equals", "ESSA"),
		cond("destination", "equals", "EKCH"),
		cond("airline", "contains", "SAS"),
		cond("callsign", "starts_with", "SAS"),
		cond("squawk", "in_list", "7500,7600,7700"),
		cond("distance_km", "under", "50"),
		cond("altitude_ft", "over", "1000"),
		cond("speed_kt", "under", "100"),
		cond("first_seen_ever", "is_true", ""),
	}
	for _, c := range cases {
		if matchCondition(c, empty) {
			t.Errorf("%s/%s matched on an empty subject", c.Field, c.Operator)
		}
	}
}

func TestMatchConditionFirstSeenEver(t *testing.T) {
	s := subject()
	s.FirstSeenEver = true
	if !matchCondition(cond("first_seen_ever", "is_true", ""), s) {
		t.Error("first_seen_ever should match when the flag is set")
	}
	s.FirstSeenEver = false
	if matchCondition(cond("first_seen_ever", "is_true", ""), s) {
		t.Error("first_seen_ever should not match when the flag is clear")
	}
}

func TestMatchConditionUnknownFieldOrOperatorNeverMatches(t *testing.T) {
	s := subject()
	if matchCondition(cond("colour", "equals", "blue"), s) {
		t.Error("an unknown field should not match")
	}
	if matchCondition(cond("manufacturer", "rhymes_with", "Boeing"), s) {
		t.Error("an unknown operator should not match")
	}
	if matchCondition(cond("manufacturer", "over", "5"), s) {
		t.Error("an operator not allowed for the field should not match")
	}
}

func TestMatchWatchCombinators(t *testing.T) {
	s := subject()
	hit := cond("manufacturer", "equals", "Boeing")
	miss := cond("manufacturer", "equals", "Airbus")

	cases := []struct {
		name string
		w    Watch
		want bool
	}{
		{"AND all true", Watch{Combinator: "AND", Conditions: []WatchCondition{hit, hit}}, true},
		{"AND one false", Watch{Combinator: "AND", Conditions: []WatchCondition{hit, miss}}, false},
		{"OR one true", Watch{Combinator: "OR", Conditions: []WatchCondition{miss, hit}}, true},
		{"OR none true", Watch{Combinator: "OR", Conditions: []WatchCondition{miss, miss}}, false},
		{"AND with no conditions", Watch{Combinator: "AND"}, false},
		{"OR with no conditions", Watch{Combinator: "OR"}, false},
	}
	for _, tc := range cases {
		if got := matchWatch(tc.w, s); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestValidateWatch(t *testing.T) {
	ok := Watch{Name: "Boeing close by", Combinator: "AND", Conditions: []WatchCondition{
		cond("manufacturer", "equals", "Boeing"),
		cond("distance_km", "under", "50"),
		cond("first_seen_ever", "is_true", ""),
	}}
	if err := validateWatch(ok); err != nil {
		t.Fatalf("valid watch rejected: %v", err)
	}

	bad := []struct {
		name string
		w    Watch
	}{
		{"empty name", Watch{Name: "  ", Combinator: "AND", Conditions: []WatchCondition{cond("hex", "equals", "abc")}}},
		{"bad combinator", Watch{Name: "x", Combinator: "XOR", Conditions: []WatchCondition{cond("hex", "equals", "abc")}}},
		{"no conditions", Watch{Name: "x", Combinator: "AND"}},
		{"unknown field", Watch{Name: "x", Combinator: "AND", Conditions: []WatchCondition{cond("colour", "equals", "blue")}}},
		{"operator not allowed for field", Watch{Name: "x", Combinator: "AND", Conditions: []WatchCondition{cond("distance_km", "contains", "5")}}},
		{"empty value on a value field", Watch{Name: "x", Combinator: "AND", Conditions: []WatchCondition{cond("hex", "equals", "")}}},
		{"non-numeric value on a numeric field", Watch{Name: "x", Combinator: "AND", Conditions: []WatchCondition{cond("altitude_ft", "over", "high")}}},
	}
	for _, tc := range bad {
		if err := validateWatch(tc.w); err == nil {
			t.Errorf("%s: expected an error, got nil", tc.name)
		}
	}
}

func TestWatchFieldListIsSelfConsistent(t *testing.T) {
	fields := watchFieldList()
	if len(fields) != 16 {
		t.Fatalf("got %d fields want 16", len(fields))
	}
	seen := map[string]bool{}
	for _, f := range fields {
		if seen[f.Key] {
			t.Errorf("duplicate field key %s", f.Key)
		}
		seen[f.Key] = true
		if f.Label == "" {
			t.Errorf("field %s has no label", f.Key)
		}
		if len(f.Operators) == 0 {
			t.Errorf("field %s has no operators", f.Key)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./core/ -run 'TestMatch|TestValidateWatch|TestWatchField' -v`
Expected: FAIL — `undefined: watchSubject`, `undefined: matchCondition`, etc.

- [ ] **Step 3: Write the implementation**

Create `core/watches-match.go`:

```go
package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Watch is one user-defined rule. Conditions are combined by Combinator; a
// watch with no conditions never matches. AppriseKey overrides the global
// apprise_config_key for this watch when non-empty.
type Watch struct {
	ID         int              `json:"id"`
	Name       string           `json:"name"`
	Enabled    bool             `json:"enabled"`
	Combinator string           `json:"combinator"`
	AppriseKey string           `json:"apprise_key"`
	Conditions []WatchCondition `json:"conditions"`
	CreatedAt  time.Time        `json:"created_at"`
	UpdatedAt  time.Time        `json:"updated_at"`
}

// WatchCondition is a single criterion. Value is always stored as text and
// parsed per field kind at match time.
type WatchCondition struct {
	ID       int    `json:"id"`
	WatchID  int    `json:"watch_id"`
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
}

// watchSubject is the flattened, comparable view of one aircraft that
// conditions are evaluated against. The Has* flags distinguish "genuinely
// zero" from "no data", because a missing value must never match.
//
// VerticalRateFpm has no Has flag on purpose: readsb reports 0 both for level
// flight and for an absent baro_rate, and treating that as level flight gives
// the right answer for signed over/under comparisons either way.
type watchSubject struct {
	Hex             string
	Callsign        string
	Registration    string
	TypeCode        string
	Model           string
	Manufacturer    string
	Country         string
	Airline         string
	AirlineCodes    []string
	Origin          []string
	Destination     []string
	Squawk          string
	DistanceKm      float64
	HasPosition     bool
	AltitudeFt      float64
	HasAltitude     bool
	SpeedKt         float64
	HasSpeed        bool
	VerticalRateFpm float64
	FirstSeenEver   bool
}

// Field kinds drive the value input the frontend renders and the validation
// the API applies.
const (
	watchKindString = "string"
	watchKindNumber = "number"
	watchKindFlag   = "flag"
)

// watchField describes one selectable criterion. This registry is the single
// source of truth: matching, validation and the frontend dropdowns all read it.
type watchField struct {
	Key       string   `json:"key"`
	Label     string   `json:"label"`
	Kind      string   `json:"kind"`
	Unit      string   `json:"unit,omitempty"`
	Hint      string   `json:"hint,omitempty"`
	Operators []string `json:"operators"`
}

var watchFields = []watchField{
	{Key: "manufacturer", Label: "Manufacturer", Kind: watchKindString, Operators: []string{"equals", "contains"},
		Hint: "From the adsbdb registration lookup. Often missing for military and state aircraft."},
	{Key: "type_code", Label: "Type code (ICAO)", Kind: watchKindString, Operators: []string{"equals", "contains"},
		Hint: "The four-character ICAO type designator, e.g. B738."},
	{Key: "model", Label: "Model", Kind: watchKindString, Operators: []string{"equals", "contains"},
		Hint: "Full model name, e.g. Boeing 737 MAX 8."},
	{Key: "country", Label: "Country of registration", Kind: watchKindString, Operators: []string{"equals"},
		Hint: "Where the aircraft is registered — not the country it is flying to or from."},
	{Key: "airline", Label: "Airline", Kind: watchKindString, Operators: []string{"equals", "contains"},
		Hint: "Matches the airline name or its ICAO/IATA code."},
	{Key: "origin", Label: "Origin airport", Kind: watchKindString, Operators: []string{"equals"},
		Hint: "ICAO or IATA code, e.g. ESSA or ARN."},
	{Key: "destination", Label: "Destination airport", Kind: watchKindString, Operators: []string{"equals"},
		Hint: "ICAO or IATA code, e.g. EKCH or CPH."},
	{Key: "registration", Label: "Registration", Kind: watchKindString, Operators: []string{"equals", "contains"}},
	{Key: "hex", Label: "ICAO 24-bit (hex)", Kind: watchKindString, Operators: []string{"equals", "in_list"},
		Hint: "Use a comma-separated list to watch several specific aircraft."},
	{Key: "distance_km", Label: "Distance from me", Kind: watchKindNumber, Unit: "km", Operators: []string{"over", "under"},
		Hint: "Skystats only tracks aircraft inside RADIUS, so \"over\" is capped by that radius."},
	{Key: "altitude_ft", Label: "Altitude", Kind: watchKindNumber, Unit: "ft", Operators: []string{"over", "under"},
		Hint: "Barometric altitude, the same value the Highest/Lowest records use."},
	{Key: "speed_kt", Label: "Ground speed", Kind: watchKindNumber, Unit: "kt", Operators: []string{"over", "under"}},
	{Key: "squawk", Label: "Squawk", Kind: watchKindString, Operators: []string{"equals", "in_list"},
		Hint: "Emergency codes: 7500 hijack, 7600 radio failure, 7700 general emergency."},
	{Key: "first_seen_ever", Label: "First time ever seen", Kind: watchKindFlag, Operators: []string{"is_true"},
		Hint: "Matches only during the very first sighting of an aircraft, never again."},
	{Key: "vertical_rate_fpm", Label: "Vertical rate", Kind: watchKindNumber, Unit: "ft/min", Operators: []string{"over", "under"},
		Hint: "Signed: positive is climbing, negative is descending."},
	{Key: "callsign", Label: "Callsign", Kind: watchKindString, Operators: []string{"equals", "contains", "starts_with"}},
}

var watchFieldsByKey = func() map[string]watchField {
	m := make(map[string]watchField, len(watchFields))
	for _, f := range watchFields {
		m[f.Key] = f
	}
	return m
}()

// watchFieldList returns the field registry for the API and the frontend.
func watchFieldList() []watchField { return watchFields }

func (f watchField) allows(operator string) bool {
	for _, op := range f.Operators {
		if op == operator {
			return true
		}
	}
	return false
}

// matchWatch reports whether the subject satisfies the watch. A watch with no
// conditions never matches, whichever combinator it uses.
func matchWatch(w Watch, s watchSubject) bool {
	if len(w.Conditions) == 0 {
		return false
	}
	if w.Combinator == "OR" {
		for _, c := range w.Conditions {
			if matchCondition(c, s) {
				return true
			}
		}
		return false
	}
	for _, c := range w.Conditions {
		if !matchCondition(c, s) {
			return false
		}
	}
	return true
}

// matchCondition evaluates one criterion. Unknown fields, operators the field
// does not allow, and absent subject data all evaluate to false.
func matchCondition(c WatchCondition, s watchSubject) bool {
	field, ok := watchFieldsByKey[c.Field]
	if !ok || !field.allows(c.Operator) {
		return false
	}

	switch field.Kind {
	case watchKindFlag:
		return c.Field == "first_seen_ever" && s.FirstSeenEver
	case watchKindNumber:
		value, err := strconv.ParseFloat(strings.TrimSpace(c.Value), 64)
		if err != nil {
			return false
		}
		subjectValue, present := numericSubjectValue(c.Field, s)
		if !present {
			return false
		}
		if c.Operator == "over" {
			return subjectValue > value
		}
		return subjectValue < value
	default:
		return matchStringCondition(c, stringSubjectValues(c.Field, s))
	}
}

// numericSubjectValue returns the subject's value for a numeric field and
// whether the aircraft actually reported it.
func numericSubjectValue(field string, s watchSubject) (float64, bool) {
	switch field {
	case "distance_km":
		return s.DistanceKm, s.HasPosition
	case "altitude_ft":
		return s.AltitudeFt, s.HasAltitude
	case "speed_kt":
		return s.SpeedKt, s.HasSpeed
	case "vertical_rate_fpm":
		return s.VerticalRateFpm, true
	}
	return 0, false
}

// stringSubjectValues returns every value a string field may legitimately
// match against — airline matches its name or either code, and an airport
// matches its ICAO or IATA code.
func stringSubjectValues(field string, s watchSubject) []string {
	switch field {
	case "manufacturer":
		return []string{s.Manufacturer}
	case "type_code":
		return []string{s.TypeCode}
	case "model":
		return []string{s.Model}
	case "country":
		return []string{s.Country}
	case "airline":
		return append([]string{s.Airline}, s.AirlineCodes...)
	case "origin":
		return s.Origin
	case "destination":
		return s.Destination
	case "registration":
		return []string{s.Registration}
	case "hex":
		return []string{s.Hex}
	case "squawk":
		return []string{s.Squawk}
	case "callsign":
		return []string{s.Callsign}
	}
	return nil
}

// matchStringCondition applies a string operator case-insensitively across all
// candidate values. An empty condition value or an all-empty subject never
// matches.
func matchStringCondition(c WatchCondition, subjectValues []string) bool {
	value := strings.ToUpper(strings.TrimSpace(c.Value))
	if value == "" {
		return false
	}

	var wanted []string
	if c.Operator == "in_list" {
		for _, part := range strings.Split(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				wanted = append(wanted, part)
			}
		}
	} else {
		wanted = []string{value}
	}

	for _, raw := range subjectValues {
		subject := strings.ToUpper(strings.TrimSpace(raw))
		if subject == "" {
			continue
		}
		for _, w := range wanted {
			switch c.Operator {
			case "contains":
				if strings.Contains(subject, w) {
					return true
				}
			case "starts_with":
				if strings.HasPrefix(subject, w) {
					return true
				}
			default: // equals, in_list
				if subject == w {
					return true
				}
			}
		}
	}
	return false
}

// validateWatch checks a watch coming in over the API before it is persisted.
func validateWatch(w Watch) error {
	if strings.TrimSpace(w.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if len(w.Name) > 200 {
		return fmt.Errorf("name must be 200 characters or fewer")
	}
	if w.Combinator != "AND" && w.Combinator != "OR" {
		return fmt.Errorf("combinator must be AND or OR")
	}
	if len(w.Conditions) == 0 {
		return fmt.Errorf("at least one condition is required")
	}
	for i, c := range w.Conditions {
		field, ok := watchFieldsByKey[c.Field]
		if !ok {
			return fmt.Errorf("condition %d: unknown field %q", i+1, c.Field)
		}
		if !field.allows(c.Operator) {
			return fmt.Errorf("condition %d: operator %q is not valid for field %q", i+1, c.Operator, c.Field)
		}
		if field.Kind == watchKindFlag {
			continue
		}
		if strings.TrimSpace(c.Value) == "" {
			return fmt.Errorf("condition %d: a value is required for field %q", i+1, c.Field)
		}
		if field.Kind == watchKindNumber {
			if _, err := strconv.ParseFloat(strings.TrimSpace(c.Value), 64); err != nil {
				return fmt.Errorf("condition %d: value for field %q must be a number", i+1, c.Field)
			}
		}
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./core/ -run 'TestMatch|TestValidateWatch|TestWatchField' -v`
Expected: PASS for every test.

- [ ] **Step 5: Commit**

```bash
git add core/watches-match.go core/watches-match_test.go
git commit -m "feat: watch field registry, subject model and matching predicates"
```

---

### Task 3: Extend the per-tick enrichment and share it between Current Sightings and watches

The 2s tick already runs one enrichment query for Current Sightings. Watches need manufacturer, country and airport ICAO codes on top of what it fetches, and must not run a second query. This task widens the query and hoists the call into `updateAircraftDatabase` so both consumers share one result.

**Files:**
- Modify: `core/current-sightings.go:41-53` (the `aircraftEnrichment` struct), `core/current-sightings.go:154-216` (`fetchAircraftEnrichment`), `core/current-sightings.go:218-261` (`refreshCurrentSightings`)
- Modify: `core/aircraft.go:20-56` (`updateAircraftDatabase`)
- Test: `core/current-sightings_test.go` (existing file — the existing tests must keep passing unchanged)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces (used by Tasks 4, 6):
  - `aircraftEnrichment` gains `Manufacturer, AircraftType, CountryName, AirlineIcao, AirlineIata, OriginIcao, DestinationIcao *string`
  - `func enrichAircraftSnapshot(pg *postgres, aircraft []Aircraft) map[string]aircraftEnrichment`
  - `refreshCurrentSightings` signature changes to `func refreshCurrentSightings(nowEpoch float64, aircraft []Aircraft, enrichment map[string]aircraftEnrichment)` — it no longer takes `pg` and no longer fetches.

- [ ] **Step 1: Widen the enrichment struct**

In `core/current-sightings.go`, replace the `aircraftEnrichment` struct with:

```go
// aircraftEnrichment holds the per-hex details that live in Postgres rather
// than in the readsb feed. Consumed by both Current Sightings and the watch
// engine, so it carries fields neither uses on its own.
type aircraftEnrichment struct {
	Registration     *string
	IcaoType         *string
	AircraftType     *string
	Manufacturer     *string
	CountryName      *string
	RegisteredOwner  *string
	AirlineName      *string
	AirlineIcao      *string
	AirlineIata      *string
	OriginIata       *string
	OriginIcao       *string
	OriginName       *string
	DestinationIata  *string
	DestinationIcao  *string
	DestinationName  *string
	InterestingGroup *string
}
```

- [ ] **Step 2: Widen the enrichment query**

In `core/current-sightings.go`, replace the body of `fetchAircraftEnrichment` (keep the signature) with:

```go
	query := `
		SELECT s.hex,
		       reg.registration,
		       reg.icao_type,
		       reg.type,
		       reg.manufacturer,
		       reg.registered_owner_country_name,
		       reg.registered_owner,
		       rt.airline_name,
		       rt.airline_icao,
		       rt.airline_iata,
		       rt.origin_iata_code,
		       rt.origin_icao_code,
		       rt.origin_name,
		       rt.destination_iata_code,
		       rt.destination_icao_code,
		       rt.destination_name,
		       ia."group"
		FROM unnest($1::text[], $2::text[]) AS s(hex, flight)
		LEFT JOIN registration_data reg ON reg.mode_s = s.hex
		LEFT JOIN LATERAL (
			SELECT airline_name, airline_icao, airline_iata,
			       origin_iata_code, origin_icao_code, origin_name,
			       destination_iata_code, destination_icao_code, destination_name
			FROM route_data
			WHERE route_callsign = s.flight
			LIMIT 1
		) rt ON true
		LEFT JOIN LATERAL (
			SELECT "group"
			FROM interesting_aircraft
			WHERE icao = UPPER(s.hex)
			LIMIT 1
		) ia ON true`

	rows, err := pg.db.Query(context.Background(), query, hexes, flights)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	enrichment := make(map[string]aircraftEnrichment, len(hexes))

	for rows.Next() {
		var hex string
		var e aircraftEnrichment

		err := rows.Scan(
			&hex,
			&e.Registration,
			&e.IcaoType,
			&e.AircraftType,
			&e.Manufacturer,
			&e.CountryName,
			&e.RegisteredOwner,
			&e.AirlineName,
			&e.AirlineIcao,
			&e.AirlineIata,
			&e.OriginIata,
			&e.OriginIcao,
			&e.OriginName,
			&e.DestinationIata,
			&e.DestinationIcao,
			&e.DestinationName,
			&e.InterestingGroup,
		)
		if err != nil {
			log.Error().Err(err).Msg("fetchAircraftEnrichment() - error scanning rows")
			continue
		}

		enrichment[hex] = e
	}

	return enrichment, rows.Err()
```

- [ ] **Step 3: Add the shared fetch helper and slim down refreshCurrentSightings**

In `core/current-sightings.go`, replace the whole `refreshCurrentSightings` function with:

```go
// enrichAircraftSnapshot fetches the Postgres-side details for one readsb
// snapshot, once per tick, for every consumer that needs them. On error it
// returns an empty map so callers degrade to live-only data rather than
// freezing on stale values.
func enrichAircraftSnapshot(pg *postgres, aircraft []Aircraft) map[string]aircraftEnrichment {

	if len(aircraft) == 0 {
		return map[string]aircraftEnrichment{}
	}

	hexes := make([]string, 0, len(aircraft))
	flights := make([]string, 0, len(aircraft))
	for _, a := range aircraft {
		hexes = append(hexes, a.Hex)
		flights = append(flights, a.Flight)
	}

	enrichment, err := fetchAircraftEnrichment(pg, hexes, flights)
	if err != nil {
		log.Error().Err(err).Msg("enrichAircraftSnapshot() - unable to fetch enrichment")
		return map[string]aircraftEnrichment{}
	}

	return enrichment
}

// refreshCurrentSightings rebuilds the Current Sightings payload from the
// snapshot the ingest ticker just processed.
func refreshCurrentSightings(nowEpoch float64, aircraft []Aircraft, enrichment map[string]aircraftEnrichment) {

	// A non-JSON readsb response (e.g. an HTML error page) leaves nowEpoch at
	// its zero value, since json.Unmarshal's error is discarded upstream. Bail
	// out rather than stamping the store with a 1970 timestamp and wiping the
	// aircraft list — let it age like any other outage instead.
	if nowEpoch == 0 {
		return
	}

	// Use the local clock rather than readsb's nowEpoch: the frontend compares
	// generatedAt against the viewer's Date.now(), so deriving it from the
	// feeder's (potentially unsynced) clock would make staleness detection
	// unreliable.
	generatedAt := time.Now()

	if len(aircraft) == 0 {
		currentSightings.replace([]CurrentSighting{}, generatedAt)
		return
	}

	sightings := buildCurrentSightings(aircraft, enrichment, nowEpoch, func(lat, lon float64) float64 {
		return *getDistance([]float64{lon, lat})
	})

	currentSightings.replace(sightings, generatedAt)
}
```

- [ ] **Step 4: Update the caller**

In `core/aircraft.go`, replace lines 54-55 (`pg.updateDatabase(...)` and `refreshCurrentSightings(...)`) with:

```go
	pg.updateDatabase(response.Now, aircraftsInRange)

	// One enrichment round trip per tick, shared by every consumer of the
	// snapshot.
	enrichment := enrichAircraftSnapshot(pg, aircraftsInRange)
	refreshCurrentSightings(response.Now, aircraftsInRange, enrichment)
```

(The watch engine call is added to this same spot in Task 6.)

- [ ] **Step 5: Build and run the existing tests**

Run: `go build ./... && go test ./...`
Expected: build succeeds; `ok github.com/tomcarman/skystats/core`. The existing `TestBuildCurrentSightings*` tests are untouched by this change and must still pass — `buildCurrentSightings` keeps its signature.

- [ ] **Step 6: Commit**

```bash
git add core/current-sightings.go core/aircraft.go
git commit -m "refactor: widen per-tick enrichment and share it across snapshot consumers"
```

---

### Task 4: known_aircraft maintenance and first-sighting tracking

`first_seen_ever` must stay true for the whole of an aircraft's first sighting, not just the single tick it appeared, or it could never combine with an AND condition that becomes true a few seconds later.

**Files:**
- Create: `core/watches-firstseen.go`
- Test: `core/watches-firstseen_test.go`

**Interfaces:**
- Consumes: `known_aircraft` from Task 1.
- Produces (used by Task 6):
  - `func markKnownAircraft(pg *postgres, hexes []string) map[string]bool`
  - `type firstSeenTracker struct { sessions map[string]time.Time }`
  - `func newFirstSeenTracker() *firstSeenTracker`
  - `func (t *firstSeenTracker) update(snapshotHexes []string, brandNew map[string]bool, now time.Time, grace time.Duration) map[string]bool`

- [ ] **Step 1: Write the failing tests**

Create `core/watches-firstseen_test.go`:

```go
package main

import (
	"testing"
	"time"
)

func TestFirstSeenTrackerKeepsFlagForWholeSighting(t *testing.T) {
	tracker := newFirstSeenTracker()
	now := time.Unix(1785399041, 0)
	grace := 10 * time.Minute

	// Tick 1: the aircraft is brand new.
	got := tracker.update([]string{"aaa111", "bbb222"}, map[string]bool{"aaa111": true}, now, grace)
	if !got["aaa111"] {
		t.Error("a brand new hex should be flagged on the tick it appears")
	}
	if got["bbb222"] {
		t.Error("an already-known hex should not be flagged")
	}

	// Tick 2, four minutes later: still the same sighting, still flagged.
	got = tracker.update([]string{"aaa111", "bbb222"}, nil, now.Add(4*time.Minute), grace)
	if !got["aaa111"] {
		t.Error("the flag should survive for the rest of the sighting")
	}
}

func TestFirstSeenTrackerExpiresAfterGrace(t *testing.T) {
	tracker := newFirstSeenTracker()
	now := time.Unix(1785399041, 0)
	grace := 10 * time.Minute

	tracker.update([]string{"aaa111"}, map[string]bool{"aaa111": true}, now, grace)

	// Gone from the snapshot but still inside the grace window.
	got := tracker.update(nil, nil, now.Add(5*time.Minute), grace)
	if !got["aaa111"] {
		t.Error("a brief dropout should not end the first sighting")
	}

	// Gone past the grace window: the first sighting is over for good.
	got = tracker.update(nil, nil, now.Add(20*time.Minute), grace)
	if got["aaa111"] {
		t.Error("the flag should expire once the grace window has passed")
	}

	// The same hex coming back later is no longer a first sighting.
	got = tracker.update([]string{"aaa111"}, nil, now.Add(30*time.Minute), grace)
	if got["aaa111"] {
		t.Error("a returning hex must not be flagged as first-ever seen again")
	}
}

func TestFirstSeenTrackerReturnsEmptySetWhenNothingIsNew(t *testing.T) {
	tracker := newFirstSeenTracker()
	got := tracker.update([]string{"aaa111", "bbb222"}, nil, time.Unix(1785399041, 0), 10*time.Minute)
	if len(got) != 0 {
		t.Errorf("got %d flagged hexes want 0", len(got))
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./core/ -run TestFirstSeenTracker -v`
Expected: FAIL — `undefined: newFirstSeenTracker`.

- [ ] **Step 3: Write the implementation**

Create `core/watches-firstseen.go`:

```go
package main

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

// markKnownAircraft records every hex in the snapshot in the permanent
// known_aircraft archive and returns the set that had never been seen before.
// known_aircraft is deliberately independent of aircraft_data and
// flight_history so retention on those tables can never resurrect a hex as
// "first ever seen".
func markKnownAircraft(pg *postgres, hexes []string) map[string]bool {

	brandNew := map[string]bool{}
	if len(hexes) == 0 {
		return brandNew
	}

	rows, err := pg.db.Query(context.Background(), `
		INSERT INTO known_aircraft (hex)
		SELECT DISTINCT unnest($1::text[])
		ON CONFLICT (hex) DO NOTHING
		RETURNING hex`, hexes)
	if err != nil {
		log.Error().Err(err).Msg("markKnownAircraft() - insert failed")
		return brandNew
	}
	defer rows.Close()

	for rows.Next() {
		var hex string
		if err := rows.Scan(&hex); err != nil {
			log.Error().Err(err).Msg("markKnownAircraft() - error scanning rows")
			continue
		}
		brandNew[hex] = true
	}

	if err := rows.Err(); err != nil {
		log.Error().Err(err).Msg("markKnownAircraft() - row iteration failed")
	}

	return brandNew
}

// firstSeenTracker keeps the first_seen_ever flag true for the whole of an
// aircraft's first sighting rather than only the tick it appeared, so the
// condition can be combined with others that become true moments later.
//
// State is in-memory only. A daemon restart during an aircraft's very first
// sighting loses the flag for that sighting; the permanent record in
// known_aircraft is what prevents it from ever being flagged again.
type firstSeenTracker struct {
	sessions map[string]time.Time
}

func newFirstSeenTracker() *firstSeenTracker {
	return &firstSeenTracker{sessions: map[string]time.Time{}}
}

// update folds this tick's brand-new hexes into the tracked set, refreshes the
// ones still visible, drops the ones absent for longer than grace, and returns
// the hexes currently in their first sighting.
func (t *firstSeenTracker) update(snapshotHexes []string, brandNew map[string]bool, now time.Time, grace time.Duration) map[string]bool {

	for hex := range brandNew {
		t.sessions[hex] = now
	}

	for _, hex := range snapshotHexes {
		if _, tracked := t.sessions[hex]; tracked {
			t.sessions[hex] = now
		}
	}

	current := make(map[string]bool, len(t.sessions))
	for hex, lastSeen := range t.sessions {
		if now.Sub(lastSeen) > grace {
			delete(t.sessions, hex)
			continue
		}
		current[hex] = true
	}

	return current
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./core/ -run TestFirstSeenTracker -v`
Expected: PASS for all three tests.

- [ ] **Step 5: Commit**

```bash
git add core/watches-firstseen.go core/watches-firstseen_test.go
git commit -m "feat: permanent known_aircraft archive and first-sighting tracking"
```

---

### Task 5: Watch store — load, cache and persist watch definitions

**Files:**
- Create: `core/watches-store.go`

**Interfaces:**
- Consumes: `Watch`, `WatchCondition` from Task 2; the `watches` / `watch_conditions` tables from Task 1.
- Produces (used by Tasks 6, 8):
  - `var watchCache = &watchStore{}`
  - `func (s *watchStore) enabled(pg *postgres) []Watch` — cached read of enabled watches
  - `func (s *watchStore) invalidate()`
  - `func listWatches(pg *postgres) ([]Watch, error)` — all watches, enabled or not, conditions attached
  - `func getWatch(pg *postgres, id int) (*Watch, error)` — one watch with conditions, `(nil, nil)` when it does not exist
  - `func createWatch(pg *postgres, w Watch) (*Watch, error)`
  - `func updateWatch(pg *postgres, id int, w Watch) (*Watch, error)`
  - `func deleteWatch(pg *postgres, id int) error`

- [ ] **Step 1: Write the implementation**

There is no DB test harness in this repo, so this task has no unit test; it is verified by `go build` plus the runtime check in Task 12.

Create `core/watches-store.go`:

```go
package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

// watchStore caches the enabled watch definitions so the 2s tick does not
// re-read them from Postgres every time. The daemon is the only writer, so
// invalidating on write keeps the cache exact rather than eventually
// consistent.
type watchStore struct {
	mu      sync.RWMutex
	watches []Watch
	loaded  bool
}

var watchCache = &watchStore{}

func (s *watchStore) invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loaded = false
	s.watches = nil
}

// enabled returns the enabled watches, loading them on first use and after any
// invalidation. On a load error it returns an empty slice and leaves the cache
// unloaded so the next tick retries.
func (s *watchStore) enabled(pg *postgres) []Watch {

	s.mu.RLock()
	if s.loaded {
		watches := s.watches
		s.mu.RUnlock()
		return watches
	}
	s.mu.RUnlock()

	all, err := listWatches(pg)
	if err != nil {
		log.Error().Err(err).Msg("watchStore.enabled() - unable to load watches")
		return nil
	}

	enabled := make([]Watch, 0, len(all))
	for _, w := range all {
		if w.Enabled && len(w.Conditions) > 0 {
			enabled = append(enabled, w)
		}
	}

	s.mu.Lock()
	s.watches = enabled
	s.loaded = true
	s.mu.Unlock()

	return enabled
}

// listWatches returns every watch with its conditions attached, newest first.
func listWatches(pg *postgres) ([]Watch, error) {

	rows, err := pg.db.Query(context.Background(), `
		SELECT id, name, enabled, combinator, COALESCE(apprise_key, ''), created_at, updated_at
		FROM watches
		ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	watches := []Watch{}
	byID := map[int]int{}

	for rows.Next() {
		var w Watch
		if err := rows.Scan(&w.ID, &w.Name, &w.Enabled, &w.Combinator, &w.AppriseKey, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, err
		}
		w.Conditions = []WatchCondition{}
		byID[w.ID] = len(watches)
		watches = append(watches, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(watches) == 0 {
		return watches, nil
	}

	condRows, err := pg.db.Query(context.Background(), `
		SELECT id, watch_id, field, operator, value
		FROM watch_conditions
		ORDER BY watch_id, id`)
	if err != nil {
		return nil, err
	}
	defer condRows.Close()

	for condRows.Next() {
		var c WatchCondition
		if err := condRows.Scan(&c.ID, &c.WatchID, &c.Field, &c.Operator, &c.Value); err != nil {
			return nil, err
		}
		if idx, ok := byID[c.WatchID]; ok {
			watches[idx].Conditions = append(watches[idx].Conditions, c)
		}
	}

	return watches, condRows.Err()
}

// getWatch returns a single watch with its conditions.
func getWatch(pg *postgres, id int) (*Watch, error) {

	var w Watch
	err := pg.db.QueryRow(context.Background(), `
		SELECT id, name, enabled, combinator, COALESCE(apprise_key, ''), created_at, updated_at
		FROM watches WHERE id = $1`, id).
		Scan(&w.ID, &w.Name, &w.Enabled, &w.Combinator, &w.AppriseKey, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	rows, err := pg.db.Query(context.Background(), `
		SELECT id, watch_id, field, operator, value
		FROM watch_conditions WHERE watch_id = $1 ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	w.Conditions = []WatchCondition{}
	for rows.Next() {
		var c WatchCondition
		if err := rows.Scan(&c.ID, &c.WatchID, &c.Field, &c.Operator, &c.Value); err != nil {
			return nil, err
		}
		w.Conditions = append(w.Conditions, c)
	}

	return &w, rows.Err()
}

// createWatch inserts a watch and its conditions in one transaction.
func createWatch(pg *postgres, w Watch) (*Watch, error) {

	ctx := context.Background()
	tx, err := pg.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var id int
	err = tx.QueryRow(ctx, `
		INSERT INTO watches (name, enabled, combinator, apprise_key)
		VALUES ($1, $2, $3, NULLIF($4, ''))
		RETURNING id`, w.Name, w.Enabled, w.Combinator, w.AppriseKey).Scan(&id)
	if err != nil {
		return nil, err
	}

	if err := insertConditions(ctx, tx, id, w.Conditions); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	watchCache.invalidate()
	return getWatch(pg, id)
}

// updateWatch replaces a watch and its full condition list. Conditions are
// deleted and re-inserted rather than diffed: the list is short and a full
// replace keeps the API contract simple.
func updateWatch(pg *postgres, id int, w Watch) (*Watch, error) {

	ctx := context.Background()
	tx, err := pg.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx, `
		UPDATE watches
		SET name = $2, enabled = $3, combinator = $4, apprise_key = NULLIF($5, ''), updated_at = now()
		WHERE id = $1`, id, w.Name, w.Enabled, w.Combinator, w.AppriseKey)
	if err != nil {
		return nil, err
	}
	if ct.RowsAffected() == 0 {
		return nil, nil
	}

	if _, err := tx.Exec(ctx, `DELETE FROM watch_conditions WHERE watch_id = $1`, id); err != nil {
		return nil, err
	}
	if err := insertConditions(ctx, tx, id, w.Conditions); err != nil {
		return nil, err
	}

	// The condition set may have changed, so any aircraft currently counted as
	// matching is no longer trustworthy. Clearing lets the next tick re-decide
	// and notify afresh under the new rules.
	if _, err := tx.Exec(ctx, `DELETE FROM watch_active_matches WHERE watch_id = $1`, id); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	watchCache.invalidate()
	activeMatchCache.forget(id)
	return getWatch(pg, id)
}

func insertConditions(ctx context.Context, tx pgx.Tx, watchID int, conditions []WatchCondition) error {
	for _, c := range conditions {
		_, err := tx.Exec(ctx, `
			INSERT INTO watch_conditions (watch_id, field, operator, value)
			VALUES ($1, $2, $3, $4)`, watchID, c.Field, c.Operator, c.Value)
		if err != nil {
			return fmt.Errorf("insert condition %s/%s: %w", c.Field, c.Operator, err)
		}
	}
	return nil
}

// deleteWatch removes a watch. Conditions and active matches cascade;
// notification history is kept with watch_id set to NULL.
func deleteWatch(pg *postgres, id int) error {

	ct, err := pg.db.Exec(context.Background(), `DELETE FROM watches WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}

	watchCache.invalidate()
	activeMatchCache.forget(id)
	return nil
}
```

- [ ] **Step 2: Note the forward reference**

`activeMatchCache` is defined in Task 6. `go build` will fail until Task 6 lands — that is expected and is why this step does not build. Do not stub it; Task 6 supplies it.

- [ ] **Step 3: Commit**

```bash
git add core/watches-store.go
git commit -m "feat: watch definition store with invalidate-on-write cache"
```

---

### Task 6: Watch evaluation engine wired into the ingest tick

**Files:**
- Create: `core/watches-engine.go`
- Test: `core/watches-engine_test.go`
- Modify: `core/aircraft.go` (the block added in Task 3, Step 4)
- Modify: `core/core.go:98` area (initialise the engine next to the notifier)

**Interfaces:**
- Consumes: `matchWatch`, `Watch`, `watchSubject` (Task 2); `aircraftEnrichment`, `enrichAircraftSnapshot` (Task 3); `markKnownAircraft`, `firstSeenTracker` (Task 4); `watchCache` (Task 5); `notifier.NotifyWatch` (Task 7).
- Produces (used by Tasks 5, 7, 8, 12):
  - `type watchKey struct { WatchID int; Hex string }`
  - `var activeMatchCache = newActiveMatchStore()`
  - `func (s *activeMatchStore) forget(watchID int)`
  - `func diffMatches(current map[watchKey]bool, previous map[watchKey]time.Time, now time.Time, grace time.Duration) (started, ended []watchKey)`
  - `func buildWatchSubject(a Aircraft, e aircraftEnrichment, distanceKm float64, hasPosition, firstSeenEver bool) watchSubject`
  - `func evaluateWatches(pg *postgres, aircraft []Aircraft, enrichment map[string]aircraftEnrichment)`
  - `func initWatchEngine(pg *postgres)`
  - `const watchMatchGrace = 10 * time.Minute`

- [ ] **Step 1: Write the failing tests**

Create `core/watches-engine_test.go`:

```go
package main

import (
	"sort"
	"testing"
	"time"
)

func keys(k []watchKey) []string {
	out := make([]string, 0, len(k))
	for _, key := range k {
		out = append(out, key.Hex)
	}
	sort.Strings(out)
	return out
}

func TestDiffMatchesReportsNewMatches(t *testing.T) {
	now := time.Unix(1785399041, 0)
	current := map[watchKey]bool{
		{WatchID: 1, Hex: "aaa111"}: true,
		{WatchID: 1, Hex: "bbb222"}: true,
	}
	previous := map[watchKey]time.Time{
		{WatchID: 1, Hex: "aaa111"}: now.Add(-time.Minute),
	}

	started, ended := diffMatches(current, previous, now, 10*time.Minute)

	if got := keys(started); len(got) != 1 || got[0] != "bbb222" {
		t.Errorf("started: got %v want [bbb222]", got)
	}
	if len(ended) != 0 {
		t.Errorf("ended: got %v want none", keys(ended))
	}
}

func TestDiffMatchesKeepsStaleMatchesInsideGrace(t *testing.T) {
	now := time.Unix(1785399041, 0)
	previous := map[watchKey]time.Time{
		{WatchID: 1, Hex: "aaa111"}: now.Add(-2 * time.Minute),
	}

	started, ended := diffMatches(nil, previous, now, 10*time.Minute)

	if len(started) != 0 {
		t.Errorf("started: got %v want none", keys(started))
	}
	if len(ended) != 0 {
		t.Errorf("a dropout inside the grace window should not end the match, got %v", keys(ended))
	}
}

func TestDiffMatchesEndsMatchesPastGrace(t *testing.T) {
	now := time.Unix(1785399041, 0)
	previous := map[watchKey]time.Time{
		{WatchID: 1, Hex: "aaa111"}: now.Add(-11 * time.Minute),
		{WatchID: 1, Hex: "bbb222"}: now.Add(-time.Minute),
	}

	started, ended := diffMatches(nil, previous, now, 10*time.Minute)

	if len(started) != 0 {
		t.Errorf("started: got %v want none", keys(started))
	}
	if got := keys(ended); len(got) != 1 || got[0] != "aaa111" {
		t.Errorf("ended: got %v want [aaa111]", got)
	}
}

func TestDiffMatchesTreatsSameHexOnDifferentWatchesSeparately(t *testing.T) {
	now := time.Unix(1785399041, 0)
	current := map[watchKey]bool{
		{WatchID: 1, Hex: "aaa111"}: true,
		{WatchID: 2, Hex: "aaa111"}: true,
	}
	previous := map[watchKey]time.Time{
		{WatchID: 1, Hex: "aaa111"}: now.Add(-time.Minute),
	}

	started, _ := diffMatches(current, previous, now, 10*time.Minute)

	if len(started) != 1 || started[0].WatchID != 2 {
		t.Errorf("started: got %+v want one entry for watch 2", started)
	}
}

func strPtr(s string) *string { return &s }

func TestBuildWatchSubjectPrefersLiveValuesOverDatabase(t *testing.T) {
	a := Aircraft{Hex: "4ca7b5", Flight: "SAS1234", R: "SE-RTM", T: "B38M", Desc: "Boeing 737 MAX 8",
		AltBaro: 31000, Gs: 450, BaroRate: -1200, Squawk: "2000"}
	e := aircraftEnrichment{
		Registration: strPtr("SE-XXX"),
		IcaoType:     strPtr("B737"),
		AircraftType: strPtr("Boeing 737-800"),
		Manufacturer: strPtr("Boeing"),
		CountryName:  strPtr("Sweden"),
		AirlineName:  strPtr("Scandinavian Airlines"),
		AirlineIcao:  strPtr("SAS"),
		AirlineIata:  strPtr("SK"),
		OriginIcao:   strPtr("ESSA"),
		OriginIata:   strPtr("ARN"),
	}

	s := buildWatchSubject(a, e, 42.5, true, false)

	if s.Registration != "SE-RTM" {
		t.Errorf("registration: got %s want SE-RTM (live value wins)", s.Registration)
	}
	if s.TypeCode != "B38M" {
		t.Errorf("type code: got %s want B38M (live value wins)", s.TypeCode)
	}
	if s.Model != "Boeing 737 MAX 8" {
		t.Errorf("model: got %s want the live desc", s.Model)
	}
	if s.Manufacturer != "Boeing" || s.Country != "Sweden" {
		t.Errorf("enrichment fields not carried through: %+v", s)
	}
	if len(s.AirlineCodes) != 2 {
		t.Errorf("airline codes: got %v want SAS and SK", s.AirlineCodes)
	}
	if len(s.Origin) != 2 {
		t.Errorf("origin: got %v want ESSA and ARN", s.Origin)
	}
	if s.VerticalRateFpm != -1200 {
		t.Errorf("vertical rate: got %v want -1200", s.VerticalRateFpm)
	}
	if !s.HasAltitude || !s.HasSpeed || !s.HasPosition {
		t.Errorf("presence flags wrong: %+v", s)
	}
}

func TestBuildWatchSubjectMarksMissingDataAbsent(t *testing.T) {
	s := buildWatchSubject(Aircraft{Hex: "4ca7b5"}, aircraftEnrichment{}, 0, false, false)

	if s.HasAltitude || s.HasSpeed || s.HasPosition {
		t.Errorf("an empty aircraft should report no altitude, speed or position: %+v", s)
	}
	if s.Manufacturer != "" || s.Country != "" || len(s.Origin) != 0 {
		t.Errorf("an empty enrichment should leave string fields empty: %+v", s)
	}
}

func TestBuildWatchSubjectFallsBackToDatabaseValues(t *testing.T) {
	e := aircraftEnrichment{Registration: strPtr("SE-DBB"), IcaoType: strPtr("B737"), AircraftType: strPtr("Boeing 737-800")}

	s := buildWatchSubject(Aircraft{Hex: "4ca7b5"}, e, 0, false, false)

	if s.Registration != "SE-DBB" {
		t.Errorf("registration: got %s want SE-DBB", s.Registration)
	}
	if s.TypeCode != "B737" {
		t.Errorf("type code: got %s want B737", s.TypeCode)
	}
	if s.Model != "Boeing 737-800" {
		t.Errorf("model: got %s want Boeing 737-800", s.Model)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./core/ -run 'TestDiffMatches|TestBuildWatchSubject' -v`
Expected: FAIL — `undefined: watchKey`, `undefined: diffMatches`, `undefined: buildWatchSubject`.

- [ ] **Step 3: Write the implementation**

Create `core/watches-engine.go`:

```go
package main

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// watchMatchGrace is how long a match survives without being re-confirmed
// before it is considered over. It mirrors the 10-minute window
// getAircraftsRecentlySeen uses to decide that a new aircraft_data row is a new
// sighting, so "new sighting" and "new notification" agree, and it absorbs the
// occasional tick where readsb drops an aircraft from the feed.
const watchMatchGrace = 10 * time.Minute

// watchKey identifies one aircraft's match against one watch.
type watchKey struct {
	WatchID int
	Hex     string
}

// activeMatchStore mirrors watch_active_matches in memory so the 2s tick can
// diff without a round trip. Postgres stays the durable record; the last-seen
// timestamps are in-memory only and reset to "now" on restart, which just gives
// every loaded match a fresh grace window.
type activeMatchStore struct {
	mu      sync.Mutex
	matches map[watchKey]time.Time
}

func newActiveMatchStore() *activeMatchStore {
	return &activeMatchStore{matches: map[watchKey]time.Time{}}
}

var activeMatchCache = newActiveMatchStore()

// forget drops every cached match for a watch, so a rewritten or deleted watch
// cannot leave stale state behind.
func (s *activeMatchStore) forget(watchID int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.matches {
		if key.WatchID == watchID {
			delete(s.matches, key)
		}
	}
}

// load replaces the cache from Postgres at startup.
func (s *activeMatchStore) load(pg *postgres, now time.Time) {

	rows, err := pg.db.Query(context.Background(), `SELECT watch_id, hex FROM watch_active_matches`)
	if err != nil {
		log.Error().Err(err).Msg("activeMatchStore.load() - query failed")
		return
	}
	defer rows.Close()

	loaded := map[watchKey]time.Time{}
	for rows.Next() {
		var key watchKey
		if err := rows.Scan(&key.WatchID, &key.Hex); err != nil {
			log.Error().Err(err).Msg("activeMatchStore.load() - error scanning rows")
			continue
		}
		loaded[key] = now
	}
	if err := rows.Err(); err != nil {
		log.Error().Err(err).Msg("activeMatchStore.load() - row iteration failed")
	}

	s.mu.Lock()
	s.matches = loaded
	s.mu.Unlock()

	log.Debug().Msgf("Loaded %d active watch matches", len(loaded))
}

// snapshot returns a copy of the current match state.
func (s *activeMatchStore) snapshot() map[watchKey]time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[watchKey]time.Time, len(s.matches))
	for k, v := range s.matches {
		out[k] = v
	}
	return out
}

// apply folds one tick's outcome into the cache: everything still matching gets
// its timestamp refreshed, everything ended is dropped.
func (s *activeMatchStore) apply(current map[watchKey]bool, ended []watchKey, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range current {
		s.matches[key] = now
	}
	for _, key := range ended {
		delete(s.matches, key)
	}
}

// diffMatches compares this tick's match set against the previous state.
// started is everything newly matching (one notification each); ended is
// everything that has not been re-confirmed within grace.
func diffMatches(current map[watchKey]bool, previous map[watchKey]time.Time, now time.Time, grace time.Duration) (started, ended []watchKey) {

	for key := range current {
		if _, active := previous[key]; !active {
			started = append(started, key)
		}
	}

	for key, lastMatched := range previous {
		if current[key] {
			continue
		}
		if now.Sub(lastMatched) > grace {
			ended = append(ended, key)
		}
	}

	return started, ended
}

// buildWatchSubject flattens one aircraft into the value set conditions are
// evaluated against. Live readsb values win over database enrichment, which is
// only a fallback for what the feed does not carry.
func buildWatchSubject(a Aircraft, e aircraftEnrichment, distanceKm float64, hasPosition, firstSeenEver bool) watchSubject {

	s := watchSubject{
		Hex:             a.Hex,
		Callsign:        a.Flight,
		Registration:    firstNonEmpty(a.R, stringValue(e.Registration)),
		TypeCode:        firstNonEmpty(a.T, stringValue(e.IcaoType)),
		Model:           firstNonEmpty(a.Desc, stringValue(e.AircraftType)),
		Manufacturer:    stringValue(e.Manufacturer),
		Country:         stringValue(e.CountryName),
		Airline:         stringValue(e.AirlineName),
		Squawk:          a.Squawk,
		DistanceKm:      distanceKm,
		HasPosition:     hasPosition,
		AltitudeFt:      float64(a.AltBaro),
		HasAltitude:     a.AltBaro != 0,
		SpeedKt:         a.Gs,
		HasSpeed:        a.Gs != 0,
		VerticalRateFpm: float64(a.BaroRate),
		FirstSeenEver:   firstSeenEver,
	}

	s.AirlineCodes = nonEmptyValues(stringValue(e.AirlineIcao), stringValue(e.AirlineIata))
	s.Origin = nonEmptyValues(stringValue(e.OriginIcao), stringValue(e.OriginIata))
	s.Destination = nonEmptyValues(stringValue(e.DestinationIcao), stringValue(e.DestinationIata))

	return s
}

func nonEmptyValues(values ...string) []string {
	var out []string
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// initWatchEngine primes the in-memory match state from Postgres so a restart
// does not re-notify for aircraft that were already matching.
func initWatchEngine(pg *postgres) {
	activeMatchCache.load(pg, time.Now())
}

// firstSeen is the process-wide first-sighting tracker, driven by the ingest
// tick.
var firstSeen = newFirstSeenTracker()

// evaluateWatches matches the current readsb snapshot against every enabled
// watch and fires one notification per match that starts. Called from the 2s
// ingest tick with the snapshot and enrichment it has already fetched.
func evaluateWatches(pg *postgres, aircraft []Aircraft, enrichment map[string]aircraftEnrichment) {

	watches := watchCache.enabled(pg)
	now := time.Now()

	hexes := make([]string, 0, len(aircraft))
	for _, a := range aircraft {
		hexes = append(hexes, a.Hex)
	}

	// known_aircraft must be maintained even with no watches configured, so
	// first_seen_ever is correct for whatever the user creates later.
	brandNew := markKnownAircraft(pg, hexes)
	firstSeenNow := firstSeen.update(hexes, brandNew, now, watchMatchGrace)

	if len(watches) == 0 {
		return
	}

	subjects := make(map[string]watchSubject, len(aircraft))
	for _, a := range aircraft {
		hasPosition := a.Lat != 0 || a.Lon != 0
		distance := 0.0
		if hasPosition {
			distance = *getDistance([]float64{a.Lon, a.Lat})
		}
		subjects[a.Hex] = buildWatchSubject(a, enrichment[a.Hex], distance, hasPosition, firstSeenNow[a.Hex])
	}

	current := map[watchKey]bool{}
	for _, w := range watches {
		for hex, subject := range subjects {
			if matchWatch(w, subject) {
				current[watchKey{WatchID: w.ID, Hex: hex}] = true
			}
		}
	}

	previous := activeMatchCache.snapshot()
	started, ended := diffMatches(current, previous, now, watchMatchGrace)
	activeMatchCache.apply(current, ended, now)

	watchByID := make(map[int]Watch, len(watches))
	for _, w := range watches {
		watchByID[w.ID] = w
	}

	for _, key := range started {
		w, ok := watchByID[key.WatchID]
		if !ok {
			continue
		}
		subject := subjects[key.Hex]

		_, err := pg.db.Exec(context.Background(), `
			INSERT INTO watch_active_matches (watch_id, hex, matched_at)
			VALUES ($1, $2, $3)
			ON CONFLICT (watch_id, hex) DO NOTHING`, key.WatchID, key.Hex, now)
		if err != nil {
			log.Error().Err(err).Msgf("evaluateWatches() - unable to record match for watch %d / %s", key.WatchID, key.Hex)
		}

		log.Info().Msgf("Watch %q matched %s", w.Name, key.Hex)

		if notifier != nil {
			go notifier.NotifyWatch(w, subject)
		}
	}

	for _, key := range ended {
		_, err := pg.db.Exec(context.Background(), `
			DELETE FROM watch_active_matches WHERE watch_id = $1 AND hex = $2`, key.WatchID, key.Hex)
		if err != nil {
			log.Error().Err(err).Msgf("evaluateWatches() - unable to clear match for watch %d / %s", key.WatchID, key.Hex)
		}
	}
}
```

- [ ] **Step 4: Wire the engine into the ingest tick**

In `core/aircraft.go`, extend the block added in Task 3 Step 4 so it reads:

```go
	pg.updateDatabase(response.Now, aircraftsInRange)

	// One enrichment round trip per tick, shared by every consumer of the
	// snapshot.
	enrichment := enrichAircraftSnapshot(pg, aircraftsInRange)
	refreshCurrentSightings(response.Now, aircraftsInRange, enrichment)
	evaluateWatches(pg, aircraftsInRange, enrichment)
```

- [ ] **Step 5: Prime the engine at startup**

In `core/core.go`, immediately after the `notifier = NewNotificationService(pg)` line (currently line 98), add:

```go
	// Restore the watch match state so a restart does not re-notify for
	// aircraft that were already matching.
	initWatchEngine(pg)
```

- [ ] **Step 6: Run the tests to verify they pass**

`go build` still fails at this point because `notifier.NotifyWatch` arrives in Task 7. Run only the compile-independent check after Task 7. For now:

Run: `go vet ./core/ 2>&1 | head`
Expected: the only reported problem is `notifier.NotifyWatch undefined`. Any other error is a real bug — fix it before moving on.

- [ ] **Step 7: Commit**

```bash
git add core/watches-engine.go core/watches-engine_test.go core/aircraft.go core/core.go
git commit -m "feat: watch evaluation engine on the 2s ingest tick"
```

---

### Task 7: Apprise message and hit-history write

**Files:**
- Modify: `core/notifications.go` (append to the end of the file)
- Test: `core/notifications_test.go` (append to the existing file)

**Interfaces:**
- Consumes: `Watch`, `watchSubject` (Task 2); `NotificationService.send`, `NotificationService.loadConfig` (existing).
- Produces (used by Task 6):
  - `func buildWatchMessage(watchName string, s watchSubject) (title, body string)`
  - `func (n *NotificationService) NotifyWatch(w Watch, s watchSubject)`

- [ ] **Step 1: Write the failing test**

Append to `core/notifications_test.go`:

```go
func TestBuildWatchMessageUsesRegistrationInTheTitle(t *testing.T) {
	s := watchSubject{
		Hex: "4ca7b5", Callsign: "SAS1234", Registration: "SE-RTM", TypeCode: "B38M",
		Model: "Boeing 737 MAX 8", Airline: "Scandinavian Airlines",
		Origin: []string{"ESSA", "ARN"}, Destination: []string{"EKCH", "CPH"},
		AltitudeFt: 31000, HasAltitude: true, SpeedKt: 450, HasSpeed: true,
		DistanceKm: 42.5, HasPosition: true, Squawk: "2000",
	}

	title, body := buildWatchMessage("Boeing close by", s)

	if !strings.Contains(title, "Boeing close by") {
		t.Errorf("title should name the watch, got %q", title)
	}
	if !strings.Contains(title, "SE-RTM") {
		t.Errorf("title should identify the aircraft, got %q", title)
	}
	for _, want := range []string{"SAS1234", "B38M", "Boeing 737 MAX 8", "Scandinavian Airlines", "ARN", "CPH", "31000", "450", "42", "2000"} {
		if !strings.Contains(body, want) {
			t.Errorf("body is missing %q:\n%s", want, body)
		}
	}
}

func TestBuildWatchMessageFallsBackToHexAndOmitsMissingData(t *testing.T) {
	title, body := buildWatchMessage("Anything", watchSubject{Hex: "4ca7b5"})

	if !strings.Contains(title, "4ca7b5") {
		t.Errorf("title should fall back to the hex, got %q", title)
	}
	for _, unwanted := range []string{"Altitude", "Speed", "Distance", "Route", "Squawk"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("body should omit %q when there is no data:\n%s", unwanted, body)
		}
	}
}

func TestBuildWatchMessageMarksFirstEverSighting(t *testing.T) {
	_, body := buildWatchMessage("New aircraft", watchSubject{Hex: "4ca7b5", FirstSeenEver: true})

	if !strings.Contains(body, "First time") {
		t.Errorf("body should flag a first-ever sighting:\n%s", body)
	}
}
```

`core/notifications_test.go` already imports `strings`, `testing` and `time`, so its import block needs no change.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./core/ -run TestBuildWatchMessage -v`
Expected: FAIL — `undefined: buildWatchMessage`.

- [ ] **Step 3: Write the implementation**

Append to `core/notifications.go`:

```go
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
// hit. The history row is written whether or not sending is enabled or
// succeeds, so the Watches tab shows hits even without Apprise configured.
func (n *NotificationService) NotifyWatch(w Watch, s watchSubject) {

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

	cfg := n.loadConfig()
	success, sendError := false, ""

	switch {
	case !cfg.Enabled:
		sendError = "notifications are disabled"
	case cfg.APIURL == "":
		sendError = "apprise api url is not set"
	default:
		key := strings.TrimSpace(w.AppriseKey)
		if key == "" {
			key = cfg.ConfigKey
		}
		if key == "" {
			sendError = "apprise config key is not set"
			break
		}
		if _, err := n.send(cfg.APIURL, key, apprisePayload{Title: title, Body: body}); err != nil {
			sendError = err.Error()
			log.Error().Err(err).Msgf("Watch notification failed for watch %d / %s", w.ID, s.Hex)
		} else {
			success = true
		}
	}

	_, err = n.pg.db.Exec(context.Background(), `
		INSERT INTO watch_notifications
			(watch_id, watch_name, hex, flight, registration, snapshot, apprise_success, apprise_error)
		VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6, $7, NULLIF($8, ''))`,
		w.ID, w.Name, s.Hex, strings.TrimSpace(s.Callsign), s.Registration, snapshot, success, sendError)
	if err != nil {
		log.Error().Err(err).Msg("NotifyWatch() - failed to write watch_notifications")
	}
}
```

- [ ] **Step 4: Build and run the full test suite**

Run: `go build ./... && go test ./...`
Expected: build succeeds (the Task 5 and Task 6 forward references now resolve); all tests pass.

- [ ] **Step 5: Commit**

```bash
git add core/notifications.go core/notifications_test.go
git commit -m "feat: Apprise watch notifications and hit history"
```

---

### Task 8: REST API for watches

**Files:**
- Create: `core/watches-api.go`
- Modify: `core/api.go:142-148` (register the route group)

**Interfaces:**
- Consumes: `listWatches`, `createWatch`, `updateWatch`, `deleteWatch`, `getWatch` (Task 5); `validateWatch`, `watchFieldList` (Task 2).
- Produces (used by Tasks 9-11):
  - `GET /api/watches` → `[]Watch`
  - `POST /api/watches` → `Watch` (201)
  - `PUT /api/watches/:id` → `Watch`
  - `DELETE /api/watches/:id` → 204
  - `GET /api/watches/fields` → `{"fields": [...], "operators": {...}}`
  - `GET /api/watches/hits?watch_id=&limit=&offset=` → `{"hits": [...]}`

- [ ] **Step 1: Write the implementation**

Create `core/watches-api.go`:

```go
package main

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

// watchOperatorLabels gives the frontend a readable name per operator without
// duplicating the vocabulary on the client.
var watchOperatorLabels = map[string]string{
	"equals":      "is",
	"contains":    "contains",
	"starts_with": "starts with",
	"over":        "is over",
	"under":       "is under",
	"in_list":     "is any of",
	"is_true":     "is true",
}

// WatchHit is one row of the hit history.
type WatchHit struct {
	ID             int            `json:"id"`
	WatchID        *int           `json:"watch_id"`
	WatchName      string         `json:"watch_name"`
	Hex            string         `json:"hex"`
	Flight         *string        `json:"flight"`
	Registration   *string        `json:"registration"`
	Snapshot       map[string]any `json:"snapshot"`
	NotifiedAt     time.Time      `json:"notified_at"`
	AppriseSuccess bool           `json:"apprise_success"`
	AppriseError   *string        `json:"apprise_error"`
}

// watchPayload is the request body for create and update. Conditions replace
// the watch's full condition list.
type watchPayload struct {
	Name       string           `json:"name"`
	Enabled    *bool            `json:"enabled"`
	Combinator string           `json:"combinator"`
	AppriseKey string           `json:"apprise_key"`
	Conditions []WatchCondition `json:"conditions"`
}

func (p watchPayload) toWatch() Watch {
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	combinator := p.Combinator
	if combinator == "" {
		combinator = "AND"
	}
	conditions := p.Conditions
	if conditions == nil {
		conditions = []WatchCondition{}
	}
	return Watch{
		Name:       p.Name,
		Enabled:    enabled,
		Combinator: combinator,
		AppriseKey: p.AppriseKey,
		Conditions: conditions,
	}
}

func (s *APIServer) getWatchFields(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"fields":    watchFieldList(),
		"operators": watchOperatorLabels,
	})
}

func (s *APIServer) getWatches(c *gin.Context) {
	watches, err := listWatches(s.pg)
	if err != nil {
		log.Error().Err(err).Msg("getWatches() - query failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to load watches"})
		return
	}
	c.JSON(http.StatusOK, watches)
}

func (s *APIServer) createWatchHandler(c *gin.Context) {

	var payload watchPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	watch := payload.toWatch()
	if err := validateWatch(watch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	created, err := createWatch(s.pg, watch)
	if err != nil {
		log.Error().Err(err).Msg("createWatchHandler() - insert failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to create watch"})
		return
	}
	c.JSON(http.StatusCreated, created)
}

func (s *APIServer) updateWatchHandler(c *gin.Context) {

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid watch id"})
		return
	}

	var payload watchPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	watch := payload.toWatch()
	if err := validateWatch(watch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updated, err := updateWatch(s.pg, id, watch)
	if err != nil {
		log.Error().Err(err).Msg("updateWatchHandler() - update failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to update watch"})
		return
	}
	if updated == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "watch not found"})
		return
	}
	c.JSON(http.StatusOK, updated)
}

func (s *APIServer) deleteWatchHandler(c *gin.Context) {

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid watch id"})
		return
	}

	if err := deleteWatch(s.pg, id); err != nil {
		if err == pgx.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "watch not found"})
			return
		}
		log.Error().Err(err).Msg("deleteWatchHandler() - delete failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to delete watch"})
		return
	}

	c.Status(http.StatusNoContent)
}

func (s *APIServer) getWatchHits(c *gin.Context) {

	limit := 50
	if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 && v <= 500 {
		limit = v
	}
	offset := 0
	if v, err := strconv.Atoi(c.Query("offset")); err == nil && v > 0 {
		offset = v
	}

	watchID := 0
	if v, err := strconv.Atoi(c.Query("watch_id")); err == nil && v > 0 {
		watchID = v
	}

	rows, err := s.pg.db.Query(context.Background(), `
		SELECT id, watch_id, watch_name, hex, flight, registration, snapshot,
		       notified_at, apprise_success, apprise_error
		FROM watch_notifications
		WHERE ($1 = 0 OR watch_id = $1)
		ORDER BY notified_at DESC, id DESC
		LIMIT $2 OFFSET $3`, watchID, limit, offset)
	if err != nil {
		log.Error().Err(err).Msg("getWatchHits() - query failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to load watch hits"})
		return
	}
	defer rows.Close()

	hits := []WatchHit{}
	for rows.Next() {
		var h WatchHit
		if err := rows.Scan(&h.ID, &h.WatchID, &h.WatchName, &h.Hex, &h.Flight,
			&h.Registration, &h.Snapshot, &h.NotifiedAt, &h.AppriseSuccess, &h.AppriseError); err != nil {
			log.Error().Err(err).Msg("getWatchHits() - error scanning rows")
			continue
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		log.Error().Err(err).Msg("getWatchHits() - row iteration failed")
	}

	c.JSON(http.StatusOK, gin.H{"hits": hits})
}
```

- [ ] **Step 2: Register the routes**

In `core/api.go`, after the `notifications := api.Group("/notifications") { ... }` block (currently ending at line 145) and before `api.GET("/version", s.getVersion)`, insert:

```go
		watches := api.Group("/watches")
		{
			// Static GET children only, and :id only on PUT/DELETE, so there is
			// no route conflict on the /watches/... segment.
			watches.GET("", s.getWatches)
			watches.GET("/fields", s.getWatchFields)
			watches.GET("/hits", s.getWatchHits)
			watches.POST("", s.createWatchHandler)
			watches.PUT("/:id", s.updateWatchHandler)
			watches.DELETE("/:id", s.deleteWatchHandler)
		}
```

- [ ] **Step 3: Build and test**

Run: `go build ./... && go test ./...`
Expected: build succeeds, all tests pass.

- [ ] **Step 4: Verify Gin does not panic on the route tree**

Gin panics at registration time on conflicting wildcard/static segments, so a clean start proves the routing is valid. This is checked at runtime in Task 12 — for now confirm the group registers exactly the six routes above and no `GET /watches/:id`.

Run: `grep -n 'watches\.' core/api.go`
Expected: six lines, no `watches.GET("/:id"...)`.

- [ ] **Step 5: Commit**

```bash
git add core/watches-api.go core/api.go
git commit -m "feat: REST API for watch CRUD, field metadata and hit history"
```

---

### Task 9: Frontend store for watches

**Files:**
- Create: `web/src/stores/watches.js`

**Interfaces:**
- Consumes: the endpoints from Task 8.
- Produces (used by Tasks 10, 11):
  - `export const watches` — writable store holding `{ items: [], loading: bool, error: string|null }`
  - `export const watchFields` — writable store holding `{ fields: [], operators: {} }`
  - `export async function loadWatches()`
  - `export async function loadWatchFields()`
  - `export async function saveWatch(watch)` — POST when `watch.id` is falsy, PUT otherwise; returns `{ ok, error }`
  - `export async function removeWatch(id)` — returns `{ ok, error }`
  - `export async function toggleWatch(watch)` — returns `{ ok, error }`
  - `export async function loadWatchHits(watchId)` — returns an array of hits

- [ ] **Step 1: Write the store**

Create `web/src/stores/watches.js`:

```javascript
import { writable } from 'svelte/store';

export const watches = writable({ items: [], loading: true, error: null });
export const watchFields = writable({ fields: [], operators: {} });

export async function loadWatches() {
    watches.update((s) => ({ ...s, loading: true, error: null }));
    try {
        const response = await fetch('/api/watches');
        if (!response.ok) throw new Error('Failed to load watches');
        const items = await response.json();
        watches.set({ items: items ?? [], loading: false, error: null });
    } catch (error) {
        console.error('Failed to load watches:', error);
        watches.set({ items: [], loading: false, error: 'Could not load watches' });
    }
}

export async function loadWatchFields() {
    try {
        const response = await fetch('/api/watches/fields');
        if (!response.ok) throw new Error('Failed to load watch fields');
        const data = await response.json();
        watchFields.set({ fields: data.fields ?? [], operators: data.operators ?? {} });
    } catch (error) {
        console.error('Failed to load watch fields:', error);
    }
}

// errorFrom pulls the server's message out of a failed response so validation
// errors surface in the form instead of a generic failure.
async function errorFrom(response, fallback) {
    const data = await response.json().catch(() => ({}));
    return data.error || fallback;
}

export async function saveWatch(watch) {
    const isUpdate = Boolean(watch.id);
    const body = {
        name: watch.name,
        enabled: watch.enabled,
        combinator: watch.combinator,
        apprise_key: watch.apprise_key ?? '',
        conditions: (watch.conditions ?? []).map((c) => ({
            field: c.field,
            operator: c.operator,
            value: c.value ?? ''
        }))
    };

    try {
        const response = await fetch(isUpdate ? `/api/watches/${watch.id}` : '/api/watches', {
            method: isUpdate ? 'PUT' : 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body)
        });
        if (!response.ok) {
            return { ok: false, error: await errorFrom(response, 'Could not save the watch') };
        }
        await loadWatches();
        return { ok: true, error: null };
    } catch (error) {
        console.error('Failed to save watch:', error);
        return { ok: false, error: 'Could not save the watch' };
    }
}

export async function removeWatch(id) {
    try {
        const response = await fetch(`/api/watches/${id}`, { method: 'DELETE' });
        if (!response.ok) {
            return { ok: false, error: await errorFrom(response, 'Could not delete the watch') };
        }
        await loadWatches();
        return { ok: true, error: null };
    } catch (error) {
        console.error('Failed to delete watch:', error);
        return { ok: false, error: 'Could not delete the watch' };
    }
}

export async function toggleWatch(watch) {
    return saveWatch({ ...watch, enabled: !watch.enabled });
}

export async function loadWatchHits(watchId) {
    const query = watchId ? `?watch_id=${watchId}&limit=50` : '?limit=50';
    try {
        const response = await fetch(`/api/watches/hits${query}`);
        if (!response.ok) throw new Error('Failed to load watch hits');
        const data = await response.json();
        return data.hits ?? [];
    } catch (error) {
        console.error('Failed to load watch hits:', error);
        return [];
    }
}
```

- [ ] **Step 2: Verify the build**

Run: `cd web && npm run build`
Expected: build succeeds (the store is unreferenced so far, but a syntax error would still fail the parse once imported — Task 10 imports it).

- [ ] **Step 3: Commit**

```bash
git add web/src/stores/watches.js
git commit -m "feat: frontend store for watch CRUD and hit history"
```

---

### Task 10: Watch editor modal

**Files:**
- Create: `web/src/components/WatchEditorModal.svelte`

**Interfaces:**
- Consumes: `watchFields`, `loadWatchFields`, `saveWatch` (Task 9).
- Produces (used by Task 11): a component with props `export let watch = null;` and `export let onClose = () => {};`, rendering a DaisyUI `<dialog>` that is open whenever `watch` is non-null. `watch = {}` means "create new".

- [ ] **Step 1: Write the component**

Create `web/src/components/WatchEditorModal.svelte`:

```svelte
<script>
    import { onMount } from 'svelte';
    import { IconTrash, IconPlus, IconAlertTriangle } from '@tabler/icons-svelte';
    import { watchFields, loadWatchFields, saveWatch } from '../stores/watches';

    export let watch = null;
    export let onClose = () => {};

    let name = '';
    let enabled = true;
    let combinator = 'AND';
    let appriseKey = '';
    let conditions = [];
    let error = null;
    let isSaving = false;
    let loadedId = undefined;

    onMount(loadWatchFields);

    // Reset the form whenever a different watch is opened. Tracking the id (and
    // undefined for "no watch open") keeps typing from being clobbered by
    // reactive re-runs while the modal stays open.
    $: if (watch && loadedId !== (watch.id ?? 'new')) {
        loadedId = watch.id ?? 'new';
        name = watch.name ?? '';
        enabled = watch.enabled ?? true;
        combinator = watch.combinator ?? 'AND';
        appriseKey = watch.apprise_key ?? '';
        conditions = (watch.conditions ?? []).map((c) => ({ ...c }));
        if (conditions.length === 0) addCondition();
        error = null;
    } else if (!watch) {
        loadedId = undefined;
    }

    $: fields = $watchFields.fields;
    $: operatorLabels = $watchFields.operators;

    function fieldFor(key) {
        return fields.find((f) => f.key === key);
    }

    function addCondition() {
        const first = fields[0];
        conditions = [
            ...conditions,
            { field: first?.key ?? 'callsign', operator: first?.operators?.[0] ?? 'contains', value: '' }
        ];
    }

    function removeCondition(index) {
        conditions = conditions.filter((_, i) => i !== index);
    }

    // Changing the field may invalidate the selected operator, so snap it back
    // to the first one the new field allows.
    function onFieldChange(index) {
        const field = fieldFor(conditions[index].field);
        if (field && !field.operators.includes(conditions[index].operator)) {
            conditions[index].operator = field.operators[0];
        }
        if (field?.kind === 'flag') {
            conditions[index].value = '';
        }
        conditions = conditions;
    }

    function useEmergencySquawks(index) {
        conditions[index].field = 'squawk';
        conditions[index].operator = 'in_list';
        conditions[index].value = '7500,7600,7700';
        conditions = conditions;
    }

    async function save() {
        isSaving = true;
        error = null;
        const result = await saveWatch({
            id: watch.id,
            name,
            enabled,
            combinator,
            apprise_key: appriseKey,
            conditions
        });
        isSaving = false;
        if (result.ok) {
            onClose();
        } else {
            error = result.error;
        }
    }
</script>

{#if watch}
    <dialog class="modal modal-open">
        <div class="modal-box max-w-3xl">
            <h3 class="text-lg font-bold mb-4">{watch.id ? 'Edit watch' : 'New watch'}</h3>

            <label class="form-control w-full mb-4">
                <div class="label"><span class="label-text">Name</span></div>
                <input
                    type="text"
                    class="input input-bordered w-full"
                    placeholder="e.g. Boeing 747 within 50 km"
                    maxlength="200"
                    bind:value={name}
                />
            </label>

            <div class="flex flex-wrap items-center gap-4 mb-4">
                <label class="label cursor-pointer gap-2">
                    <input type="checkbox" class="toggle toggle-primary" bind:checked={enabled} />
                    <span class="label-text">Enabled</span>
                </label>

                <label class="form-control">
                    <div class="label"><span class="label-text">Match</span></div>
                    <select class="select select-bordered" bind:value={combinator}>
                        <option value="AND">all conditions</option>
                        <option value="OR">any condition</option>
                    </select>
                </label>
            </div>

            <div class="divider">Conditions</div>

            {#each conditions as condition, index (index)}
                <div class="flex flex-wrap items-end gap-2 mb-3">
                    <select
                        class="select select-bordered select-sm grow"
                        bind:value={condition.field}
                        on:change={() => onFieldChange(index)}
                    >
                        {#each fields as field (field.key)}
                            <option value={field.key}>{field.label}</option>
                        {/each}
                    </select>

                    <select class="select select-bordered select-sm" bind:value={condition.operator}>
                        {#each fieldFor(condition.field)?.operators ?? [] as operator}
                            <option value={operator}>{operatorLabels[operator] ?? operator}</option>
                        {/each}
                    </select>

                    {#if fieldFor(condition.field)?.kind !== 'flag'}
                        <input
                            type="text"
                            class="input input-bordered input-sm grow"
                            placeholder={fieldFor(condition.field)?.kind === 'number'
                                ? fieldFor(condition.field)?.unit ?? 'value'
                                : 'value'}
                            bind:value={condition.value}
                        />
                    {/if}

                    {#if condition.field === 'squawk'}
                        <button class="btn btn-sm btn-outline" on:click={() => useEmergencySquawks(index)}>
                            Emergency
                        </button>
                    {/if}

                    <button
                        class="btn btn-sm btn-ghost btn-square"
                        aria-label="Remove condition"
                        on:click={() => removeCondition(index)}
                    >
                        <IconTrash class="h-4 w-4" />
                    </button>
                </div>

                {#if fieldFor(condition.field)?.hint}
                    <p class="text-xs opacity-60 mb-3 -mt-2">{fieldFor(condition.field).hint}</p>
                {/if}
            {/each}

            <button class="btn btn-sm btn-outline mt-2" on:click={addCondition}>
                <IconPlus class="h-4 w-4" /> Add condition
            </button>

            <label class="form-control w-full mt-6">
                <div class="label">
                    <span class="label-text">Apprise key (optional)</span>
                </div>
                <input
                    type="text"
                    class="input input-bordered w-full"
                    placeholder="Leave blank to use the key from Settings"
                    bind:value={appriseKey}
                />
            </label>

            {#if error}
                <div class="alert alert-error mt-4">
                    <IconAlertTriangle class="h-5 w-5" />
                    <span>{error}</span>
                </div>
            {/if}

            <div class="modal-action">
                <button class="btn" on:click={onClose} disabled={isSaving}>Cancel</button>
                <button class="btn btn-primary" on:click={save} disabled={isSaving}>
                    {isSaving ? 'Saving…' : 'Save'}
                </button>
            </div>
        </div>
        <form method="dialog" class="modal-backdrop">
            <button on:click={onClose}>close</button>
        </form>
    </dialog>
{/if}
```

- [ ] **Step 2: Verify the build**

Run: `cd web && npm run build`
Expected: build succeeds. A Svelte compile error here means a template typo — fix it before moving on.

- [ ] **Step 3: Commit**

```bash
git add web/src/components/WatchEditorModal.svelte
git commit -m "feat: watch editor modal with dynamic condition rows"
```

---

### Task 11: Watches tab — list, hit history and navigation

**Files:**
- Create: `web/src/components/WatchList.svelte`
- Create: `web/src/components/WatchHits.svelte`
- Create: `web/src/components/TabWatches.svelte`
- Modify: `web/src/lib/dashboardCards.js`
- Modify: `web/src/App.svelte`
- Modify: `web/src/components/Settings.svelte:6-15` (`cardTabLabels` / `cardTabOrder`)

**Interfaces:**
- Consumes: `watches`, `loadWatches`, `removeWatch`, `toggleWatch`, `loadWatchHits` (Task 9); `WatchEditorModal` (Task 10); `openAircraftModal` from `web/src/stores/aircraftModal.js` (existing).
- Produces: the `watches` tab, registered in `dashboardCards` as `watch_list` and `watch_hits` so both cards are hideable like every other card.

- [ ] **Step 1: Write the watch list**

Create `web/src/components/WatchList.svelte`:

```svelte
<script>
    import { onMount } from 'svelte';
    import { IconEdit, IconTrash, IconPlus } from '@tabler/icons-svelte';
    import { watches, loadWatches, removeWatch, toggleWatch } from '../stores/watches';
    import WatchEditorModal from './WatchEditorModal.svelte';

    let editing = null;
    let actionError = null;

    onMount(loadWatches);

    function describe(watch) {
        const count = watch.conditions?.length ?? 0;
        const joiner = watch.combinator === 'OR' ? 'any' : 'all';
        return `${count} condition${count === 1 ? '' : 's'} (${joiner})`;
    }

    async function onToggle(watch) {
        actionError = null;
        const result = await toggleWatch(watch);
        if (!result.ok) actionError = result.error;
    }

    async function onDelete(watch) {
        if (!confirm(`Delete the watch "${watch.name}"? Its hit history is kept.`)) return;
        actionError = null;
        const result = await removeWatch(watch.id);
        if (!result.ok) actionError = result.error;
    }
</script>

<div class="flex justify-end mb-4">
    <button class="btn btn-sm btn-primary" on:click={() => (editing = {})}>
        <IconPlus class="h-4 w-4" /> New watch
    </button>
</div>

{#if actionError}
    <div class="alert alert-error mb-4"><span>{actionError}</span></div>
{/if}

{#if $watches.loading}
    <div class="flex justify-center py-8"><span class="loading loading-spinner loading-lg"></span></div>
{:else if $watches.error}
    <div class="alert alert-error"><span>{$watches.error}</span></div>
{:else if $watches.items.length === 0}
    <p class="text-center opacity-60 py-8">
        No watches yet. Create one to get an Apprise notification when a matching aircraft shows up.
    </p>
{:else}
    <div class="overflow-x-auto">
        <table class="table table-zebra">
            <thead>
                <tr>
                    <th>Name</th>
                    <th>Conditions</th>
                    <th>Enabled</th>
                    <th class="text-right">Actions</th>
                </tr>
            </thead>
            <tbody>
                {#each $watches.items as watch (watch.id)}
                    <tr>
                        <td class="font-medium">{watch.name}</td>
                        <td class="opacity-70">{describe(watch)}</td>
                        <td>
                            <input
                                type="checkbox"
                                class="toggle toggle-sm toggle-primary"
                                checked={watch.enabled}
                                on:change={() => onToggle(watch)}
                            />
                        </td>
                        <td class="text-right whitespace-nowrap">
                            <button
                                class="btn btn-ghost btn-sm btn-square"
                                aria-label="Edit watch"
                                on:click={() => (editing = watch)}
                            >
                                <IconEdit class="h-4 w-4" />
                            </button>
                            <button
                                class="btn btn-ghost btn-sm btn-square"
                                aria-label="Delete watch"
                                on:click={() => onDelete(watch)}
                            >
                                <IconTrash class="h-4 w-4" />
                            </button>
                        </td>
                    </tr>
                {/each}
            </tbody>
        </table>
    </div>
{/if}

<WatchEditorModal watch={editing} onClose={() => (editing = null)} />
```

- [ ] **Step 2: Write the hit history**

Create `web/src/components/WatchHits.svelte`:

```svelte
<script>
    import { onMount } from 'svelte';
    import { IconAlertTriangle } from '@tabler/icons-svelte';
    import { loadWatchHits } from '../stores/watches';
    import { openAircraftModal } from '../stores/aircraftModal';

    let hits = [];
    let loading = true;

    onMount(async () => {
        hits = await loadWatchHits();
        loading = false;
    });

    function formatTime(value) {
        return new Date(value).toLocaleString();
    }

    function identity(hit) {
        return hit.registration || hit.flight || hit.hex;
    }

    function detail(hit) {
        const s = hit.snapshot ?? {};
        const parts = [];
        if (s.type_code) parts.push(s.type_code);
        if (s.origin?.length && s.destination?.length) {
            parts.push(`${s.origin[s.origin.length - 1]} → ${s.destination[s.destination.length - 1]}`);
        }
        if (s.altitude_ft) parts.push(`${Math.round(s.altitude_ft)} ft`);
        if (s.distance_km) parts.push(`${Math.round(s.distance_km)} km`);
        return parts.join(' · ');
    }
</script>

{#if loading}
    <div class="flex justify-center py-8"><span class="loading loading-spinner loading-lg"></span></div>
{:else if hits.length === 0}
    <p class="text-center opacity-60 py-8">No watch hits recorded yet.</p>
{:else}
    <div class="overflow-x-auto">
        <table class="table table-zebra">
            <thead>
                <tr>
                    <th>Time</th>
                    <th>Watch</th>
                    <th>Aircraft</th>
                    <th>Details</th>
                    <th>Notified</th>
                </tr>
            </thead>
            <tbody>
                {#each hits as hit (hit.id)}
                    <tr class="cursor-pointer hover" on:click={() => openAircraftModal(hit.hex)}>
                        <td class="whitespace-nowrap">{formatTime(hit.notified_at)}</td>
                        <td>{hit.watch_name}</td>
                        <td class="font-medium">{identity(hit)}</td>
                        <td class="opacity-70">{detail(hit)}</td>
                        <td>
                            {#if hit.apprise_success}
                                <span class="badge badge-success badge-sm">sent</span>
                            {:else}
                                <span class="badge badge-ghost badge-sm gap-1" title={hit.apprise_error ?? ''}>
                                    <IconAlertTriangle class="h-3 w-3" /> not sent
                                </span>
                            {/if}
                        </td>
                    </tr>
                {/each}
            </tbody>
        </table>
    </div>
{/if}
```

- [ ] **Step 3: Write the tab shell**

Create `web/src/components/TabWatches.svelte`:

```svelte
<script>
    import HideableCard from './HideableCard.svelte';
    import { dashboardCards } from '../lib/dashboardCards';

    const cards = dashboardCards.filter((c) => c.tab === 'watches');
</script>

<div class="grid grid-cols-1 mt-6 gap-6">
    {#each cards as card (card.id)}
        <HideableCard id={card.id} title={card.title}>
            <svelte:component this={card.component} {...(card.props || {})} />
        </HideableCard>
    {/each}
</div>
```

- [ ] **Step 4: Register the cards**

In `web/src/lib/dashboardCards.js`, add these imports next to the other component imports:

```javascript
import WatchList from '../components/WatchList.svelte';
import WatchHits from '../components/WatchHits.svelte';
```

and add these two entries at the end of the `dashboardCards` array (after `motion_longest_route`, remembering to add a comma to that line):

```javascript
    { id: 'watch_list', title: 'Watches', tab: 'watches', component: WatchList },
    { id: 'watch_hits', title: 'Watch Hits', tab: 'watches', component: WatchHits }
```

- [ ] **Step 5: Add the tab to the navigation**

In `web/src/App.svelte`, add the import next to the other tab imports:

```javascript
  import TabWatches from './components/TabWatches.svelte';
```

and add the tab as the last entry of the `tabs` array (adding a comma to the `motion-stat` line):

```javascript
    { name: 'watches', label: 'Watches', component: TabWatches }
```

- [ ] **Step 6: Add the tab to the card-visibility settings**

In `web/src/components/Settings.svelte`, add `watches: 'Watches'` to `cardTabLabels` and `'watches'` to the end of `cardTabOrder`, so the two new cards can be hidden like every other card:

```javascript
    const cardTabLabels = {
        global: 'Always Visible',
        'current-stat': 'Current Sightings',
        activity: 'Activity',
        'route-stat': 'Route Information',
        'interesting-stat': 'Interesting Aircraft',
        'motion-stat': 'Record Holders',
        watches: 'Watches'
    };
    const cardTabOrder = ['global', 'current-stat', 'activity', 'route-stat', 'interesting-stat', 'motion-stat', 'watches'];
```

- [ ] **Step 7: Verify the build**

Run: `cd web && npm run build`
Expected: build succeeds with no Svelte warnings about unknown components or unused imports.

- [ ] **Step 8: Commit**

```bash
git add web/src/components/WatchList.svelte web/src/components/WatchHits.svelte web/src/components/TabWatches.svelte web/src/lib/dashboardCards.js web/src/App.svelte web/src/components/Settings.svelte
git commit -m "feat: Watches tab with watch list and hit history"
```

---

### Task 12: Documentation and end-to-end verification

**Files:**
- Modify: `README.md` (feature list / API section, if either exists — check first)
- Modify: `CLAUDE.md` (Architecture → Go daemon section)

**Interfaces:**
- Consumes: everything above.
- Produces: a verified, deployable branch.

- [ ] **Step 1: Document the feature in CLAUDE.md**

In `/mnt/c/temp/github/claude/skystats/CLAUDE.md`, in the "Go daemon (`core/`, single `main` package)" section, append to the 2s bullet:

```markdown
- **2s** — fetch `READSB_AIRCRAFT_JSON` and upsert aircraft positions (`aircraft.go`, `readsb.go`). Only aircraft within `RADIUS` km of `LAT`/`LON` are recorded (distance via cheap-ruler). This hot path uses an in-memory recently-seen cache (`recentAircraftCache` on the `postgres` struct, 10-minute sliding expiry) so unchanged aircraft skip the DB lookup. The same tick fetches Postgres enrichment once (`enrichAircraftSnapshot`) and feeds it to both Current Sightings and the watch engine (`watches-engine.go`), which matches every aircraft against the user's watches and fires one Apprise notification per sighting that starts matching.
```

And add a short paragraph after the API section:

```markdown
### Watches

User-defined rules (`watches` + `watch_conditions`) are matched against the live snapshot every 2s. `watches-match.go` holds the field registry, the `watchSubject` model and the pure matching predicates — it is the single source of truth for which operators each field accepts, and it is what `/api/watches/fields` serves to the frontend. `watch_active_matches` records which aircraft currently match which watch so a notification fires only when a match starts; a match that goes 10 minutes without being re-confirmed ends, and the aircraft can notify again on its next sighting. `watch_notifications` is the permanent hit history and is written whether or not Apprise sending succeeded. `known_aircraft` is a never-pruned archive of every hex ever seen, backing the "first time ever seen" criterion.
```

- [ ] **Step 2: Update README.md if it documents endpoints or features**

Run: `grep -n 'api/stats\|## Features\|Notifications' README.md | head -20`
If the README lists endpoints or features, add the six `/api/watches` routes and a one-line Watches feature entry in the same style. If it does not, skip this step and note it.

- [ ] **Step 3: Full build and test**

Run from the repo root:

```bash
go build ./... && go test ./... && (cd web && npm run build)
```

Expected: Go build clean, `ok github.com/tomcarman/skystats/core`, Vite build succeeds. Do not proceed on any failure.

- [ ] **Step 4: Run the daemon against the real database and confirm the migration and routes**

```bash
cd core && go build -o skystats-daemon && ./skystats-daemon
sleep 5
grep -iE 'migration|watch|panic|error' skystats.log | tail -30
```

Expected: migration `15` applied, no panic from Gin's route registration, no errors mentioning `watch`. If Gin panics on the route tree, the `/api/watches` group in `core/api.go` is at fault.

- [ ] **Step 5: Exercise the API end to end**

```bash
# Field metadata
curl -s localhost:8080/api/watches/fields | head -c 400; echo

# Create a watch that will certainly match something
curl -s -X POST localhost:8080/api/watches \
  -H 'Content-Type: application/json' \
  -d '{"name":"Anything above 1000 ft","combinator":"AND","conditions":[{"field":"altitude_ft","operator":"over","value":"1000"}]}'; echo

# Validation must reject a watch with no conditions
curl -s -o /dev/null -w '%{http_code}\n' -X POST localhost:8080/api/watches \
  -H 'Content-Type: application/json' -d '{"name":"Empty","combinator":"AND","conditions":[]}'

# List, then wait a tick or two and read the hits
curl -s localhost:8080/api/watches | head -c 400; echo
sleep 10
curl -s localhost:8080/api/watches/hits | head -c 600; echo
```

Expected: `fields` returns 16 entries; the create returns 201 with the watch and its condition; the empty-condition create returns `400`; `hits` contains at least one row within a few seconds if any aircraft are in range (`apprise_success` will be `false` with `"notifications are disabled"` unless Apprise is configured — that is correct).

- [ ] **Step 6: Confirm the one-notification-per-sighting rule**

```bash
sleep 60
curl -s 'localhost:8080/api/watches/hits?limit=200' | grep -o '"hex":"[^"]*"' | sort | uniq -c | sort -rn | head
```

Expected: each hex appears **once** per watch. A hex appearing repeatedly within a minute means the match state is not sticking — check `watch_active_matches` and the `diffMatches` grace handling before going further.

- [ ] **Step 7: Clean up the test watch and stop the daemon**

```bash
curl -s -X DELETE localhost:8080/api/watches/1 -o /dev/null -w '%{http_code}\n'
curl -s localhost:8080/api/watches/hits | head -c 300; echo   # history must survive the delete
kill $(cat core/skystats.pid)
```

Expected: DELETE returns `204`; the hit rows are still listed, with `watch_id: null` and `watch_name` intact — this is the "keep the history" decision working.

- [ ] **Step 8: Commit**

```bash
git add CLAUDE.md README.md
git commit -m "docs: document watches and notifications"
```

- [ ] **Step 9: Report before deploying**

Do **not** deploy to 192.168.1.251 as part of this plan. Summarise for the user: what was built, the results of Steps 3-7, and that the branch `feat/watches` is ready to merge and deploy. Deployment is gated on their sign-off, and the stale-base check (`git log HEAD..origin/main`) must be run before any tar-over-ssh deploy.

---

## Notes for the implementer

**Things that are deliberately not in this plan** (spec: "Utanför scope"): nested AND/OR groups, web push or email channels, a configurable per-watch cooldown beyond one-per-sighting, and retention on `watch_notifications`.

**Two spec assumptions that the codebase contradicted**, resolved as follows:

1. The spec suggested matching against recently-updated `aircraft_data` rows. That table never stores `squawk` or `baro_rate` (`insertNewAircrafts` omits both columns), and its `alt_baro`/`gs` columns hold the session **maximum**, not the current value. Matching therefore runs against the live readsb snapshot, which carries all of them, with Postgres used only for enrichment that the feed does not provide.

2. The spec asked whether `flight_history` could back "first time ever seen". It cannot: `runHistoryRetention` prunes it on a `history_retention_days` setting (default 730). Hence the separate never-pruned `known_aircraft` table, backfilled from `aircraft_data` in the migration.
