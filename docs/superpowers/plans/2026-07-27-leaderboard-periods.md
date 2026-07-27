# Period-based Leaderboards Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild the 7 Record Holders leaderboards so each can be viewed per time period (24h/7d/30d/90d/365d/all-time), capped at 100 rows per category+period, backed by the new `flight_history` + `records` tables.

**Architecture:** Keep the existing processed-flag ingest trigger (120s/300s tickers over `aircraft_data`) but redirect writes to `flight_history` (permanent archive) and `records` (per-period leaderboards). A central category→metric map drives ingest, trim, read sort, and the future custom-range path. Two new background jobs (sweep, retention) maintain the tables. The read API collapses to one shared handler behind the 7 existing routes; the frontend gains one global period selector.

**Tech Stack:** Go (package `main` in `core/`, pgx v5, gin, zerolog), PostgreSQL (golang-migrate), Svelte 5 + Tailwind/DaisyUI (Vite).

## Global Constraints

- Migration `000012_add_period_records` is written but **not yet run**. It renames the 7 old `*_aircraft` tables to `*_deprecated`. Migrations auto-run at daemon startup, so the code rewrite in this plan MUST be complete before the daemon next starts against a DB where 000012 runs. Do **not** run/deploy 000012 until Task 8.
- Ingest qualification is **variant A (insert-then-trim to 100)**. No threshold pre-gating.
- Default read period = `all_time`. Read row limit = `min(record_holder_table_limit, 100)`.
- Keep the 7 endpoint paths under `/api/stats/motion/...` unchanged.
- Period types (exact strings, matching the DB CHECK constraint): `24h`, `7d`, `30d`, `90d`, `365d`, `all_time`.
- Categories (exact strings): `fastest`, `slowest`, `highest`, `lowest`, `furthest_flown`, `longest_route`, `most_remaining`.
- Local Go lives at `~/.local/go/bin/go` on the WSL box (see [[deployment-251]] memory). Commands below say `go`; ensure it is on PATH or use the full path.
- This repo has no DB/HTTP/Svelte test harness. Pure Go logic is unit-tested with `go test`; DB/API/frontend tasks are verified by a successful build plus the deploy-and-observe protocol in Task 8.

---

### Task 1: Central category→metric map + pure helpers

**Files:**
- Create: `core/records-meta.go`
- Test: `core/records-meta_test.go`

**Interfaces:**
- Produces:
  - `type recordCategory struct { Name string; MetricName string; KeepMax bool }`
  - `var recordCategories map[string]recordCategory` (7 entries, keyed by category name)
  - `func (c recordCategory) bestFirstSQL() string` → `"DESC"` if KeepMax else `"ASC"`
  - `var allPeriodTypes []string` = the 6 period types in CHECK order
  - `func periodWindow(periodType string) (time.Duration, bool)` — ok=false for `all_time`
  - `func isValidPeriodType(p string) bool`
  - `func periodsForFirstSeen(firstSeen, now time.Time) []string`

- [ ] **Step 1: Write the failing tests**

Create `core/records-meta_test.go`:

```go
package main

import (
	"testing"
	"time"
)

func TestBestFirstSQL(t *testing.T) {
	cases := map[string]string{
		"fastest": "DESC",
		"slowest": "ASC",
		"highest": "DESC",
		"lowest":  "ASC",
	}
	for cat, want := range cases {
		if got := recordCategories[cat].bestFirstSQL(); got != want {
			t.Errorf("%s: got %s want %s", cat, got, want)
		}
	}
}

func TestPeriodWindow(t *testing.T) {
	if _, ok := periodWindow("all_time"); ok {
		t.Error("all_time should have no window")
	}
	w, ok := periodWindow("7d")
	if !ok || w != 7*24*time.Hour {
		t.Errorf("7d window wrong: %v ok=%v", w, ok)
	}
}

func TestPeriodsForFirstSeen_Fresh(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	got := periodsForFirstSeen(now.Add(-time.Minute), now)
	want := []string{"24h", "7d", "30d", "90d", "365d", "all_time"}
	if !equalStrings(got, want) {
		t.Errorf("fresh flight: got %v want %v", got, want)
	}
}

func TestPeriodsForFirstSeen_TenDaysOld(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	got := periodsForFirstSeen(now.Add(-10*24*time.Hour), now)
	want := []string{"30d", "90d", "365d", "all_time"}
	if !equalStrings(got, want) {
		t.Errorf("10-day-old flight: got %v want %v", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd core && go test ./... -run 'BestFirstSQL|PeriodWindow|PeriodsForFirstSeen'`
Expected: FAIL — `undefined: recordCategories` (and the other symbols).

- [ ] **Step 3: Write the implementation**

Create `core/records-meta.go`:

