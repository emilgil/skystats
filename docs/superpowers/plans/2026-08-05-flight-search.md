# Flight Search Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a "Sök" tab that lets a user filter `flight_history` (finished flights) by period/manufacturer/model/registration-country/airports/altitude/speed/airline/interesting-category/route-known-status/free-text, see a sortable paginated (50/page) results table, click a row for a full-detail modal, and export the current filter as CSV.

**Architecture:** One new Go source pair in `core/` — a pure, dependency-free filter-parsing/query-building module (unit tested, no DB) and a thin Gin handler module that wires that logic to two new endpoints (`GET /api/search/flights`, `GET /api/search/flights/export`). Both endpoints run the same three-table `LEFT JOIN` (`flight_history` → `registration_data` → `interesting_aircraft` → `route_data`) confirmed with the user, built as parameterized SQL with a column whitelist for `sort` (no raw query-param-to-SQL mapping). Frontend is four new Svelte components plus one new store, following the existing "explicit search button, no debounce" and "row click opens a store-driven dialog" house patterns — no new frontend libraries.

**Tech Stack:** Go 1.25.3, `github.com/jackc/pgx/v5` (`pgxpool.Pool`), Gin, `encoding/csv` (stdlib); Svelte (classic component API, not runes), DaisyUI, plain `fetch`.

## Global Constraints

- Data source is `flight_history` (finished flights), never the live feed — one row per flight, no per-aircraft dedupe.
- Speed/altitude metrics are `ground_speed` and `barometric_altitude` only (same as Record Holders) — no `ias`/`tas`/`geometric_altitude` filters in v1.
- Country filter means **registration country**, not departure/arrival country.
- Country input is the ISO alpha-2 code (`registration_data.registered_owner_country_iso_name`, e.g. `"GB"`), matching the spec's own `&country=GB` example — despite the confusing `_iso_name` suffix, this column holds the 2-letter code, not a full name.
- Pagination is fixed at 50 rows/page; `page_size` may be passed but is clamped to `[1, 50]`.
- CSV export ignores pagination but caps at 10,000 rows; if the cap is hit, the response carries `X-Search-Truncated: true` and the frontend must show a "refine your search" message.
- **Security:** every filter value is passed as a `$n` placeholder argument, never string-interpolated into SQL. The `sort` query param is validated against a fixed whitelist map before being used to build `ORDER BY` — it is never mapped 1:1 from the raw query string.
- v2/nice-to-have filters from the spec (`distance_flown`, `route_distance`, `distance_remaining`, flight duration, domestic/international) are explicitly out of scope for this plan.
- Manufacturer/model/country filters only match the subset of `flight_history` rows whose hex has been enriched by adsbdb into `registration_data` (historically ~33% of the fleet) — this is expected `LEFT JOIN` behavior, not a bug, and must be surfaced to the user via a small UI notice (Task 7), not "fixed."

---

### Reference: full column list used throughout this plan

`flight_history` (migration `000012`, already exists): `id, hex, flight, registration, type, first_seen, last_seen, ground_speed, indicated_air_speed, true_air_speed, barometric_altitude, geometric_altitude, distance_flown, route_distance, distance_remaining, origin_icao_code, origin_iata_code, destination_icao_code, destination_iata_code, created_at`. Unique constraint `(hex, first_seen)`. `hex` is lowercase.

`registration_data` (migration `000001`): `id, type, icao_type, manufacturer, mode_s, registration, registered_owner_country_iso_name, registered_owner_country_name, registered_owner_operator_flag_code, registered_owner, url_photo, url_photo_thumbnail`. Unique constraint on `mode_s` (lowercase hex — same casing as `flight_history.hex`, no conversion needed to join).

`interesting_aircraft` (migration `000001` + `000002`): `icao, registration, operator, type, icao_type, "group", tag1, tag2, tag3, category, link, image_link_1..4, commit_hash`. Unique constraint on `icao` (**uppercase** hex — join needs `UPPER(fh.hex)`). `"group"` holds exactly `Mil`/`Gov`/`Pol`/`Civ`.

`route_data` (existing table, used elsewhere via `LEFT JOIN route_data rt ON fh.flight = rt.route_callsign`, columns used here: `route_callsign, airline_name`).

---

### Task 1: Migration — index `flight_history.hex`

**Files:**
- Create: `migrations/000019_add_flight_history_hex_index.up.sql`
- Create: `migrations/000019_add_flight_history_hex_index.down.sql`

**Interfaces:**
- Produces: an index the search JOINs (Task 3) rely on for acceptable performance. No Go symbols.

`flight_history` currently has no index that starts with `hex` alone (only the composite `UNIQUE (hex, first_seen)`, and six single-column indexes on metric columns — see migration `000012`). The new search feature joins `registration_data.mode_s = flight_history.hex` and `interesting_aircraft.icao = UPPER(flight_history.hex)` on every request, so a dedicated index is needed. `000018` is the latest existing migration, so this is `000019`.

- [ ] **Step 1: Write the up migration**

`migrations/000019_add_flight_history_hex_index.up.sql`:
```sql
-- Flight search (docs/superpowers/specs/2026-08-05-flight-search-spec.md) joins
-- flight_history to registration_data and interesting_aircraft on hex for every
-- request. flight_history had no index starting with hex alone (only the
-- composite UNIQUE(hex, first_seen)), so add one.
CREATE INDEX idx_flight_history_hex ON flight_history (hex);
```

- [ ] **Step 2: Write the down migration**

`migrations/000019_add_flight_history_hex_index.down.sql`:
```sql
DROP INDEX IF EXISTS idx_flight_history_hex;
```

- [ ] **Step 3: Verify migration numbering and file pairing**