```go
package main

import "time"

// recordCategory is the single source of truth for how a leaderboard category
// is named and sorted. Shared by the ingest write path, the read/API path, and
// (later) the custom-range read path.
type recordCategory struct {
	Name       string // canonical category, matches records.category CHECK
	MetricName string // records.metric_name; also the flat JSON field the frontend expects
	KeepMax    bool   // true: larger metric_value is better (ORDER BY DESC); false: smaller (ASC)
}

var recordCategories = map[string]recordCategory{
	"fastest":        {Name: "fastest", MetricName: "ground_speed", KeepMax: true},
	"slowest":        {Name: "slowest", MetricName: "ground_speed", KeepMax: false},
	"highest":        {Name: "highest", MetricName: "barometric_altitude", KeepMax: true},
	"lowest":         {Name: "lowest", MetricName: "barometric_altitude", KeepMax: false},
	"furthest_flown": {Name: "furthest_flown", MetricName: "distance_flown", KeepMax: true},
	"longest_route":  {Name: "longest_route", MetricName: "route_distance", KeepMax: true},
	"most_remaining": {Name: "most_remaining", MetricName: "distance_remaining", KeepMax: true},
}

// bestFirstSQL returns the ORDER BY direction that puts the best record first
// for this category (used for read LIMIT and for trim-to-100).
func (c recordCategory) bestFirstSQL() string {
	if c.KeepMax {
		return "DESC"
	}
	return "ASC"
}

// allPeriodTypes lists every period_type in records.period_type CHECK order.
var allPeriodTypes = []string{"24h", "7d", "30d", "90d", "365d", "all_time"}

// periodWindow returns the sliding window for a windowed period_type. ok is
// false for "all_time", which has no window.
func periodWindow(periodType string) (time.Duration, bool) {
	switch periodType {
	case "24h":
		return 24 * time.Hour, true
	case "7d":
		return 7 * 24 * time.Hour, true
	case "30d":
		return 30 * 24 * time.Hour, true
	case "90d":
		return 90 * 24 * time.Hour, true
	case "365d":
		return 365 * 24 * time.Hour, true
	default:
		return 0, false
	}
}

// isValidPeriodType reports whether p is an accepted period_type.
func isValidPeriodType(p string) bool {
	if p == "all_time" {
		return true
	}
	_, ok := periodWindow(p)
	return ok
}

// periodsForFirstSeen returns the period_types whose window contains firstSeen,
// plus always "all_time". A freshly-evaluated flight normally lands in all of
// them; an old firstSeen (e.g. after downtime) is excluded from windows it has
// already fallen out of.
func periodsForFirstSeen(firstSeen, now time.Time) []string {
	var out []string
	for _, p := range allPeriodTypes {
		window, ok := periodWindow(p)
		if !ok { // all_time
			out = append(out, p)
			continue
		}
		if !firstSeen.Before(now.Add(-window)) {
			out = append(out, p)
		}
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd core && go test ./... -run 'BestFirstSQL|PeriodWindow|PeriodsForFirstSeen' -v`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add core/records-meta.go core/records-meta_test.go
git commit -m "feat: add central leaderboard category/period metadata"
```

---

### Task 2: Shared record write helpers (flight_history upsert, records insert, trim)

**Files:**
- Create: `core/records-ingest.go`

**Interfaces:**
- Consumes (Task 1): `recordCategories`, `recordCategory.bestFirstSQL()`, `periodsForFirstSeen()`.
- Produces:
  - `type recordCandidate struct { Hex, Flight, Registration, Type string; FirstSeen, LastSeen time.Time; MetricValue float64; Details map[string]any }`
  - `func upsertFlightHistory(pg *postgres, hex, flight, registration, aircraftType string, firstSeen, lastSeen time.Time, metricCols map[string]any)`
  - `func writeRecords(pg *postgres, category string, candidates []recordCandidate)`
  - `func trimRecordsBucket(pg *postgres, meta recordCategory, period string, maxRows int)`

- [ ] **Step 1: Write the implementation**

Create `core/records-ingest.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

// recordCandidate is one flight's contribution to a single leaderboard category.
type recordCandidate struct {
	Hex          string
	Flight       string
	Registration string
	Type         string
	FirstSeen    time.Time
	LastSeen     time.Time
	MetricValue  float64
	Details      map[string]any
}

// upsertFlightHistory merges one flight's known columns into flight_history.
// metricCols maps column name -> value for the columns this pass knows about;
// other columns keep their previous value via COALESCE. Column names come from
// our own code (never user input), so the dynamic SQL is safe.
func upsertFlightHistory(pg *postgres, hex, flight, registration, aircraftType string, firstSeen, lastSeen time.Time, metricCols map[string]any) {
	cols := []string{"hex", "flight", "registration", "type", "first_seen", "last_seen"}
	args := []any{hex, flight, registration, aircraftType, firstSeen, lastSeen}

	names := make([]string, 0, len(metricCols))
	for k := range metricCols {
		names = append(names, k)
	}
	sort.Strings(names) // deterministic column order
	for _, k := range names {
		cols = append(cols, k)
		args = append(args, metricCols[k])
	}

	placeholders := make([]string, len(args))
	for i := range args {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}

	updates := []string{"last_seen = EXCLUDED.last_seen"}
	for _, k := range names {
		updates = append(updates, fmt.Sprintf("%s = COALESCE(EXCLUDED.%s, flight_history.%s)", k, k, k))
	}

	query := fmt.Sprintf(
		`INSERT INTO flight_history (%s) VALUES (%s)
		 ON CONFLICT (hex, first_seen) DO UPDATE SET %s`,
		strings.Join(cols, ", "), strings.Join(placeholders, ", "), strings.Join(updates, ", "))

	if _, err := pg.db.Exec(context.Background(), query, args...); err != nil {
		log.Error().Err(err).Msg("upsertFlightHistory() - failed")
	}
}

// writeRecords inserts each candidate into every period bucket whose window
// contains its first_seen (all_time always), then trims each affected bucket
// to maxRows=100. Variant A: insert-then-trim, no threshold pre-gating.
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
	affected := map[string]bool{}
	batch := &pgx.Batch{}
	queued := 0

	insert := `
		INSERT INTO records (category, period_type, hex, flight, registration, type,
			first_seen, last_seen, metric_name, metric_value, details)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (category, period_type, hex, first_seen) DO UPDATE SET
			metric_value = EXCLUDED.metric_value,
			last_seen = EXCLUDED.last_seen,
			details = EXCLUDED.details`

	for _, c := range candidates {
		detailsJSON, err := json.Marshal(c.Details)
		if err != nil {
			log.Error().Err(err).Msg("writeRecords() - marshal details")
			continue
		}
		for _, period := range periodsForFirstSeen(c.FirstSeen, now) {
			batch.Queue(insert,
				category, period, c.Hex, c.Flight, c.Registration, c.Type,
				c.FirstSeen, c.LastSeen, meta.MetricName, c.MetricValue, detailsJSON)
			affected[period] = true
			queued++
		}
	}

	br := pg.db.SendBatch(context.Background(), batch)
	for i := 0; i < queued; i++ {
		if _, err := br.Exec(); err != nil {
			log.Error().Err(err).Msgf("writeRecords() - insert failed (%s)", category)
		}
	}
	br.Close()

	for period := range affected {
		trimRecordsBucket(pg, meta, period, 100)
	}
}