Run: `ls migrations/ | sort | tail -6`
Expected: the last two pairs are `000018_recompute_route_distance_haversine.{up,down}.sql` and the two new `000019_add_flight_history_hex_index.{up,down}.sql` files — confirming no other branch has already claimed `000019` (if it has, rename both new files to the next free number and update this task's filenames before continuing).

- [ ] **Step 4: Commit**

```bash
git add migrations/000019_add_flight_history_hex_index.up.sql migrations/000019_add_flight_history_hex_index.down.sql
git commit -m "feat: index flight_history.hex for flight search joins"
```

---

### Task 2: Pure filter-parsing and query-building logic

**Files:**
- Create: `core/search-flights-query.go`
- Test: `core/search-flights-query_test.go`

**Interfaces:**
- Produces (consumed by Tasks 3 and 4):
  - `type flightSearchParams struct` (fields: `From, To time.Time`; `Manufacturer, Model, Country, Origin, Destination, Airline string`; `AltitudeOp string; AltitudeValue int; HasAltitudeFilter bool`; `SpeedOp string; SpeedValue int; HasSpeedFilter bool`; `Interesting string` — already the mapped DB code, e.g. `"Mil"`; `OriginStatus, DestinationStatus string`; `Query string`; `Sort, Dir string`; `Page, PageSize int`)
  - `func parseFlightSearchParams(q url.Values, now time.Time) (flightSearchParams, error)`
  - `func buildFlightSearchWhere(p flightSearchParams) (where string, args []any)`
  - `func flightSearchOrderBy(p flightSearchParams) string`
  - `type flightSearchRow struct` (fields listed in Step 5 below)
  - `func flightSearchRowToJSON(r flightSearchRow) gin.H`
  - `func flightSearchRowToCSVRecord(r flightSearchRow) []string`
  - `var flightSearchCSVHeader []string`
  - `const flightSearchSelectColumns string`, `const flightSearchBaseQuery string` — the shared `SELECT ... FROM ... LEFT JOIN ...` fragments both handlers build on top of.
- Consumes (from existing code, reuse — do not redefine): `isValidPeriodType(p string) bool` and `periodWindow(p string) (time.Duration, bool)`, both already defined in `core/records-meta.go`.

This task has no DB access and is fully unit-testable — it is the single source of truth for which query params are valid and how they become SQL, mirroring the existing `watchField`/`watchFieldsByKey` whitelist pattern in `core/watches-match.go`.

- [ ] **Step 1: Write the failing tests for `parseFlightSearchParams`**

`core/search-flights-query_test.go`:
```go
package main

import (
	"net/url"
	"testing"
	"time"
)

func mustParseTime(t *testing.T, layout, value string) time.Time {
	t.Helper()
	tm, err := time.Parse(layout, value)
	if err != nil {
		t.Fatalf("bad fixture time %q: %v", value, err)
	}
	return tm
}

func TestParseFlightSearchParamsDefaults(t *testing.T) {
	now := mustParseTime(t, time.RFC3339, "2026-08-05T12:00:00Z")
	p, err := parseFlightSearchParams(url.Values{}, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Sort != "first_seen" {
		t.Errorf("Sort = %q, want first_seen", p.Sort)
	}
	if p.Dir != "desc" {
		t.Errorf("Dir = %q, want desc", p.Dir)
	}
	if p.Page != 1 {
		t.Errorf("Page = %d, want 1", p.Page)
	}
	if p.PageSize != 50 {
		t.Errorf("PageSize = %d, want 50", p.PageSize)
	}
	if p.OriginStatus != "any" || p.DestinationStatus != "any" {
		t.Errorf("status defaults = %q/%q, want any/any", p.OriginStatus, p.DestinationStatus)
	}
	if !p.From.IsZero() {
		t.Errorf("From = %v, want zero (all_time has no lower bound)", p.From)
	}
}

func TestParseFlightSearchParamsPeriodPresets(t *testing.T) {
	now := mustParseTime(t, time.RFC3339, "2026-08-05T12:00:00Z")
	cases := []struct {
		period   string
		wantFrom time.Time
	}{
		{"24h", now.Add(-24 * time.Hour)},
		{"7d", now.Add(-7 * 24 * time.Hour)},
		{"all_time", time.Time{}},
	}
	for _, tc := range cases {
		q := url.Values{"period": {tc.period}}
		p, err := parseFlightSearchParams(q, now)
		if err != nil {
			t.Fatalf("period %q: unexpected error: %v", tc.period, err)
		}
		if !p.From.Equal(tc.wantFrom) {
			t.Errorf("period %q: From = %v, want %v", tc.period, p.From, tc.wantFrom)
		}
	}
}

func TestParseFlightSearchParamsInvalidPeriod(t *testing.T) {
	now := time.Now()
	_, err := parseFlightSearchParams(url.Values{"period": {"bogus"}}, now)
	if err == nil {
		t.Fatal("expected error for invalid period, got nil")
	}
}

func TestParseFlightSearchParamsCustomRange(t *testing.T) {
	now := time.Now()
	q := url.Values{"from": {"2026-01-01"}, "to": {"2026-01-31"}}
	p, err := parseFlightSearchParams(q, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantFrom := mustParseTime(t, "2006-01-02", "2026-01-01")
	wantTo := mustParseTime(t, "2006-01-02", "2026-02-01") // to is end-exclusive, +1 day
	if !p.From.Equal(wantFrom) {
		t.Errorf("From = %v, want %v", p.From, wantFrom)
	}
	if !p.To.Equal(wantTo) {
		t.Errorf("To = %v, want %v", p.To, wantTo)
	}
}

func TestParseFlightSearchParamsCustomRangeErrors(t *testing.T) {
	now := time.Now()
	cases := map[string]url.Values{
		"only from, no to":     {"from": {"2026-01-01"}},
		"only to, no from":     {"to": {"2026-01-31"}},
		"bad from format":      {"from": {"01/01/2026"}, "to": {"2026-01-31"}},
		"from after to":        {"from": {"2026-02-01"}, "to": {"2026-01-01"}},
	}
	for name, q := range cases {
		if _, err := parseFlightSearchParams(q, now); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

func TestParseFlightSearchParamsAltitudeSpeedPairing(t *testing.T) {
	now := time.Now()
	cases := map[string]url.Values{
		"altitude value without op":        {"altitude_value": {"35000"}},
		"altitude op without value":        {"altitude_op": {"gte"}},
		"altitude bad op":                  {"altitude_op": {"sideways"}, "altitude_value": {"35000"}},
		"altitude non-numeric value":       {"altitude_op": {"gte"}, "altitude_value": {"high"}},
		"speed value without op":           {"speed_value": {"450"}},
		"speed op without value":           {"speed_op": {"lte"}},
	}
	for name, q := range cases {
		if _, err := parseFlightSearchParams(q, now); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}

	q := url.Values{"altitude_op": {"gte"}, "altitude_value": {"35000"}, "speed_op": {"lte"}, "speed_value": {"450"}}
	p, err := parseFlightSearchParams(q, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !p.HasAltitudeFilter || p.AltitudeOp != "gte" || p.AltitudeValue != 35000 {
		t.Errorf("altitude filter = %+v, want gte/35000", p)
	}
	if !p.HasSpeedFilter || p.SpeedOp != "lte" || p.SpeedValue != 450 {
		t.Errorf("speed filter = %+v, want lte/450", p)
	}
}

func TestParseFlightSearchParamsInteresting(t *testing.T) {
	now := time.Now()
	p, err := parseFlightSearchParams(url.Values{"interesting": {"military"}}, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Interesting != "Mil" {
		t.Errorf("Interesting = %q, want Mil", p.Interesting)
	}
	if _, err := parseFlightSearchParams(url.Values{"interesting": {"alien"}}, now); err == nil {
		t.Error("expected error for invalid interesting value")
	}
}

func TestParseFlightSearchParamsStatusValidation(t *testing.T) {
	now := time.Now()
	if _, err := parseFlightSearchParams(url.Values{"origin_status": {"maybe"}}, now); err == nil {
		t.Error("expected error for invalid origin_status")
	}
	if _, err := parseFlightSearchParams(url.Values{"destination_status": {"maybe"}}, now); err == nil {
		t.Error("expected error for invalid destination_status")
	}
	p, err := parseFlightSearchParams(url.Values{"origin_status": {"known"}, "destination_status": {"unknown"}}, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.OriginStatus != "known" || p.DestinationStatus != "unknown" {
		t.Errorf("status = %q/%q, want known/unknown", p.OriginStatus, p.DestinationStatus)
	}
}

func TestParseFlightSearchParamsSortAndDirWhitelist(t *testing.T) {
	now := time.Now()
	if _, err := parseFlightSearchParams(url.Values{"sort": {"'; DROP TABLE flight_history;--"}}, now); err == nil {
		t.Error("expected error for non-whitelisted sort column")
	}
	if _, err := parseFlightSearchParams(url.Values{"dir": {"sideways"}}, now); err == nil {
		t.Error("expected error for invalid dir")
	}
	p, err := parseFlightSearchParams(url.Values{"sort": {"ground_speed"}, "dir": {"asc"}}, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Sort != "ground_speed" || p.Dir != "asc" {
		t.Errorf("sort/dir = %q/%q, want ground_speed/asc", p.Sort, p.Dir)
	}
}

func TestParseFlightSearchParamsPageClamping(t *testing.T) {
	now := time.Now()
	cases := []struct {
		q            url.Values
		wantPage     int
		wantPageSize int
	}{
		{url.Values{}, 1, 50},
		{url.Values{"page": {"3"}}, 3, 50},
		{url.Values{"page": {"-1"}}, 1, 50},        // invalid, falls back to default
		{url.Values{"page": {"nope"}}, 1, 50},      // non-numeric, falls back to default
		{url.Values{"page_size": {"10"}}, 1, 10},
		{url.Values{"page_size": {"999"}}, 1, 50},  // over the cap, falls back to default
		{url.Values{"page_size": {"0"}}, 1, 50},    // zero, falls back to default
	}
	for _, tc := range cases {
		p, err := parseFlightSearchParams(tc.q, now)
		if err != nil {
			t.Fatalf("query %v: unexpected error: %v", tc.q, err)
		}
		if p.Page != tc.wantPage || p.PageSize != tc.wantPageSize {
			t.Errorf("query %v: page/pageSize = %d/%d, want %d/%d", tc.q, p.Page, p.PageSize, tc.wantPage, tc.wantPageSize)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd core && go test ./... -run TestParseFlightSearchParams -v`
Expected: build failure — `undefined: parseFlightSearchParams` (the function doesn't exist yet).

- [ ] **Step 3: Implement `parseFlightSearchParams` and its supporting types**

`core/search-flights-query.go`:
```go
package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// flightSearchParams is the fully validated, normalized form of every
// /api/search/flights query parameter. parseFlightSearchParams is the only
// way to construct one — every field has already passed whitelist/range
// validation by the time a handler sees it.
type flightSearchParams struct {
	From, To time.Time // zero Time means "no bound"

	Manufacturer string
	Model        string
	Country      string // ISO alpha-2, e.g. "GB"
	Origin       string // IATA code
	Destination  string // IATA code
	Airline      string

	AltitudeOp        string // "gte" or "lte"
	AltitudeValue     int
	HasAltitudeFilter bool

	SpeedOp        string
	SpeedValue     int
	HasSpeedFilter bool

	Interesting string // "" (no filter) or one of Mil/Gov/Pol/Civ

	OriginStatus      string // any/known/unknown
	DestinationStatus string // any/known/unknown

	Query string // free text against flight/registration/hex

	Sort string
	Dir  string

	Page     int
	PageSize int
}

// searchSortColumns whitelists which query-param sort keys are accepted and
// what SQL column each maps to. sort is never taken from the request and used
// in SQL directly — only a value that is a key in this map may pass through
// parseFlightSearchParams.
var searchSortColumns = map[string]string{
	"first_seen":          "fh.first_seen",
	"last_seen":           "fh.last_seen",
	"ground_speed":        "fh.ground_speed",
	"barometric_altitude": "fh.barometric_altitude",
	"distance_flown":      "fh.distance_flown",
	"route_distance":      "fh.route_distance",
	"distance_remaining":  "fh.distance_remaining",
}

// interestingGroupCodes maps the API's English query values to the short
// codes stored in interesting_aircraft."group" (Mil/Gov/Pol/Civ), matching
// the same mapping /api/stats/interesting/{military,government,police,civilian}
// already uses (see api.go's getRecentInterestingAircraft call sites).
var interestingGroupCodes = map[string]string{
	"military":   "Mil",
	"government": "Gov",
	"police":     "Pol",
	"civilian":   "Civ",
}

const dateLayout = "2006-01-02"

func parseFlightSearchParams(q url.Values, now time.Time) (flightSearchParams, error) {
	p := flightSearchParams{
		OriginStatus:      "any",
		DestinationStatus: "any",
		Sort:              "first_seen",
		Dir:               "desc",
		Page:              1,
		PageSize:          50,
	}

	fromStr := strings.TrimSpace(q.Get("from"))
	toStr := strings.TrimSpace(q.Get("to"))
	if fromStr != "" || toStr != "" {
		if fromStr == "" || toStr == "" {
			return p, fmt.Errorf("from and to must both be provided for a custom date range")
		}
		from, err := time.Parse(dateLayout, fromStr)
		if err != nil {
			return p, fmt.Errorf("invalid from date, expected YYYY-MM-DD")
		}
		to, err := time.Parse(dateLayout, toStr)
		if err != nil {
			return p, fmt.Errorf("invalid to date, expected YYYY-MM-DD")
		}
		to = to.Add(24 * time.Hour) // to is inclusive of the whole day
		if !from.Before(to) {
			return p, fmt.Errorf("from must be before to")
		}
		p.From = from
		p.To = to
	} else {
		period := strings.TrimSpace(q.Get("period"))
		if period == "" {
			period = "all_time"
		}
		if !isValidPeriodType(period) {
			return p, fmt.Errorf("invalid period %q", period)
		}
		if window, ok := periodWindow(period); ok {
			p.From = now.Add(-window)
		}
	}

	p.Manufacturer = strings.TrimSpace(q.Get("manufacturer"))
	p.Model = strings.TrimSpace(q.Get("model"))
	p.Country = strings.TrimSpace(q.Get("country"))
	p.Origin = strings.TrimSpace(q.Get("origin"))
	p.Destination = strings.TrimSpace(q.Get("destination"))
	p.Airline = strings.TrimSpace(q.Get("airline"))
	p.Query = strings.TrimSpace(q.Get("q"))

	altOp := strings.TrimSpace(q.Get("altitude_op"))
	altVal := strings.TrimSpace(q.Get("altitude_value"))
	if altOp != "" || altVal != "" {
		if altOp == "" || altVal == "" {
			return p, fmt.Errorf("altitude_op and altitude_value must be provided together")
		}
		if altOp != "gte" && altOp != "lte" {
			return p, fmt.Errorf("altitude_op must be gte or lte")
		}
		v, err := strconv.Atoi(altVal)
		if err != nil {
			return p, fmt.Errorf("altitude_value must be an integer")
		}
		p.AltitudeOp = altOp
		p.AltitudeValue = v
		p.HasAltitudeFilter = true
	}

	speedOp := strings.TrimSpace(q.Get("speed_op"))
	speedVal := strings.TrimSpace(q.Get("speed_value"))
	if speedOp != "" || speedVal != "" {
		if speedOp == "" || speedVal == "" {
			return p, fmt.Errorf("speed_op and speed_value must be provided together")
		}
		if speedOp != "gte" && speedOp != "lte" {
			return p, fmt.Errorf("speed_op must be gte or lte")
		}
		v, err := strconv.Atoi(speedVal)
		if err != nil {
			return p, fmt.Errorf("speed_value must be an integer")
		}
		p.SpeedOp = speedOp
		p.SpeedValue = v
		p.HasSpeedFilter = true
	}

	if interesting := strings.TrimSpace(q.Get("interesting")); interesting != "" {
		code, ok := interestingGroupCodes[interesting]
		if !ok {
			return p, fmt.Errorf("invalid interesting %q", interesting)
		}
		p.Interesting = code
	}

	if v := strings.TrimSpace(q.Get("origin_status")); v != "" {
		if v != "any" && v != "known" && v != "unknown" {
			return p, fmt.Errorf("origin_status must be any, known or unknown")
		}
		p.OriginStatus = v
	}
	if v := strings.TrimSpace(q.Get("destination_status")); v != "" {
		if v != "any" && v != "known" && v != "unknown" {
			return p, fmt.Errorf("destination_status must be any, known or unknown")
		}
		p.DestinationStatus = v
	}

	if v := strings.TrimSpace(q.Get("sort")); v != "" {
		if _, ok := searchSortColumns[v]; !ok {
			return p, fmt.Errorf("invalid sort %q", v)
		}
		p.Sort = v
	}
	if v := strings.TrimSpace(q.Get("dir")); v != "" {
		if v != "asc" && v != "desc" {
			return p, fmt.Errorf("dir must be asc or desc")
		}
		p.Dir = v
	}

	if v, err := strconv.Atoi(q.Get("page")); err == nil && v > 0 {
		p.Page = v
	}
	if v, err := strconv.Atoi(q.Get("page_size")); err == nil && v > 0 && v <= 50 {
		p.PageSize = v
	}

	return p, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd core && go test ./... -run TestParseFlightSearchParams -v`
Expected: all `TestParseFlightSearchParams*` subtests PASS.

- [ ] **Step 5: Write the failing tests for `buildFlightSearchWhere` and `flightSearchOrderBy`**

Append to `core/search-flights-query_test.go`:
```go
func TestBuildFlightSearchWhereEmpty(t *testing.T) {
	p, _ := parseFlightSearchParams(url.Values{}, time.Now())
	where, args := buildFlightSearchWhere(p)
	if where != "TRUE" {
		t.Errorf("where = %q, want TRUE for no filters", where)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want empty", args)
	}
}

func TestBuildFlightSearchWhereEachFilter(t *testing.T) {
	now := mustParseTime(t, time.RFC3339, "2026-08-05T12:00:00Z")

	cases := []struct {
		name       string
		q          url.Values
		wantWhere  string
		wantArgs   []any
	}{
		{"manufacturer", url.Values{"manufacturer": {"Boeing"}}, "rd.manufacturer ILIKE $1", []any{"%Boeing%"}},
		{"model", url.Values{"model": {"A320"}}, "rd.type ILIKE $1", []any{"%A320%"}},
		{"country", url.Values{"country": {"GB"}}, "UPPER(rd.registered_owner_country_iso_name) = UPPER($1)", []any{"GB"}},
		{"origin", url.Values{"origin": {"ARN"}}, "UPPER(fh.origin_iata_code) = UPPER($1)", []any{"ARN"}},
		{"destination", url.Values{"destination": {"LHR"}}, "UPPER(fh.destination_iata_code) = UPPER($1)", []any{"LHR"}},
		{"airline", url.Values{"airline": {"Ryanair"}}, "rt.airline_name ILIKE $1", []any{"%Ryanair%"}},
		{"altitude gte", url.Values{"altitude_op": {"gte"}, "altitude_value": {"35000"}}, "fh.barometric_altitude >= $1", []any{35000}},
		{"altitude lte", url.Values{"altitude_op": {"lte"}, "altitude_value": {"1000"}}, "fh.barometric_altitude <= $1", []any{1000}},
		{"speed gte", url.Values{"speed_op": {"gte"}, "speed_value": {"450"}}, "fh.ground_speed >= $1", []any{450}},
		{"interesting", url.Values{"interesting": {"military"}}, `ia."group" = $1`, []any{"Mil"}},
		{"origin known", url.Values{"origin_status": {"known"}}, "fh.origin_iata_code IS NOT NULL AND fh.origin_iata_code != ''", nil},
		{"origin unknown", url.Values{"origin_status": {"unknown"}}, "(fh.origin_iata_code IS NULL OR fh.origin_iata_code = '')", nil},
		{"destination known", url.Values{"destination_status": {"known"}}, "fh.destination_iata_code IS NOT NULL AND fh.destination_iata_code != ''", nil},
		{"destination unknown", url.Values{"destination_status": {"unknown"}}, "(fh.destination_iata_code IS NULL OR fh.destination_iata_code = '')", nil},
		{"free text", url.Values{"q": {"SAS123"}}, "(fh.flight ILIKE $1 OR fh.registration ILIKE $1 OR fh.hex ILIKE $1)", []any{"%SAS123%"}},
	}

	for _, tc := range cases {
		p, err := parseFlightSearchParams(tc.q, now)
		if err != nil {
			t.Fatalf("%s: unexpected parse error: %v", tc.name, err)
		}
		where, args := buildFlightSearchWhere(p)
		if where != tc.wantWhere {
			t.Errorf("%s: where = %q, want %q", tc.name, where, tc.wantWhere)
		}
		if tc.wantArgs == nil {
			if len(args) != 0 {
				t.Errorf("%s: args = %v, want empty", tc.name, args)
			}
			continue
		}
		if len(args) != len(tc.wantArgs) {
			t.Fatalf("%s: args = %v, want %v", tc.name, args, tc.wantArgs)
		}
		for i := range args {
			if args[i] != tc.wantArgs[i] {
				t.Errorf("%s: args[%d] = %v, want %v", tc.name, i, args[i], tc.wantArgs[i])
			}
		}
	}
}

func TestBuildFlightSearchWhereCombinesWithAndAndOrdersArgs(t *testing.T) {
	now := mustParseTime(t, time.RFC3339, "2026-08-05T12:00:00Z")
	q := url.Values{"manufacturer": {"Boeing"}, "origin": {"ARN"}, "period": {"7d"}}
	p, err := parseFlightSearchParams(q, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	where, args := buildFlightSearchWhere(p)
	want := "fh.first_seen >= $1 AND rd.manufacturer ILIKE $2 AND UPPER(fh.origin_iata_code) = UPPER($3)"
	if where != want {
		t.Errorf("where = %q, want %q", where, want)
	}
	if len(args) != 3 || args[1] != "%Boeing%" || args[2] != "ARN" {
		t.Errorf("args = %v", args)
	}
}

func TestFlightSearchOrderBy(t *testing.T) {
	cases := []struct {
		sort, dir string
		want      string
	}{
		{"first_seen", "desc", "fh.first_seen DESC, fh.id DESC"},
		{"first_seen", "asc", "fh.first_seen ASC, fh.id ASC"},
		{"ground_speed", "asc", "fh.ground_speed ASC, fh.id ASC"},
	}
	for _, tc := range cases {
		p := flightSearchParams{Sort: tc.sort, Dir: tc.dir}
		if got := flightSearchOrderBy(p); got != tc.want {
			t.Errorf("sort=%s dir=%s: got %q, want %q", tc.sort, tc.dir, got, tc.want)
		}
	}
}
```

- [ ] **Step 6: Run the tests to verify they fail**

Run: `cd core && go test ./... -run 'TestBuildFlightSearchWhere|TestFlightSearchOrderBy' -v`
Expected: build failure — `undefined: buildFlightSearchWhere` / `undefined: flightSearchOrderBy`.

- [ ] **Step 7: Implement `buildFlightSearchWhere` and `flightSearchOrderBy`**

Append to `core/search-flights-query.go`:
```go
// buildFlightSearchWhere turns validated params into a SQL WHERE fragment
// (without the WHERE keyword) plus the $n-ordered argument list. Every value
// is passed as an argument — no filter value is ever concatenated into where.
// Column names come only from fixed string literals in this function, never
// from user input.
func buildFlightSearchWhere(p flightSearchParams) (string, []any) {
	var conditions []string
	var args []any

	addCond := func(cond string, val any) {
		args = append(args, val)
		conditions = append(conditions, fmt.Sprintf(cond, len(args)))
	}

	if !p.From.IsZero() {
		addCond("fh.first_seen >= $%d", p.From)
	}
	if !p.To.IsZero() {
		addCond("fh.first_seen < $%d", p.To)
	}
	if p.Manufacturer != "" {
		addCond("rd.manufacturer ILIKE $%d", "%"+p.Manufacturer+"%")
	}
	if p.Model != "" {
		addCond("rd.type ILIKE $%d", "%"+p.Model+"%")
	}
	if p.Country != "" {
		addCond("UPPER(rd.registered_owner_country_iso_name) = UPPER($%d)", p.Country)
	}
	if p.Origin != "" {
		addCond("UPPER(fh.origin_iata_code) = UPPER($%d)", p.Origin)
	}
	if p.Destination != "" {
		addCond("UPPER(fh.destination_iata_code) = UPPER($%d)", p.Destination)
	}
	if p.HasAltitudeFilter {
		op := ">="
		if p.AltitudeOp == "lte" {
			op = "<="
		}
		args = append(args, p.AltitudeValue)
		conditions = append(conditions, fmt.Sprintf("fh.barometric_altitude %s $%d", op, len(args)))
	}
	if p.HasSpeedFilter {
		op := ">="
		if p.SpeedOp == "lte" {
			op = "<="
		}
		args = append(args, p.SpeedValue)
		conditions = append(conditions, fmt.Sprintf("fh.ground_speed %s $%d", op, len(args)))
	}
	if p.Airline != "" {
		addCond("rt.airline_name ILIKE $%d", "%"+p.Airline+"%")
	}
	if p.Interesting != "" {
		addCond(`ia."group" = $%d`, p.Interesting)
	}
	switch p.OriginStatus {
	case "known":
		conditions = append(conditions, "fh.origin_iata_code IS NOT NULL AND fh.origin_iata_code != ''")
	case "unknown":
		conditions = append(conditions, "(fh.origin_iata_code IS NULL OR fh.origin_iata_code = '')")
	}
	switch p.DestinationStatus {
	case "known":
		conditions = append(conditions, "fh.destination_iata_code IS NOT NULL AND fh.destination_iata_code != ''")
	case "unknown":
		conditions = append(conditions, "(fh.destination_iata_code IS NULL OR fh.destination_iata_code = '')")
	}
	if p.Query != "" {
		n := len(args) + 1
		args = append(args, "%"+p.Query+"%")
		conditions = append(conditions, fmt.Sprintf("(fh.flight ILIKE $%d OR fh.registration ILIKE $%d OR fh.hex ILIKE $%d)", n, n, n))
	}

	if len(conditions) == 0 {
		return "TRUE", args
	}
	return strings.Join(conditions, " AND "), args
}

// flightSearchOrderBy builds the ORDER BY clause. p.Sort is guaranteed by
// parseFlightSearchParams to be a key of searchSortColumns, and p.Dir is
// guaranteed to be "asc" or "desc" — both are validated before this is ever
// called, so no further checking happens here. fh.id is a tiebreaker: several
// flight_history metric columns (and first_seen itself, whole-second
// granularity) can tie across rows ingested in the same tick.
func flightSearchOrderBy(p flightSearchParams) string {
	col := searchSortColumns[p.Sort]
	dir := "DESC"
	if p.Dir == "asc" {
		dir = "ASC"
	}
	return fmt.Sprintf("%s %s, fh.id %s", col, dir, dir)
}
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `cd core && go test ./... -run 'TestBuildFlightSearchWhere|TestFlightSearchOrderBy' -v`
Expected: all subtests PASS.

- [ ] **Step 9: Write the failing tests for row scanning/formatting helpers**

Append to `core/search-flights-query_test.go`:
```go
func strp(s string) *string { return &s }
func intp(i int) *int       { return &i }
func f64p(f float64) *float64 { return &f }

func TestFlightSearchRowToCSVRecordHandlesNils(t *testing.T) {
	r := flightSearchRow{
		Hex:       "4d201f",
		Flight:    strp("SAS123 "),
		FirstSeen: mustParseTime(t, time.RFC3339, "2026-08-01T10:00:00Z"),
		// every other pointer field left nil
	}
	rec := flightSearchRowToCSVRecord(r)
	if len(rec) != len(flightSearchCSVHeader) {
		t.Fatalf("record has %d fields, header has %d", len(rec), len(flightSearchCSVHeader))
	}
	if rec[0] != "4d201f" {
		t.Errorf("hex = %q, want 4d201f", rec[0])
	}
	if rec[1] != "SAS123 " {
		t.Errorf("flight = %q, want %q", rec[1], "SAS123 ")
	}
	// last_seen (index 5) is nil -> must render as empty string, not "<nil>" or panic
	if rec[5] != "" {
		t.Errorf("last_seen = %q, want empty string for nil", rec[5])
	}
}

func TestFlightSearchRowToCSVRecordFormatsNumbers(t *testing.T) {
	r := flightSearchRow{
		Hex:                 "4d201f",
		FirstSeen:           mustParseTime(t, time.RFC3339, "2026-08-01T10:00:00Z"),
		BarometricAltitude:  intp(35000),
		GroundSpeed:         f64p(450.5),
		DistanceFlown:       f64p(850.25),
	}
	rec := flightSearchRowToCSVRecord(r)
	idx := func(col string) int {
		for i, h := range flightSearchCSVHeader {
			if h == col {
				return i
			}
		}
		t.Fatalf("no CSV column named %q", col)
		return -1
	}
	if rec[idx("barometric_altitude")] != "35000" {
		t.Errorf("barometric_altitude = %q, want 35000", rec[idx("barometric_altitude")])
	}
	if rec[idx("ground_speed")] != "450.50" {
		t.Errorf("ground_speed = %q, want 450.50", rec[idx("ground_speed")])
	}
	if rec[idx("distance_flown")] != "850.25" {
		t.Errorf("distance_flown = %q, want 850.25", rec[idx("distance_flown")])
	}
}
```

- [ ] **Step 10: Run the tests to verify they fail**

Run: `cd core && go test ./... -run TestFlightSearchRowToCSVRecord -v`
Expected: build failure — `undefined: flightSearchRow` / `undefined: flightSearchRowToCSVRecord` / `undefined: flightSearchCSVHeader`.

- [ ] **Step 11: Implement the row struct, JSON/CSV mappers, and shared query fragments**

Append to `core/search-flights-query.go` (needs one more import, `time`, already imported; add `github.com/gin-gonic/gin` for `gin.H`):
```go
// (add to the import block at the top of the file)
// "github.com/gin-gonic/gin"

// flightSearchRow is one row of the search result set — every column the
// frontend needs, including the metrics that aren't filterable in v1
// (indicated/true air speed, geometric altitude), since the detail modal
// shows all of them. Nullable columns are pointers because LEFT JOINs and
// legitimately-missing readsb metrics both produce SQL NULLs here.
type flightSearchRow struct {
	Hex                        string
	Flight                     *string
	Registration               *string
	Type                       *string
	FirstSeen                  time.Time
	LastSeen                   *time.Time
	GroundSpeed                *float64
	IndicatedAirSpeed          *int
	TrueAirSpeed               *int
	BarometricAltitude         *int
	GeometricAltitude          *int
	DistanceFlown              *float64
	RouteDistance              *float64
	DistanceRemaining          *float64
	OriginIataCode             *string
	OriginIcaoCode             *string
	DestinationIataCode        *string
	DestinationIcaoCode        *string
	Manufacturer               *string
	Model                      *string
	RegisteredOwnerCountryName *string
	RegisteredOwnerCountryIso  *string
	InterestingGroup           *string
	AirlineName                *string
}

// flightSearchSelectColumns and flightSearchBaseQuery are shared verbatim by
// the JSON search handler and the CSV export handler (search-flights.go) so
// both endpoints filter and join identically. mode_s/hex are both lowercase
// (no case conversion needed); interesting_aircraft.icao is uppercase.
const flightSearchSelectColumns = `
	fh.hex, fh.flight, fh.registration, fh.type, fh.first_seen, fh.last_seen,
	fh.ground_speed, fh.indicated_air_speed, fh.true_air_speed,
	fh.barometric_altitude, fh.geometric_altitude,
	fh.distance_flown, fh.route_distance, fh.distance_remaining,
	fh.origin_iata_code, fh.origin_icao_code,
	fh.destination_iata_code, fh.destination_icao_code,
	rd.manufacturer, rd.type AS model,
	rd.registered_owner_country_name, rd.registered_owner_country_iso_name,
	ia."group" AS interesting_group, rt.airline_name`

const flightSearchBaseQuery = `
	FROM flight_history fh
	LEFT JOIN registration_data rd ON rd.mode_s = fh.hex
	LEFT JOIN interesting_aircraft ia ON ia.icao = UPPER(fh.hex)
	LEFT JOIN route_data rt ON rt.route_callsign = fh.flight
	WHERE `

// scanFlightSearchRow scans one row of a query selecting exactly
// flightSearchSelectColumns, in that order.
func scanFlightSearchRow(rows interface {
	Scan(dest ...any) error
}) (flightSearchRow, error) {
	var r flightSearchRow
	err := rows.Scan(
		&r.Hex, &r.Flight, &r.Registration, &r.Type, &r.FirstSeen, &r.LastSeen,
		&r.GroundSpeed, &r.IndicatedAirSpeed, &r.TrueAirSpeed,
		&r.BarometricAltitude, &r.GeometricAltitude,
		&r.DistanceFlown, &r.RouteDistance, &r.DistanceRemaining,
		&r.OriginIataCode, &r.OriginIcaoCode,
		&r.DestinationIataCode, &r.DestinationIcaoCode,
		&r.Manufacturer, &r.Model,
		&r.RegisteredOwnerCountryName, &r.RegisteredOwnerCountryIso,
		&r.InterestingGroup, &r.AirlineName,
	)
	return r, err
}

func flightSearchRowToJSON(r flightSearchRow) gin.H {
	return gin.H{
		"hex":                                r.Hex,
		"flight":                             r.Flight,
		"registration":                       r.Registration,
		"type":                               r.Type,
		"first_seen":                         r.FirstSeen,
		"last_seen":                          r.LastSeen,
		"ground_speed":                       r.GroundSpeed,
		"indicated_air_speed":                r.IndicatedAirSpeed,
		"true_air_speed":                     r.TrueAirSpeed,
		"barometric_altitude":                r.BarometricAltitude,
		"geometric_altitude":                 r.GeometricAltitude,
		"distance_flown":                     r.DistanceFlown,
		"route_distance":                     r.RouteDistance,
		"distance_remaining":                 r.DistanceRemaining,
		"origin_iata_code":                   r.OriginIataCode,
		"origin_icao_code":                   r.OriginIcaoCode,
		"destination_iata_code":              r.DestinationIataCode,
		"destination_icao_code":              r.DestinationIcaoCode,
		"manufacturer":                       r.Manufacturer,
		"model":                              r.Model,
		"registered_owner_country_name":      r.RegisteredOwnerCountryName,
		"registered_owner_country_iso_name":  r.RegisteredOwnerCountryIso,
		"interesting_group":                  r.InterestingGroup,
		"airline_name":                       r.AirlineName,
	}
}

var flightSearchCSVHeader = []string{
	"hex", "flight", "registration", "type", "first_seen", "last_seen",
	"ground_speed", "indicated_air_speed", "true_air_speed",
	"barometric_altitude", "geometric_altitude",
	"distance_flown", "route_distance", "distance_remaining",
	"origin_iata_code", "origin_icao_code", "destination_iata_code", "destination_icao_code",
	"manufacturer", "model", "registered_owner_country_name", "registered_owner_country_iso_name",
	"interesting_group", "airline_name",
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func formatTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func formatFloatPtr(f *float64) string {
	if f == nil {
		return ""
	}
	return strconv.FormatFloat(*f, 'f', 2, 64)
}

func formatIntPtr(i *int) string {
	if i == nil {
		return ""
	}
	return strconv.Itoa(*i)
}

// flightSearchRowToCSVRecord must produce fields in exactly the order of
// flightSearchCSVHeader.
func flightSearchRowToCSVRecord(r flightSearchRow) []string {
	return []string{
		r.Hex,
		derefStr(r.Flight),
		derefStr(r.Registration),
		derefStr(r.Type),
		r.FirstSeen.UTC().Format(time.RFC3339),
		formatTimePtr(r.LastSeen),
		formatFloatPtr(r.GroundSpeed),
		formatIntPtr(r.IndicatedAirSpeed),
		formatIntPtr(r.TrueAirSpeed),
		formatIntPtr(r.BarometricAltitude),
		formatIntPtr(r.GeometricAltitude),
		formatFloatPtr(r.DistanceFlown),
		formatFloatPtr(r.RouteDistance),
		formatFloatPtr(r.DistanceRemaining),
		derefStr(r.OriginIataCode),
		derefStr(r.OriginIcaoCode),
		derefStr(r.DestinationIataCode),
		derefStr(r.DestinationIcaoCode),
		derefStr(r.Manufacturer),
		derefStr(r.Model),
		derefStr(r.RegisteredOwnerCountryName),
		derefStr(r.RegisteredOwnerCountryIso),
		derefStr(r.InterestingGroup),
		derefStr(r.AirlineName),
	}
}
```

Note: `scanFlightSearchRow` takes a small inline interface (`interface{ Scan(dest ...any) error }`) rather than `pgx.Rows` directly so it stays dependency-light and matches both `pgx.Rows` and `pgx.Row` call sites used in Tasks 3–4 — both satisfy this interface.

- [ ] **Step 12: Run all tests in the package to verify everything passes and nothing else broke**

Run: `cd core && go test ./... -v 2>&1 | tail -60`
Expected: every `TestParseFlightSearchParams*`, `TestBuildFlightSearchWhere*`, `TestFlightSearchOrderBy`, `TestFlightSearchRowToCSVRecord*` PASSES, and all pre-existing tests still PASS (no regressions).

- [ ] **Step 13: Format and vet**

Run: `cd core && gofmt -l . && go vet ./...`
Expected: `gofmt -l .` prints nothing (no unformatted files); `go vet` prints nothing.

- [ ] **Step 14: Commit**

```bash
git add core/search-flights-query.go core/search-flights-query_test.go
git commit -m "feat: add pure filter-parsing and query-building logic for flight search"
```

---

### Task 3: JSON search endpoint

**Files:**
- Create: `core/search-flights.go`
- Modify: `core/api.go` (add the `search` route group)

**Interfaces:**
- Consumes (from Task 2): `parseFlightSearchParams`, `buildFlightSearchWhere`, `flightSearchOrderBy`, `flightSearchSelectColumns`, `flightSearchBaseQuery`, `scanFlightSearchRow`, `flightSearchRowToJSON`.
- Produces (consumed by Task 4, which lives in the same file): nothing new by name, but establishes the file `core/search-flights.go` that Task 4 appends to.

This handler has no unit tests — per this project's convention (see `CLAUDE.md`), the database and HTTP-handler layers have no test harness; correctness here is `go build`/`go vet` plus later manual/runtime verification, matching how every other DB-backed handler in this codebase is checked.

- [ ] **Step 1: Write the handler**

`core/search-flights.go`:
```go
package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

func (s *APIServer) getFlightSearch(c *gin.Context) {
	params, err := parseFlightSearchParams(c.Request.URL.Query(), time.Now())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	where, args := buildFlightSearchWhere(params)
	orderBy := flightSearchOrderBy(params)

	var total int
	countQuery := "SELECT COUNT(*) " + flightSearchBaseQuery + where
	if err := s.pg.db.QueryRow(context.Background(), countQuery, args...).Scan(&total); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	offset := (params.Page - 1) * params.PageSize
	dataArgs := append(append([]any{}, args...), params.PageSize, offset)
	dataQuery := fmt.Sprintf(
		"SELECT %s %s %s ORDER BY %s LIMIT $%d OFFSET $%d",
		flightSearchSelectColumns, flightSearchBaseQuery, where, orderBy,
		len(args)+1, len(args)+2,
	)

	rows, err := s.pg.db.Query(context.Background(), dataQuery, dataArgs...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	results := []gin.H{}
	for rows.Next() {
		r, err := scanFlightSearchRow(rows)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		results = append(results, flightSearchRowToJSON(r))
	}

	c.JSON(http.StatusOK, gin.H{
		"results":     results,
		"total_count": total,
		"page":        params.Page,
		"page_size":   params.PageSize,
	})
}
```

- [ ] **Step 2: Register the route**

In `core/api.go`, add a new route group after the existing `records := api.Group("/records")` block (around line 150, immediately before the `watches := api.Group("/watches")` block):
```go
		search := api.Group("/search")
		{
			search.GET("/flights", s.getFlightSearch)
		}
```

- [ ] **Step 3: Build and vet**

Run: `cd core && go build ./... && go vet ./...`
Expected: both succeed with no output/errors.

- [ ] **Step 4: Commit**

```bash
git add core/search-flights.go core/api.go
git commit -m "feat: add GET /api/search/flights endpoint"
```

---

### Task 4: CSV export endpoint

**Files:**
- Modify: `core/search-flights.go` (append the export handler)
- Modify: `core/api.go` (add the export route)

**Interfaces:**
- Consumes (from Task 2): `parseFlightSearchParams`, `buildFlightSearchWhere`, `flightSearchOrderBy`, `flightSearchSelectColumns`, `flightSearchBaseQuery`, `scanFlightSearchRow`, `flightSearchRowToCSVRecord`, `flightSearchCSVHeader`.
- Produces (consumed by Task 8, frontend): response header `X-Search-Truncated: true` when the 10,000-row cap is hit — the frontend must check for this exact header name/value.

- [ ] **Step 1: Write the export handler**

Append to `core/search-flights.go` (add `encoding/csv` to the import block):
```go
const flightSearchExportLimit = 10000

func (s *APIServer) exportFlightSearchCSV(c *gin.Context) {
	params, err := parseFlightSearchParams(c.Request.URL.Query(), time.Now())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	where, args := buildFlightSearchWhere(params)
	orderBy := flightSearchOrderBy(params)

	dataArgs := append(append([]any{}, args...), flightSearchExportLimit+1)
	dataQuery := fmt.Sprintf(
		"SELECT %s %s %s ORDER BY %s LIMIT $%d",
		flightSearchSelectColumns, flightSearchBaseQuery, where, orderBy, len(args)+1,
	)

	rows, err := s.pg.db.Query(context.Background(), dataQuery, dataArgs...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var records [][]string
	for rows.Next() {
		r, err := scanFlightSearchRow(rows)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		records = append(records, flightSearchRowToCSVRecord(r))
	}

	truncated := len(records) > flightSearchExportLimit
	if truncated {
		records = records[:flightSearchExportLimit]
	}

	filename := fmt.Sprintf("flight-search-%s.csv", time.Now().UTC().Format("20060102-150405"))
	c.Header("Content-Type", "text/csv")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	if truncated {
		c.Header("X-Search-Truncated", "true")
	}

	w := csv.NewWriter(c.Writer)
	if err := w.Write(flightSearchCSVHeader); err != nil {
		return
	}
	for _, rec := range records {
		if err := w.Write(rec); err != nil {
			return
		}
	}
	w.Flush()
}
```

- [ ] **Step 2: Register the route**

In `core/api.go`, extend the `search` group added in Task 3:
```go
		search := api.Group("/search")
		{
			search.GET("/flights", s.getFlightSearch)
			search.GET("/flights/export", s.exportFlightSearchCSV)
		}
```

- [ ] **Step 3: Build and vet**

Run: `cd core && go build ./... && go vet ./...`
Expected: both succeed with no output/errors.

- [ ] **Step 4: Commit**

```bash
git add core/search-flights.go core/api.go
git commit -m "feat: add CSV export for flight search"
```

---

### Task 5: Flight detail modal

**Files:**
- Create: `web/src/stores/flightDetailModal.js`
- Create: `web/src/components/FlightDetailModal.svelte`

**Interfaces:**
- Produces (consumed by Task 8): `export const selectedFlight` (a Svelte writable store holding either `null` or a full flight-search result row object as returned by `GET /api/search/flights`), `export function openFlightModal(flight)`, `export function closeFlightModal()`.

Search results are historical *flights* (hex + first_seen), not "the aircraft's current state" — the existing `AircraftModal.svelte`/`selectedHex` store is keyed on hex alone and fetches live aggregate data, so it cannot show one specific flight leg. This is a new, separate modal that needs no fetch: every field it displays is already present in the row object passed to it by the results table (Task 6).

- [ ] **Step 1: Write the store**

`web/src/stores/flightDetailModal.js`:
```js
import { writable } from 'svelte/store';

// Holds the full flight-search result row whose detail modal is open, or
// null when closed. Unlike aircraftModal.js's selectedHex, this needs no
// fetch-by-key — the row already carries every field the modal displays.
export const selectedFlight = writable(null);

export function openFlightModal(flight) {
    if (!flight) return;
    selectedFlight.set(flight);
}

export function closeFlightModal() {
    selectedFlight.set(null);
}
```

- [ ] **Step 2: Write the modal component**

`web/src/components/FlightDetailModal.svelte`:
```svelte
<script>
    import { selectedFlight, closeFlightModal } from '../stores/flightDetailModal';

    let dialogEl;

    $: if ($selectedFlight && dialogEl) {
        dialogEl.showModal();
    }

    function onClose() {
        closeFlightModal();
    }

    function formatValue(v, suffix = '') {
        if (v === null || v === undefined || v === '') return '-';
        return suffix ? `${v} ${suffix}` : v;
    }

    function formatDate(v) {
        if (!v) return '-';
        return new Date(v).toLocaleString();
    }
</script>

<dialog bind:this={dialogEl} class="modal" on:close={onClose}>
    <div class="modal-box max-w-2xl">
        {#if $selectedFlight}
            <h3 class="text-lg font-bold mb-4">
                {formatValue($selectedFlight.flight)} &mdash; {formatValue($selectedFlight.registration)}
            </h3>
            <div class="grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
                <div><span class="font-semibold">Hex:</span> {$selectedFlight.hex}</div>
                <div><span class="font-semibold">Typ:</span> {formatValue($selectedFlight.type)}</div>
                <div><span class="font-semibold">Tillverkare:</span> {formatValue($selectedFlight.manufacturer)}</div>
                <div><span class="font-semibold">Modell:</span> {formatValue($selectedFlight.model)}</div>
                <div><span class="font-semibold">Registreringsland:</span> {formatValue($selectedFlight.registered_owner_country_name)}</div>
                <div><span class="font-semibold">Flygbolag:</span> {formatValue($selectedFlight.airline_name)}</div>
                <div><span class="font-semibold">Sedd f&ouml;rsta g&aring;ngen:</span> {formatDate($selectedFlight.first_seen)}</div>
                <div><span class="font-semibold">Sedd sista g&aring;ngen:</span> {formatDate($selectedFlight.last_seen)}</div>
                <div><span class="font-semibold">Avg&aring;ng:</span> {formatValue($selectedFlight.origin_iata_code)} ({formatValue($selectedFlight.origin_icao_code)})</div>
                <div><span class="font-semibold">Ankomst:</span> {formatValue($selectedFlight.destination_iata_code)} ({formatValue($selectedFlight.destination_icao_code)})</div>
                <div><span class="font-semibold">Ground speed:</span> {formatValue($selectedFlight.ground_speed, 'kt')}</div>
                <div><span class="font-semibold">Indicated air speed:</span> {formatValue($selectedFlight.indicated_air_speed, 'kt')}</div>
                <div><span class="font-semibold">True air speed:</span> {formatValue($selectedFlight.true_air_speed, 'kt')}</div>
                <div><span class="font-semibold">Barometrisk h&ouml;jd:</span> {formatValue($selectedFlight.barometric_altitude, 'ft')}</div>
                <div><span class="font-semibold">Geometrisk h&ouml;jd:</span> {formatValue($selectedFlight.geometric_altitude, 'ft')}</div>
                <div><span class="font-semibold">Flugen distans:</span> {formatValue($selectedFlight.distance_flown, 'km')}</div>
                <div><span class="font-semibold">Ruttavst&aring;nd:</span> {formatValue($selectedFlight.route_distance, 'km')}</div>
                <div><span class="font-semibold">Kvarvarande distans:</span> {formatValue($selectedFlight.distance_remaining, 'km')}</div>
                <div><span class="font-semibold">Interesting:</span> {formatValue($selectedFlight.interesting_group)}</div>
            </div>
        {/if}
    </div>
    <form method="dialog" class="modal-backdrop">
        <button>close</button>
    </form>
</dialog>
```

- [ ] **Step 3: Verify the frontend still builds**

Run: `cd web && npm run build`
Expected: build succeeds with no errors (the component isn't imported/used yet, but must still compile standalone).

- [ ] **Step 4: Commit**

```bash
git add web/src/stores/flightDetailModal.js web/src/components/FlightDetailModal.svelte
git commit -m "feat: add flight detail modal for search results"
```

---

### Task 6: Results table and pagination

**Files:**
- Create: `web/src/components/SearchResultsTable.svelte`
- Create: `web/src/components/SearchPagination.svelte`

**Interfaces:**
- Produces (consumed by Task 8):
  - `SearchResultsTable.svelte` props: `results` (array of row objects as returned by the search API), `loading` (bool), `error` (string or null), `sort` (string), `dir` (`"asc"`/`"desc"`), `onSort` (function taking a sort key string), `onRowClick` (function taking a row object).
  - `SearchPagination.svelte` props: `page` (number), `pageSize` (number), `totalCount` (number), `onPageChange` (function taking the new page number).

No existing component in this codebase has sortable column headers or page-number pagination (confirmed by exploration) — both are new patterns here, built from the existing `MotionStats.svelte`-style loading/error/empty branching and the `join`/`btn-sm` DaisyUI idiom already used for the Record Holders period buttons.

- [ ] **Step 1: Write the results table**

`web/src/components/SearchResultsTable.svelte`:
```svelte
<script>
    export let results = [];
    export let loading = false;
    export let error = null;
    export let sort = 'first_seen';
    export let dir = 'desc';
    export let onSort = () => {};
    export let onRowClick = () => {};

    const columns = [
        { key: 'first_seen', label: 'Sedd' },
        { key: null, label: 'Flight' },
        { key: null, label: 'Reg' },
        { key: null, label: 'Typ' },
        { key: null, label: 'Flygbolag' },
        { key: 'barometric_altitude', label: 'Höjd' },
        { key: 'ground_speed', label: 'Hastighet' },
        { key: 'distance_flown', label: 'Distans' },
        { key: null, label: 'Rutt' }
    ];

    function formatValue(v, suffix = '') {
        if (v === null || v === undefined || v === '') return '-';
        return suffix ? `${v} ${suffix}` : v;
    }

    function formatDate(v) {
        if (!v) return '-';
        return new Date(v).toLocaleString();
    }
</script>

<div class="overflow-x-auto mt-4">
    {#if loading}
        <div class="flex justify-center py-8">
            <span class="loading loading-ring loading-lg"></span>
        </div>
    {:else if error}
        <div class="alert alert-error">
            <span>Något gick fel: {error}</span>
        </div>
    {:else if results.length === 0}
        <div class="alert alert-info">
            <span>Inga träffar</span>
        </div>
    {:else}
        <table class="table">
            <thead>
                <tr>
                    {#each columns as col}
                        <th>
                            {#if col.key}
                                <button type="button" class="flex items-center gap-1" on:click={() => onSort(col.key)}>
                                    {col.label}
                                    {#if sort === col.key}
                                        <span>{dir === 'asc' ? '▲' : '▼'}</span>
                                    {/if}
                                </button>
                            {:else}
                                {col.label}
                            {/if}
                        </th>
                    {/each}
                </tr>
            </thead>
            <tbody>
                {#each results as row (row.hex + row.first_seen)}
                    <tr class="cursor-pointer hover:bg-base-300" on:click={() => onRowClick(row)}>
                        <td>{formatDate(row.first_seen)}</td>
                        <td>{formatValue(row.flight)}</td>
                        <td>{formatValue(row.registration)}</td>
                        <td>{formatValue(row.type)}</td>
                        <td>{formatValue(row.airline_name)}</td>
                        <td>{formatValue(row.barometric_altitude, 'ft')}</td>
                        <td>{formatValue(row.ground_speed, 'kt')}</td>
                        <td>{formatValue(row.distance_flown, 'km')}</td>
                        <td>{formatValue(row.origin_iata_code, '')} &rarr; {formatValue(row.destination_iata_code, '')}</td>
                    </tr>
                {/each}
            </tbody>
        </table>
    {/if}
</div>
```

- [ ] **Step 2: Write the pagination component**

`web/src/components/SearchPagination.svelte`:
```svelte
<script>
    export let page = 1;
    export let pageSize = 50;
    export let totalCount = 0;
    export let onPageChange = () => {};

    $: totalPages = Math.max(1, Math.ceil(totalCount / pageSize));
</script>

{#if totalCount > 0}
    <div class="flex justify-center items-center gap-4 mt-4">
        <button class="btn btn-sm" disabled={page <= 1} on:click={() => onPageChange(page - 1)}>Föregående</button>
        <span>Sida {page} av {totalPages} ({totalCount} träffar)</span>
        <button class="btn btn-sm" disabled={page >= totalPages} on:click={() => onPageChange(page + 1)}>Nästa</button>
    </div>
{/if}
```

- [ ] **Step 3: Verify the frontend still builds**

Run: `cd web && npm run build`
Expected: build succeeds with no errors.

- [ ] **Step 4: Commit**

```bash
git add web/src/components/SearchResultsTable.svelte web/src/components/SearchPagination.svelte
git commit -m "feat: add sortable results table and pagination for flight search"
```

---

### Task 7: Filter form

**Files:**
- Create: `web/src/components/SearchFilterForm.svelte`

**Interfaces:**
- Produces (consumed by Task 8): a component that dispatches a `search` custom event whose `event.detail.filters` is an object with exactly these keys: `period, useCustomRange, from, to, manufacturer, model, country, origin, destination, altitudeOp, altitudeValue, speedOp, speedValue, airline, interesting, originStatus, destinationStatus, q`. `interesting` is one of `""`, `"military"`, `"government"`, `"police"`, `"civilian"` (the English API values, not the DB's Mil/Gov/Pol/Civ codes — mapping happens server-side in `parseFlightSearchParams`, Task 2).

Filter state lives entirely inside this component as local `let` variables, submitted only on explicit button click — matching this codebase's only existing filter-editing pattern (`WatchEditorModal.svelte`'s local-state-then-explicit-save), not two-way prop binding of a nested object (which doesn't reactively propagate through Svelte's `bind:` in this component API without extra plumbing) and not debounced auto-search (grepped, no debounce pattern exists anywhere in this codebase; the spec itself recommends an explicit button for this reason).

- [ ] **Step 1: Write the filter form**

`web/src/components/SearchFilterForm.svelte`:
```svelte
<script>
    import { createEventDispatcher } from 'svelte';

    const dispatch = createEventDispatcher();

    const PERIODS = [
        { value: '24h', label: '24h' },
        { value: '7d', label: '7d' },
        { value: '30d', label: '30d' },
        { value: '90d', label: '90d' },
        { value: '365d', label: '365d' },
        { value: 'all_time', label: 'All-time' }
    ];

    let period = 'all_time';
    let useCustomRange = false;
    let from = '';
    let to = '';
    let manufacturer = '';
    let model = '';
    let country = '';
    let origin = '';
    let destination = '';
    let altitudeOp = 'gte';
    let altitudeValue = '';
    let speedOp = 'gte';
    let speedValue = '';
    let airline = '';
    let interesting = '';
    let originStatus = 'any';
    let destinationStatus = 'any';
    let q = '';

    function selectPreset(value) {
        useCustomRange = false;
        period = value;
    }

    function submit() {
        dispatch('search', {
            filters: {
                period, useCustomRange, from, to,
                manufacturer, model, country, origin, destination,
                altitudeOp, altitudeValue, speedOp, speedValue,
                airline, interesting, originStatus, destinationStatus, q
            }
        });
    }
</script>

<div class="card bg-base-100 shadow-sm rounded p-6">
    <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
        <label class="form-control">
            <div class="label"><span class="label-text">Fritext (flight/reg/hex)</span></div>
            <input type="text" class="input input-bordered" bind:value={q} placeholder="SAS123, SE-ABC, 4d201f" />
        </label>

        <label class="form-control">
            <div class="label"><span class="label-text">Tillverkare</span></div>
            <input type="text" class="input input-bordered" bind:value={manufacturer} placeholder="Boeing" />
            <div class="label"><span class="label-text-alt text-base-content/60">Endast tillgängligt för flygplan med känd registreringsdata (~33 % av flottan).</span></div>
        </label>

        <label class="form-control">
            <div class="label"><span class="label-text">Modell</span></div>
            <input type="text" class="input input-bordered" bind:value={model} placeholder="A320" />
            <div class="label"><span class="label-text-alt text-base-content/60">Endast tillgängligt för flygplan med känd registreringsdata.</span></div>
        </label>

        <label class="form-control">
            <div class="label"><span class="label-text">Registreringsland (ISO-kod)</span></div>
            <input type="text" class="input input-bordered" bind:value={country} placeholder="SE" maxlength="2" />
            <div class="label"><span class="label-text-alt text-base-content/60">Endast tillgängligt för flygplan med känd registreringsdata.</span></div>
        </label>

        <label class="form-control">
            <div class="label"><span class="label-text">Avgångsflygplats (IATA)</span></div>
            <input type="text" class="input input-bordered" bind:value={origin} placeholder="ARN" maxlength="3" />
        </label>

        <label class="form-control">
            <div class="label"><span class="label-text">Ankomstflygplats (IATA)</span></div>
            <input type="text" class="input input-bordered" bind:value={destination} placeholder="LHR" maxlength="3" />
        </label>

        <label class="form-control">
            <div class="label"><span class="label-text">Flygbolag</span></div>
            <input type="text" class="input input-bordered" bind:value={airline} placeholder="Ryanair" />
        </label>

        <label class="form-control">
            <div class="label"><span class="label-text">Höjd (barometrisk, ft)</span></div>
            <div class="join">
                <select class="select select-bordered join-item" bind:value={altitudeOp}>
                    <option value="gte">Över</option>
                    <option value="lte">Under</option>
                </select>
                <input type="number" class="input input-bordered join-item w-full" bind:value={altitudeValue} placeholder="35000" />
            </div>
        </label>

        <label class="form-control">
            <div class="label"><span class="label-text">Hastighet (ground speed, kt)</span></div>
            <div class="join">
                <select class="select select-bordered join-item" bind:value={speedOp}>
                    <option value="gte">Över</option>
                    <option value="lte">Under</option>
                </select>
                <input type="number" class="input input-bordered join-item w-full" bind:value={speedValue} placeholder="450" />
            </div>
        </label>

        <label class="form-control">
            <div class="label"><span class="label-text">Interesting-kategori</span></div>
            <select class="select select-bordered" bind:value={interesting}>
                <option value="">Alla</option>
                <option value="military">Militär</option>
                <option value="government">Regering</option>
                <option value="police">Polis</option>
                <option value="civilian">Civil</option>
            </select>
        </label>

        <label class="form-control">
            <div class="label"><span class="label-text">Avgångsflygplats status</span></div>
            <select class="select select-bordered" bind:value={originStatus}>
                <option value="any">Alla</option>
                <option value="known">Endast med känd</option>
                <option value="unknown">Endast utan känd</option>
            </select>
        </label>

        <label class="form-control">
            <div class="label"><span class="label-text">Ankomstflygplats status</span></div>
            <select class="select select-bordered" bind:value={destinationStatus}>
                <option value="any">Alla</option>
                <option value="known">Endast med känd</option>
                <option value="unknown">Endast utan känd</option>
            </select>
        </label>
    </div>

    <div class="mt-4">
        <div class="label"><span class="label-text">Tidsperiod</span></div>
        <div class="join">
            {#each PERIODS as p}
                <button type="button" class="join-item btn btn-sm {!useCustomRange && period === p.value ? 'btn-active btn-primary' : ''}"
                    on:click={() => selectPreset(p.value)}>
                    {p.label}
                </button>
            {/each}
            <button type="button" class="join-item btn btn-sm {useCustomRange ? 'btn-active btn-primary' : ''}"
                on:click={() => { useCustomRange = true; }}>
                Valfritt intervall
            </button>
        </div>
        {#if useCustomRange}
            <div class="flex gap-2 mt-2 items-center">
                <input type="date" class="input input-bordered" bind:value={from} />
                <span>till</span>
                <input type="date" class="input input-bordered" bind:value={to} />
            </div>
        {/if}
    </div>

    <div class="mt-4 flex justify-center">
        <button type="button" class="btn btn-primary" on:click={submit}>Sök</button>
    </div>
</div>
```

- [ ] **Step 2: Verify the frontend still builds**

Run: `cd web && npm run build`
Expected: build succeeds with no errors.

- [ ] **Step 3: Commit**

```bash
git add web/src/components/SearchFilterForm.svelte
git commit -m "feat: add flight search filter form"
```

---

### Task 8: Wire the Sök tab together

**Files:**
- Create: `web/src/lib/flightSearchUrl.js`
- Create: `web/src/components/TabFlightSearch.svelte`
- Modify: `web/src/App.svelte`

**Interfaces:**
- Consumes: `SearchFilterForm.svelte` (Task 7, `search` event with `event.detail.filters`), `SearchResultsTable.svelte` + `SearchPagination.svelte` (Task 6), `FlightDetailModal.svelte` + `openFlightModal` (Task 5), backend endpoints `GET /api/search/flights` and `GET /api/search/flights/export` (Tasks 3–4, including the `X-Search-Truncated` response header).
- Produces: nothing consumed elsewhere — this is the final integration point.

- [ ] **Step 1: Write the URL-building helper**

`web/src/lib/flightSearchUrl.js`:
```js
// Turns a SearchFilterForm filters object plus sort/pagination state into a
// URLSearchParams for /api/search/flights. Centralized here (mirroring
// stores/recordPeriod.js's buildRecordUrl) so the JSON search call and the
// CSV export call can never drift apart on which params they send.
export function buildFlightSearchParams(filters, sort, dir, page, pageSize) {
    const params = new URLSearchParams();

    if (filters.useCustomRange && filters.from && filters.to) {
        params.set('from', filters.from);
        params.set('to', filters.to);
    } else {
        params.set('period', filters.period);
    }

    if (filters.manufacturer) params.set('manufacturer', filters.manufacturer);
    if (filters.model) params.set('model', filters.model);
    if (filters.country) params.set('country', filters.country);
    if (filters.origin) params.set('origin', filters.origin);
    if (filters.destination) params.set('destination', filters.destination);
    if (filters.altitudeValue !== '') {
        params.set('altitude_op', filters.altitudeOp);
        params.set('altitude_value', filters.altitudeValue);
    }
    if (filters.speedValue !== '') {
        params.set('speed_op', filters.speedOp);
        params.set('speed_value', filters.speedValue);
    }
    if (filters.airline) params.set('airline', filters.airline);
    if (filters.interesting) params.set('interesting', filters.interesting);
    if (filters.originStatus && filters.originStatus !== 'any') params.set('origin_status', filters.originStatus);
    if (filters.destinationStatus && filters.destinationStatus !== 'any') params.set('destination_status', filters.destinationStatus);
    if (filters.q) params.set('q', filters.q);

    params.set('sort', sort);
    params.set('dir', dir);
    params.set('page', String(page));
    params.set('page_size', String(pageSize));

    return params;
}

export function buildFlightSearchUrl(filters, sort, dir, page, pageSize) {
    return `/api/search/flights?${buildFlightSearchParams(filters, sort, dir, page, pageSize)}`;
}

export function buildFlightSearchExportUrl(filters, sort, dir) {
    const params = buildFlightSearchParams(filters, sort, dir, 1, 50);
    params.delete('page');
    params.delete('page_size');
    return `/api/search/flights/export?${params}`;
}
```

- [ ] **Step 2: Write the tab component**

`web/src/components/TabFlightSearch.svelte`:
```svelte
<script>
    import SearchFilterForm from './SearchFilterForm.svelte';
    import SearchResultsTable from './SearchResultsTable.svelte';
    import SearchPagination from './SearchPagination.svelte';
    import FlightDetailModal from './FlightDetailModal.svelte';
    import { openFlightModal } from '../stores/flightDetailModal';
    import { buildFlightSearchUrl, buildFlightSearchExportUrl } from '../lib/flightSearchUrl';

    let filters = null;
    let results = [];
    let totalCount = 0;
    let page = 1;
    const pageSize = 50;
    let sort = 'first_seen';
    let dir = 'desc';
    let loading = false;
    let error = null;
    let searched = false;
    let exporting = false;
    let exportMessage = null;

    let requestSeq = 0;

    async function runSearch(resetPage) {
        if (!filters) return;
        if (resetPage) page = 1;
        const seq = ++requestSeq;
        loading = true;
        error = null;
        try {
            const response = await fetch(buildFlightSearchUrl(filters, sort, dir, page, pageSize));
            if (!response.ok) {
                const body = await response.json().catch(() => ({}));
                throw new Error(body.error || `${response.status}`);
            }
            const result = await response.json();
            if (seq !== requestSeq) return;
            results = result.results;
            totalCount = result.total_count;
            searched = true;
        } catch (err) {
            if (seq !== requestSeq) return;
            error = err.message;
            results = [];
            totalCount = 0;
        } finally {
            if (seq === requestSeq) loading = false;
        }
    }

    function handleSearchSubmit(event) {
        filters = event.detail.filters;
        runSearch(true);
    }

    function handleSort(column) {
        if (sort === column) {
            dir = dir === 'asc' ? 'desc' : 'asc';
        } else {
            sort = column;
            dir = 'desc';
        }
        runSearch(true);
    }

    function handlePageChange(newPage) {
        page = newPage;
        runSearch(false);
    }

    async function handleExport() {
        if (!filters) return;
        exporting = true;
        exportMessage = null;
        try {
            const response = await fetch(buildFlightSearchExportUrl(filters, sort, dir));
            if (!response.ok) {
                const body = await response.json().catch(() => ({}));
                throw new Error(body.error || `${response.status}`);
            }
            if (response.headers.get('X-Search-Truncated') === 'true') {
                exportMessage = 'Exporten begränsades till 10 000 rader. Förfina sökningen för att få med allt.';
            }
            const blob = await response.blob();
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = 'flight-search.csv';
            document.body.appendChild(a);
            a.click();
            a.remove();
            URL.revokeObjectURL(url);
        } catch (err) {
            exportMessage = `Export misslyckades: ${err.message}`;
        } finally {
            exporting = false;
        }
    }
</script>

<div>
    <SearchFilterForm on:search={handleSearchSubmit} />

    {#if exportMessage}
        <div class="alert alert-warning mt-4">
            <span>{exportMessage}</span>
        </div>
    {/if}

    {#if searched}
        <div class="flex justify-end mt-4">
            <button class="btn btn-sm btn-outline" on:click={handleExport} disabled={exporting}>
                {exporting ? 'Exporterar...' : 'Exportera CSV'}
            </button>
        </div>

        <SearchResultsTable
            {results}
            {loading}
            {error}
            {sort}
            {dir}
            onSort={handleSort}
            onRowClick={(row) => openFlightModal(row)}
        />

        <SearchPagination
            {page}
            {pageSize}
            {totalCount}
            onPageChange={handlePageChange}
        />
    {/if}
</div>

<FlightDetailModal />
```

- [ ] **Step 3: Register the tab in `App.svelte`**

In `web/src/App.svelte`, add the import alongside the other tab imports (near line 9, after `import TabWatches from './components/TabWatches.svelte';`):
```svelte
  import TabFlightSearch from './components/TabFlightSearch.svelte';
```
And add the tab entry to the `tabs` array (the array defined around line 19):
```svelte
  const tabs = [
    { name: 'current-stat', label: 'Current Sightings', component: TabCurrentSightings },
    { name: 'activity', label: 'Activity', component: TabActivity },
    { name: 'route-stat', label: 'Route Information', component: TabRouteStats },
    { name: 'interesting-stat', label: 'Interesting Aircraft', component: TabInterestingStats },
    { name: 'motion-stat', label: 'Record Holders', component: TabMotionStats },
    { name: 'watches', label: 'Watches', component: TabWatches },
    { name: 'search', label: 'Sök', component: TabFlightSearch }
  ];
```

- [ ] **Step 4: Verify the frontend builds**

Run: `cd web && npm run build`
Expected: build succeeds with no errors.

- [ ] **Step 5: Manual browser verification**

Run: `cd web && npm run dev -- --host` (requires the Go daemon + Postgres running separately for the API to answer; if unavailable, at minimum confirm the tab renders and the form is interactive).
Expected, once a backend is reachable:
- The "Sök" tab appears in the nav and switches correctly.
- Submitting the empty form (all_time, no filters) returns a results table with pagination and populates `total_count`.
- Clicking a row opens `FlightDetailModal` with that row's data.
- Sorting by a column header toggles asc/desc and refetches.
- "Exportera CSV" triggers a file download.
- An invalid combination (e.g. only `from` filled in, no `to` — not directly reachable via this UI since both date inputs are shown together, but verify via `curl "localhost:8080/api/search/flights?from=2026-01-01"` if the daemon is reachable) returns HTTP 400.

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/flightSearchUrl.js web/src/components/TabFlightSearch.svelte web/src/App.svelte
git commit -m "feat: wire up the Sök flight search tab"
```

---

## Self-Review Notes

**Spec coverage:** every v1 filter (period incl. custom range, manufacturer, model, registration country, origin/destination airport, altitude/speed over-under, airline, interesting-category, origin/destination known-unknown status, free text), sorting, 50/page pagination, CSV export with 10,000-row cap and truncation notice, the manufacturer/model/country coverage-gap UI notice, and the new "Sök" tab are each implemented in exactly one task above. v2/nice-to-have filters are explicitly excluded per Global Constraints, matching the spec's own recommendation to backlog them.

**Placeholder scan:** every step contains complete, runnable code — no `TODO`/`fill in`/"similar to Task N" shortcuts. The one intentionally-loose spot (browser click-through in Task 8 Step 5) is explicitly a manual-verification step, not a coded deliverable, consistent with how every prior feature in this codebase's history was runtime-verified after merge rather than during plan execution.

**Type/name consistency:** `flightSearchParams`, `buildFlightSearchWhere`, `flightSearchOrderBy`, `flightSearchRow`, `scanFlightSearchRow`, `flightSearchRowToJSON`, `flightSearchRowToCSVRecord`, `flightSearchSelectColumns`, `flightSearchBaseQuery`, `flightSearchCSVHeader` are defined once in Task 2 and referenced by the identical names in Tasks 3–4. Frontend: `openFlightModal`/`selectedFlight` (Task 5) are referenced by those exact names in Task 8; `SearchResultsTable`/`SearchPagination` prop names (Task 6) and `SearchFilterForm`'s `search` event detail shape (Task 7) match exactly what `TabFlightSearch.svelte` (Task 8) consumes.