// trimRecordsBucket keeps only the best maxRows rows in one (category, period)
// bucket, deleting the rest. Best-first order comes from the category metadata.
func trimRecordsBucket(pg *postgres, meta recordCategory, period string, maxRows int) {
	query := fmt.Sprintf(`
		DELETE FROM records
		WHERE category = $1 AND period_type = $2
		  AND id NOT IN (
			SELECT id FROM records
			WHERE category = $1 AND period_type = $2
			ORDER BY metric_value %s, first_seen ASC
			LIMIT $3
		  )`, meta.bestFirstSQL())

	if _, err := pg.db.Exec(context.Background(), query, meta.Name, period, maxRows); err != nil {
		log.Error().Err(err).Msgf("trimRecordsBucket() - failed (%s/%s)", meta.Name, period)
	}
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd core && go build ./...`
Expected: builds with no errors. (Runtime behavior is exercised in Task 8 after migration 000012 runs.)

- [ ] **Step 3: Commit**

```bash
git add core/records-ingest.go
git commit -m "feat: add flight_history + records write helpers"
```

---

### Task 3: Rewrite motion ingest to write flight_history + records

**Files:**
- Modify: `core/stats-motion.go` (the 4 `update*Aircraft` functions)
- Delete: `core/stats-motion-helpers.go` (floor/ceiling threshold getters, now unused)

**Interfaces:**
- Consumes (Task 2): `upsertFlightHistory`, `writeRecords`, `recordCandidate`. (Task 1 indirectly.)
- Produces: unchanged public surface — `updateMeasurementStatistics(pg)` still called from `core.go`.

- [ ] **Step 1: Replace the 4 category functions**

In `core/stats-motion.go`, keep `updateMeasurementStatistics` and `getAircraftsForMeasurementStatistics` as they are. Replace the four `update{Lowest,Highest,Slowest,Fastest}Aircraft` functions with the versions below. Remove the now-unused `sort` import (only these functions used it).

```go
func updateLowestAircraft(pg *postgres, aircrafts []Aircraft) {
	var toProcess []Aircraft
	for _, a := range aircrafts {
		if !a.LowestProcessed {
			toProcess = append(toProcess, a)
		}
	}
	if len(toProcess) == 0 {
		return
	}

	var candidates []recordCandidate
	for _, a := range toProcess {
		if a.AltBaro < 1 { // validity: lowest needs a real altitude
			continue
		}
		upsertFlightHistory(pg, a.Hex, a.Flight, a.R, a.T, a.FirstSeen, a.LastSeen,
			map[string]any{"barometric_altitude": a.AltBaro, "geometric_altitude": a.AltGeom})
		candidates = append(candidates, recordCandidate{
			Hex: a.Hex, Flight: a.Flight, Registration: a.R, Type: a.T,
			FirstSeen: a.FirstSeen, LastSeen: a.LastSeen,
			MetricValue: float64(a.AltBaro),
			Details:     map[string]any{"geometric_altitude": a.AltGeom},
		})
	}
	writeRecords(pg, "lowest", candidates)
	MarkProcessed(pg, "lowest_aircraft_processed", toProcess)
}

func updateHighestAircraft(pg *postgres, aircrafts []Aircraft) {
	var toProcess []Aircraft
	for _, a := range aircrafts {
		if !a.HighestProcessed {
			toProcess = append(toProcess, a)
		}
	}
	if len(toProcess) == 0 {
		return
	}

	var candidates []recordCandidate
	for _, a := range toProcess {
		if a.AltBaro < 1 {
			continue
		}
		upsertFlightHistory(pg, a.Hex, a.Flight, a.R, a.T, a.FirstSeen, a.LastSeen,
			map[string]any{"barometric_altitude": a.AltBaro, "geometric_altitude": a.AltGeom})
		candidates = append(candidates, recordCandidate{
			Hex: a.Hex, Flight: a.Flight, Registration: a.R, Type: a.T,
			FirstSeen: a.FirstSeen, LastSeen: a.LastSeen,
			MetricValue: float64(a.AltBaro),
			Details:     map[string]any{"geometric_altitude": a.AltGeom},
		})
	}
	writeRecords(pg, "highest", candidates)
	MarkProcessed(pg, "highest_aircraft_processed", toProcess)
}

func updateSlowestAircraft(pg *postgres, aircrafts []Aircraft) {
	var toProcess []Aircraft
	for _, a := range aircrafts {
		if !a.SlowestProcessed {
			toProcess = append(toProcess, a)
		}
	}
	if len(toProcess) == 0 {
		return
	}

	var candidates []recordCandidate
	for _, a := range toProcess {
		if a.Gs < 1 { // validity: slowest needs a real, non-zero groundspeed
			continue
		}
		upsertFlightHistory(pg, a.Hex, a.Flight, a.R, a.T, a.FirstSeen, a.LastSeen,
			map[string]any{"ground_speed": a.Gs, "indicated_air_speed": a.Ias, "true_air_speed": a.Tas})
		candidates = append(candidates, recordCandidate{
			Hex: a.Hex, Flight: a.Flight, Registration: a.R, Type: a.T,
			FirstSeen: a.FirstSeen, LastSeen: a.LastSeen,
			MetricValue: a.Gs,
			Details:     map[string]any{"indicated_air_speed": a.Ias, "true_air_speed": a.Tas},
		})
	}
	writeRecords(pg, "slowest", candidates)
	MarkProcessed(pg, "slowest_aircraft_processed", toProcess)
}

func updateFastestAircraft(pg *postgres, aircrafts []Aircraft) {
	var toProcess []Aircraft
	for _, a := range aircrafts {
		if !a.FastestProcessed {
			toProcess = append(toProcess, a)
		}
	}
	if len(toProcess) == 0 {
		return
	}

	var candidates []recordCandidate
	for _, a := range toProcess {
		if a.Gs < 1 {
			continue
		}
		upsertFlightHistory(pg, a.Hex, a.Flight, a.R, a.T, a.FirstSeen, a.LastSeen,
			map[string]any{"ground_speed": a.Gs, "indicated_air_speed": a.Ias, "true_air_speed": a.Tas})
		candidates = append(candidates, recordCandidate{
			Hex: a.Hex, Flight: a.Flight, Registration: a.R, Type: a.T,
			FirstSeen: a.FirstSeen, LastSeen: a.LastSeen,
			MetricValue: a.Gs,
			Details:     map[string]any{"indicated_air_speed": a.Ias, "true_air_speed": a.Tas},
		})
	}
	writeRecords(pg, "fastest", candidates)
	MarkProcessed(pg, "fastest_aircraft_processed", toProcess)
}
```

Note: the old `ground_speed`/`altitude` detail keys match migration 000012's bootstrap and the old API output, so read-side JSON stays identical.

- [ ] **Step 2: Delete the obsolete helpers**

```bash
git rm core/stats-motion-helpers.go
```

(Variant A needs no floor/ceiling thresholds; these functions have no remaining callers.)

- [ ] **Step 3: Verify it compiles**

Run: `cd core && go build ./...`
Expected: builds with no errors, no "declared and not used" for `sort`.

- [ ] **Step 4: Commit**

```bash
git add core/stats-motion.go
git commit -m "feat: motion ingest writes flight_history + records"
```

---

### Task 4: Rewrite distance ingest; remove obsolete DeleteExcessRows

**Files:**
- Modify: `core/stats-distance.go` (the 3 `update*Aircraft` functions)
- Modify: `core/db-utils.go` (remove `DeleteExcessRows`; keep `MarkProcessed`)

**Interfaces:**
- Consumes (Task 2): `upsertFlightHistory`, `writeRecords`, `recordCandidate`. Existing `haversineDistanceKm`.
- Produces: unchanged public surface — `updateDistanceStatistics(pg)` still called from `core.go`.

- [ ] **Step 1: Replace the 3 distance functions**

In `core/stats-distance.go`, keep `updateDistanceStatistics` and `getAircraftsForDistanceStatistics`. Replace the three `update{FurthestFlown,MostRemaining,LongestRoute}Aircraft` functions with:

```go
func updateFurthestFlownAircraft(pg *postgres, aircrafts []Aircraft) {
	var toProcess []Aircraft
	for _, a := range aircrafts {
		if !a.FurthestFlownProcessed {
			toProcess = append(toProcess, a)
		}
	}
	if len(toProcess) == 0 {
		return
	}

	var candidates []recordCandidate
	for _, a := range toProcess {
		if !a.OriginLat.Valid || !a.OriginLon.Valid || !a.LastSeenLat.Valid || !a.LastSeenLon.Valid {
			continue
		}
		distanceFlown := haversineDistanceKm(
			a.OriginLat.Float64, a.OriginLon.Float64, a.LastSeenLat.Float64, a.LastSeenLon.Float64)

		upsertFlightHistory(pg, a.Hex, a.Flight, a.R, a.T, a.FirstSeen, a.LastSeen, map[string]any{
			"distance_flown":        distanceFlown,
			"origin_icao_code":      nullStr(a.OriginIcaoCode),
			"origin_iata_code":      nullStr(a.OriginIataCode),
			"destination_icao_code": nullStr(a.DestinationIcaoCode),
			"destination_iata_code": nullStr(a.DestinationIataCode),
		})
		candidates = append(candidates, recordCandidate{
			Hex: a.Hex, Flight: a.Flight, Registration: a.R, Type: a.T,
			FirstSeen: a.FirstSeen, LastSeen: a.LastSeen,
			MetricValue: distanceFlown,
			Details: map[string]any{
				"origin_icao_code":      nullStr(a.OriginIcaoCode),
				"origin_iata_code":      nullStr(a.OriginIataCode),
				"destination_icao_code": nullStr(a.DestinationIcaoCode),
				"destination_iata_code": nullStr(a.DestinationIataCode),
			},
		})
	}
	writeRecords(pg, "furthest_flown", candidates)
	MarkProcessed(pg, "furthest_flown_processed", toProcess)
}

func updateMostRemainingAircraft(pg *postgres, aircrafts []Aircraft) {
	var toProcess []Aircraft
	for _, a := range aircrafts {
		if !a.MostRemainingProcessed {
			toProcess = append(toProcess, a)
		}
	}
	if len(toProcess) == 0 {
		return
	}

	var candidates []recordCandidate
	for _, a := range toProcess {
		if !a.DestinationLat.Valid || !a.DestinationLon.Valid || !a.LastSeenLat.Valid || !a.LastSeenLon.Valid {
			continue
		}
		distanceRemaining := haversineDistanceKm(
			a.LastSeenLat.Float64, a.LastSeenLon.Float64, a.DestinationLat.Float64, a.DestinationLon.Float64)

		upsertFlightHistory(pg, a.Hex, a.Flight, a.R, a.T, a.FirstSeen, a.LastSeen, map[string]any{
			"distance_remaining":    distanceRemaining,
			"destination_icao_code": nullStr(a.DestinationIcaoCode),
			"destination_iata_code": nullStr(a.DestinationIataCode),
		})
		candidates = append(candidates, recordCandidate{
			Hex: a.Hex, Flight: a.Flight, Registration: a.R, Type: a.T,
			FirstSeen: a.FirstSeen, LastSeen: a.LastSeen,
			MetricValue: distanceRemaining,
			Details: map[string]any{
				"destination_icao_code": nullStr(a.DestinationIcaoCode),
				"destination_iata_code": nullStr(a.DestinationIataCode),
			},
		})
	}
	writeRecords(pg, "most_remaining", candidates)
	MarkProcessed(pg, "most_remaining_processed", toProcess)
}

func updateLongestRouteAircraft(pg *postgres, aircrafts []Aircraft) {
	var toProcess []Aircraft
	for _, a := range aircrafts {
		if !a.LongestRouteProcessed {
			toProcess = append(toProcess, a)
		}
	}
	if len(toProcess) == 0 {
		return
	}

	var candidates []recordCandidate
	for _, a := range toProcess {
		if !a.OriginLat.Valid || !a.OriginLon.Valid || !a.DestinationLat.Valid || !a.DestinationLon.Valid {
			continue
		}
		routeDistance := haversineDistanceKm(
			a.OriginLat.Float64, a.OriginLon.Float64, a.DestinationLat.Float64, a.DestinationLon.Float64)

		upsertFlightHistory(pg, a.Hex, a.Flight, a.R, a.T, a.FirstSeen, a.LastSeen, map[string]any{
			"route_distance":        routeDistance,
			"origin_icao_code":      nullStr(a.OriginIcaoCode),
			"origin_iata_code":      nullStr(a.OriginIataCode),
			"destination_icao_code": nullStr(a.DestinationIcaoCode),
			"destination_iata_code": nullStr(a.DestinationIataCode),
		})
		candidates = append(candidates, recordCandidate{
			Hex: a.Hex, Flight: a.Flight, Registration: a.R, Type: a.T,
			FirstSeen: a.FirstSeen, LastSeen: a.LastSeen,
			MetricValue: routeDistance,
			Details: map[string]any{
				"origin_icao_code":      nullStr(a.OriginIcaoCode),
				"origin_iata_code":      nullStr(a.OriginIataCode),
				"destination_icao_code": nullStr(a.DestinationIcaoCode),
				"destination_iata_code": nullStr(a.DestinationIataCode),
			},
		})
	}
	writeRecords(pg, "longest_route", candidates)
	MarkProcessed(pg, "longest_route_processed", toProcess)
}

// nullStr unwraps a sql.NullString to a *string so it JSON-marshals as null
// (not "") when absent, matching migration 000012's bootstrap details.
func nullStr(ns sql.NullString) any {
	if ns.Valid {
		return ns.String
	}
	return nil
}
```

Add `"database/sql"` to the imports of `core/stats-distance.go` (needed by `nullStr`).

- [ ] **Step 2: Remove the obsolete DeleteExcessRows**

In `core/db-utils.go`, delete the entire `DeleteExcessRows` function (both motion and distance ingest no longer call it; `records` is trimmed by `trimRecordsBucket`). Keep `MarkProcessed`. If removing it leaves an unused import, remove that too.

- [ ] **Step 3: Verify it compiles and no stale callers remain**

Run: `cd core && go build ./... && grep -rn "DeleteExcessRows\|stats-motion-helpers\|getFastestAircraftFloor\|getLowestAircraftCeiling\|getHighestAircraftFloor\|getSlowestAircraftCeiling" .`
Expected: build succeeds; grep prints nothing.

- [ ] **Step 4: Commit**

```bash
git add core/stats-distance.go core/db-utils.go
git commit -m "feat: distance ingest writes flight_history + records; drop DeleteExcessRows"
```

---

### Task 5: Sweep and retention background jobs

**Files:**
- Create: `core/records-jobs.go`
- Modify: `core/core.go` (ticker declarations, defer Stop, select cases)

**Interfaces:**
- Consumes (Task 1): `allPeriodTypes`, `periodWindow`.
- Produces:
  - `func runLeaderboardSweep(pg *postgres)`
  - `func runHistoryRetention(pg *postgres)`
  - `func getIntSetting(pg *postgres, key string, def int) int`

- [ ] **Step 1: Create the jobs file**

Create `core/records-jobs.go`:

```go
package main

import (
	"context"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"
)

// getIntSetting reads an integer user_settings value, returning def on any error.
func getIntSetting(pg *postgres, key string, def int) int {
	var val string
	err := pg.db.QueryRow(context.Background(),
		`SELECT setting_value FROM user_settings WHERE setting_key = $1`, key).Scan(&val)
	if err != nil {
		return def
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return def
	}
	return n
}

// runLeaderboardSweep deletes records whose first_seen has aged out of their
// period window. all_time is exempt (it only sheds rows via trim-to-100).
func runLeaderboardSweep(pg *postgres) {
	for _, period := range allPeriodTypes {
		window, ok := periodWindow(period)
		if !ok {
			continue // all_time
		}
		cutoff := time.Now().Add(-window)
		ct, err := pg.db.Exec(context.Background(),
			`DELETE FROM records WHERE period_type = $1 AND first_seen < $2`, period, cutoff)
		if err != nil {
			log.Error().Err(err).Msgf("runLeaderboardSweep() - failed for %s", period)
			continue
		}
		log.Debug().Msgf("Leaderboard sweep %s removed %d rows", period, ct.RowsAffected())
	}
}

// runHistoryRetention deletes flight_history older than history_retention_days
// that is no longer referenced by any active record. Active records are never
// purged regardless of age.
func runHistoryRetention(pg *postgres) {
	days := getIntSetting(pg, "history_retention_days", 730)
	cutoff := time.Now().AddDate(0, 0, -days)
	ct, err := pg.db.Exec(context.Background(), `
		DELETE FROM flight_history fh
		WHERE fh.first_seen < $1
		  AND NOT EXISTS (
			SELECT 1 FROM records r
			WHERE r.hex = fh.hex AND r.first_seen = fh.first_seen
		  )`, cutoff)
	if err != nil {
		log.Error().Err(err).Msg("runHistoryRetention() - failed")
		return
	}
	log.Debug().Msgf("History retention removed %d flight_history rows", ct.RowsAffected())
}
```

- [ ] **Step 2: Wire the tickers into core.go**

In `core/core.go`, after the existing ticker declarations (around line 111, after `updateDistanceStatisticsTicker`), add:

```go
	sweepMinutes := getIntSetting(pg, "leaderboard_sweep_interval_minutes", 60)
	leaderboardSweepTicker := time.NewTicker(time.Duration(sweepMinutes) * time.Minute)
	historyRetentionTicker := time.NewTicker(24 * time.Hour)
```

In the `defer func()` Stop block (around line 130), add:

```go
		leaderboardSweepTicker.Stop()
		historyRetentionTicker.Stop()
```

In the `for { select { ... } }` loop (around line 154, after the `updateDistanceStatisticsTicker` case), add:

```go
		case <-leaderboardSweepTicker.C:
			log.Debug().Msg("Leaderboard sweep")
			if newMinutes := getIntSetting(pg, "leaderboard_sweep_interval_minutes", 60); newMinutes > 0 && newMinutes != sweepMinutes {
				sweepMinutes = newMinutes
				leaderboardSweepTicker.Reset(time.Duration(sweepMinutes) * time.Minute)
				log.Info().Msgf("Leaderboard sweep interval changed to %d min", sweepMinutes)
			}
			runLeaderboardSweep(pg)
		case <-historyRetentionTicker.C:
			log.Debug().Msg("History retention")
			runHistoryRetention(pg)
```

(The interval is re-read at the top of each sweep tick and `Reset` applies a change from the next interval onward — no restart needed, consistent with the retention job.)

- [ ] **Step 3: Verify it compiles**

Run: `cd core && go build ./...`
Expected: builds with no errors.

- [ ] **Step 4: Commit**

```bash
git add core/records-jobs.go core/core.go
git commit -m "feat: add leaderboard sweep and history retention jobs"
```

---

### Task 6: Rewrite read API to one shared records handler

**Files:**
- Modify: `core/api.go` (add `getRecords`; rebind 7 routes; remove 7 old handlers; add imports)

**Interfaces:**
- Consumes (Task 1): `recordCategories`, `recordCategory.bestFirstSQL()`, `isValidPeriodType`.
- Produces: `func (s *APIServer) getRecords(c *gin.Context, category string)`.

- [ ] **Step 1: Ensure imports**

In `core/api.go` imports, ensure `"encoding/json"` and `"fmt"` are present (add any that are missing).

- [ ] **Step 2: Add the shared handler**

Add this function to `core/api.go` (e.g. where `getFastestAircraft` was):

```go
func (s *APIServer) getRecords(c *gin.Context, category string) {
	meta, ok := recordCategories[category]
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unknown category"})
		return
	}

	period := c.DefaultQuery("period", "all_time")
	if !isValidPeriodType(period) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid period"})
		return
	}

	limit := s.getLimit("record_holder_table_limit")
	if limit > 100 {
		limit = 100
	}

	query := fmt.Sprintf(`
		SELECT hex, flight, registration, type, first_seen, last_seen,
		       metric_value::float8, details
		FROM records
		WHERE category = $1 AND period_type = $2
		ORDER BY metric_value %s, first_seen ASC
		LIMIT $3`, meta.bestFirstSQL())

	rows, err := s.pg.db.Query(context.Background(), query, category, period, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	out := []gin.H{}
	for rows.Next() {
		var hex, flight, registration, aircraftType string
		var firstSeen, lastSeen *time.Time
		var metricValue float64
		var detailsRaw []byte

		if err := rows.Scan(&hex, &flight, &registration, &aircraftType,
			&firstSeen, &lastSeen, &metricValue, &detailsRaw); err != nil {
			continue
		}

		row := gin.H{
			"hex":           hex,
			"flight":        flight,
			"registration":  registration,
			"type":          aircraftType,
			"first_seen":    firstSeen,
			"last_seen":     lastSeen,
			meta.MetricName: metricValue,
		}
		if len(detailsRaw) > 0 {
			var details map[string]any
			if json.Unmarshal(detailsRaw, &details) == nil {
				for k, v := range details {
					row[k] = v
				}
			}
		}
		out = append(out, row)
	}

	c.JSON(http.StatusOK, out)
}
```

- [ ] **Step 3: Rebind the 7 routes and remove the old handlers**

In the route registration block (`core/api.go` ~lines 95-101), replace the 7 lines with:

```go
			stats.GET("/motion/fastest", func(c *gin.Context) { s.getRecords(c, "fastest") })
			stats.GET("/motion/slowest", func(c *gin.Context) { s.getRecords(c, "slowest") })
			stats.GET("/motion/highest", func(c *gin.Context) { s.getRecords(c, "highest") })
			stats.GET("/motion/lowest", func(c *gin.Context) { s.getRecords(c, "lowest") })
			stats.GET("/motion/furthest-flown", func(c *gin.Context) { s.getRecords(c, "furthest_flown") })
			stats.GET("/motion/most-remaining", func(c *gin.Context) { s.getRecords(c, "most_remaining") })
			stats.GET("/motion/longest-route", func(c *gin.Context) { s.getRecords(c, "longest_route") })
```

Then delete the 7 now-unused handler functions: `getFastestAircraft`, `getSlowestAircraft`, `getHighestAircraft`, `getLowestAircraft`, `getFurthestFlownAircraft`, `getMostRemainingAircraft`, `getLongestRouteAircraft`.

- [ ] **Step 4: Verify it compiles and no stale handlers remain**

Run: `cd core && go build ./... && grep -rn "getFastestAircraft\|getSlowestAircraft\|getHighestAircraft\|getLowestAircraft\|getFurthestFlownAircraft\|getMostRemainingAircraft\|getLongestRouteAircraft" core/`
Expected: build succeeds; grep prints nothing.

- [ ] **Step 5: Commit**

```bash
git add core/api.go
git commit -m "feat: single records read handler behind the 7 motion routes"
```

---

### Task 7: Frontend global period selector

**Files:**
- Create: `web/src/stores/recordPeriod.js`
- Modify: `web/src/components/MotionStats.svelte`
- Modify: `web/src/components/TabMotionStats.svelte`

**Interfaces:**
- Produces: `recordPeriod` (writable store), `RECORD_PERIODS` (array), `buildRecordUrl(endpoint, period)`.
- The 7 `Motion*.svelte` card files are **not** touched — the store decouples them.

- [ ] **Step 1: Create the period store + URL builder**

Create `web/src/stores/recordPeriod.js`:

```js
import { writable } from 'svelte/store';

// Current leaderboard period. A preset string for now; the custom-range seam
// will later allow an object descriptor ({ from, to }).
export const recordPeriod = writable('all_time');

export const RECORD_PERIODS = [
    { value: '24h', label: '24h' },
    { value: '7d', label: '7d' },
    { value: '30d', label: '30d' },
    { value: '90d', label: '90d' },
    { value: '365d', label: '365d' },
    { value: 'all_time', label: 'All-time' }
];

// Single place that turns (endpoint, period) into a URL. Later: if `period` is
// an object { from, to }, append &from=&to= here instead.
export function buildRecordUrl(endpoint, period) {
    const sep = endpoint.includes('?') ? '&' : '?';
    return `${endpoint}${sep}period=${encodeURIComponent(period)}`;
}
```

- [ ] **Step 2: Make MotionStats period-aware**

In `web/src/components/MotionStats.svelte`, update the `<script>` block. Change the import line and the fetch, and replace the `onMount` fetch with a reactive fetch driven by the period store:

Replace:
```js
    import { onMount } from 'svelte';
    import { refreshRecordHolderData } from '../stores/settings';
```
with:
```js
    import { refreshRecordHolderData } from '../stores/settings';
    import { recordPeriod, buildRecordUrl } from '../stores/recordPeriod';
```

Replace the `fetchData` fetch line:
```js
            const response = await fetch(endpoint);
```
with:
```js
            const response = await fetch(buildRecordUrl(endpoint, $recordPeriod));
```

Replace the `onMount(...)` block:
```js
    onMount(() => {
        fetchData();
    })
```
with a reactive statement that runs on mount and whenever the period changes:
```js
    // Fetch on mount and whenever the selected period changes.
    $: if (endpoint) {
        $recordPeriod;
        fetchData();
    }
```

Leave the existing `$: if ($refreshRecordHolderData) { fetchData(); }` block as-is.

- [ ] **Step 3: Add the selector to the Record Holders tab**

Replace the entire contents of `web/src/components/TabMotionStats.svelte` with:

```svelte
<script>
    import HideableCard from './HideableCard.svelte';
    import { dashboardCards } from '../lib/dashboardCards';
    import { recordPeriod, RECORD_PERIODS } from '../stores/recordPeriod';

    const cards = dashboardCards.filter((c) => c.tab === 'motion-stat');
</script>

<div class="flex justify-center mt-10">
    <div class="join">
        {#each RECORD_PERIODS as p}
            <button
                type="button"
                class="join-item btn btn-sm {$recordPeriod === p.value ? 'btn-active btn-primary' : ''}"
                on:click={() => recordPeriod.set(p.value)}
            >
                {p.label}
            </button>
        {/each}
    </div>
</div>

<div class="grid grid-cols-1 lg:grid-cols-2 mt-6 gap-6">
    {#each cards as card (card.id)}
        <HideableCard id={card.id} title={card.title}>
            <svelte:component this={card.component} {...(card.props || {})} />
        </HideableCard>
    {/each}
</div>
```

- [ ] **Step 4: Verify the frontend builds**

Run: `cd web && npm run build`
Expected: build completes with no errors.

- [ ] **Step 5: Commit**

```bash
git add web/src/stores/recordPeriod.js web/src/components/MotionStats.svelte web/src/components/TabMotionStats.svelte
git commit -m "feat: global period selector for Record Holders"
```

---

### Task 8: End-to-end verification and deploy (runs migration 000012)

**Files:** none (integration).

This is the gate where migration 000012 first runs. Everything above must be committed and building first.

- [ ] **Step 1: Full local build of both halves**

Run: `cd core && go build ./... && go test ./... && cd ../web && npm run build`
Expected: Go builds, unit tests pass, frontend builds.

- [ ] **Step 2: Confirm no references to the old (soon-to-be-deprecated) table names remain in code**

Run: `grep -rn -E "fastest_aircraft|slowest_aircraft|highest_aircraft|lowest_aircraft|furthest_flown_aircraft|longest_route_aircraft|most_remaining_aircraft" core/ | grep -v "_deprecated" | grep -v "_test.go"`
Expected: prints nothing (all runtime references now go to `records` / `flight_history`). Matches inside migration files are fine and expected — restrict the check to `core/` as shown.

- [ ] **Step 3: Deploy to the 251 test server**

Deploy per the [[deployment-251]] flow: `git archive` the branch over SSH to `/opt/skystats`, then `docker compose up -d --build`. Startup applies migration 000012 (renames old tables to `*_deprecated`, bootstraps `flight_history` + all_time `records`).

- [ ] **Step 4: Confirm clean startup**

Run: `ssh root@192.168.1.251 'cd /opt/skystats && docker compose logs --tail=40 skystats | grep -iE "migrat|error|missing|does not exist"'`
Expected: migration 000012 applied; no "relation does not exist" / missing-table errors.

- [ ] **Step 5: Verify the API returns bootstrapped all-time data and honours period**

Run:
```bash
ssh root@192.168.1.251 'for cat in fastest slowest highest lowest furthest-flown most-remaining longest-route; do echo -n "$cat all_time: "; curl -s "http://localhost:5173/api/stats/motion/$cat?period=all_time" | head -c 120; echo; done; echo -n "invalid period -> "; curl -s -o /dev/null -w "%{http_code}\n" "http://localhost:5173/api/stats/motion/fastest?period=bogus"'
```
Expected: each category returns a JSON array (all-time bootstrapped from old data); the invalid-period request returns `400`. Period buckets `24h`/`7d`/etc. may be empty initially and fill over time.

- [ ] **Step 6: Verify the frontend selector**

Open http://192.168.1.251:5173, go to the Record Holders tab, confirm the period selector renders and switching periods reloads all 7 cards (all-time shows data; shorter windows may be empty until they fill). Spot-check that furthest/longest/most-remaining cards still show origin/destination codes (from the `details` jsonb).

- [ ] **Step 7: (Optional) Watch the jobs**

Temporarily set `LOG_LEVEL=DEBUG` in the server `.env` and restart to see `Leaderboard sweep ... removed N rows` and `History retention removed N rows` log lines on their tickers, then revert to `INFO`.

- [ ] **Step 8: Finalize**

No code commit here. If everything checks out, the branch is ready to merge. A later, separate migration will `DROP` the `*_deprecated` tables once this has run cleanly in practice.

---

## Self-Review

**Spec coverage:**
- Datamodell (flight_history/records/settings) — provided by migration 000012, consumed throughout. ✓
- Central kategori→metrik-karta — Task 1. ✓
- Ingest-omskrivning (motion + distance, insert-then-trim, window membership, COALESCE upsert) — Tasks 2, 3, 4. ✓
- Sweep-jobb (read-at-tick + Reset, all_time exempt) — Task 5. ✓
- Retention-jobb (NOT EXISTS guard, read setting each run) — Task 5. ✓
- Läs-/API-lager (shared handler, 7 routes kept, ?period=, default all_time, 400 on invalid, ≤100, same JSON shape) — Task 6. ✓
- Frontend (global selector, store seam, cards untouched) — Task 7. ✓
- Deploy-sekvens (migration co-deploy, bootstrap survives, empty period buckets expected) — Global Constraints + Task 8. ✓
- Verifiering (go build/test, deploy-observe, rollback via down.sql) — Task 8. ✓
- Custom-range sömmar (central map, ?period= param shape on same routes, opaque period descriptor + single URL builder) — Tasks 1, 6, 7. ✓

**Placeholder scan:** No TBD/TODO; every code step shows complete code; every command has an expected result. ✓

**Type consistency:** `recordCategory{Name, MetricName, KeepMax}`, `bestFirstSQL()`, `recordCandidate{...}`, `upsertFlightHistory(...)`, `writeRecords(category, candidates)`, `trimRecordsBucket(meta, period, maxRows)`, `getIntSetting`, `getRecords(c, category)`, `recordPeriod`/`RECORD_PERIODS`/`buildRecordUrl` — names and signatures are used consistently across Tasks 1-7. ✓
