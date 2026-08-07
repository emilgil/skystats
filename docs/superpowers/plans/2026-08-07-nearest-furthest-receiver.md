# Nearest/Furthest Receiver-Distance Records Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add two new Record Holders leaderboard cards, "Nearest" and "Furthest", showing the shortest/longest 2D distance an aircraft has ever been measured at from the receiver, with a true running min/max tracked across the whole flight rather than a ticker snapshot.

**Architecture:** Extend `aircraft_data` with six running-extreme columns updated every 2s tick (same pattern already used for `alt_baro`/`gs` session-max), finalize into the existing `flight_history`/`records` pipeline via a new staleness-gated ticker (only picks up rows whose `last_seen` is 10+ minutes old — the one deliberate deviation from the seven existing categories' tickers, which have no such gate). Everything downstream (read API, clearing, notifications, period filtering) is already generic over `category` and needs only new map entries, not new code.

**Tech Stack:** Go (pgx v5), PostgreSQL (golang-migrate), Svelte 5 + Tailwind/DaisyUI, `github.com/JamesLMilner/cheap-ruler-go`.

## Global Constraints

- 2D distance only, computed via the existing `getDistance()`/cheap-ruler path — never 3D/slant range (design spec, "Avståndsberäkning").
- Bearing is computed via cheap-ruler's own `Bearing()` method (same `CheapRuler` instance as distance), not readsb's `r_dir` field — user-confirmed decision (design doc, "Avståndsberäkning").
- "Flight complete" = `last_seen` older than 10 minutes, matching the existing `recentAircraftCache` / `last_seen_epoch > nowEpoch - 600` boundary in `aircraft.go` that already defines "new flight" elsewhere — user-confirmed decision (design doc, "Mätmetod").
- Category names: `nearest` and `furthest_range` (not `furthest_flown`, which is a different metric) — pre-verified free of collisions.
- No 3D range, no "overhead pass" counter — explicitly out of scope.
- No retroactive backfill for flights already in progress when this ships; they start accumulating from the next tick after deploy.
- Every DB-facing change in this plan has no automated test harness (per `CLAUDE.md`) and is verified by `go build` plus running the stack manually. Only pure, I/O-free logic (bearing normalization) gets a unit test, matching the existing `haversine_test.go` precedent.

---

## File Structure

| File | Responsibility |
|---|---|
| `migrations/000018_add_receiver_distance_records.{up,down}.sql` | New columns, `records.category` constraint, notification setting defaults |
| `core/models.go` | New `Aircraft` struct fields |
| `core/aircraft.go` | `getBearing`/`normalizeBearing`; hot-path read/insert/update wiring |
| `core/aircraft_test.go` (new) | Unit tests for `normalizeBearing` |
| `core/records-meta.go` | `recordCategories` entries for `nearest`/`furthest_range` |
| `core/records-meta_test.go` | Extend `TestBestFirstSQL` with the two new categories |
| `core/stats-receiver-distance.go` (new) | Staleness-gated finalize ticker |
| `core/core.go` | Wire the finalize ticker into the existing 120s tick |
| `core/notifications.go` | `recordDisplay` + notification config map entries |
| `core/api.go` | Two new `/api/stats/motion/*` routes |
| `web/src/components/MotionNearestAircraft.svelte` (new) | Nearest card |
| `web/src/components/MotionFurthestRangeAircraft.svelte` (new) | Furthest card |
| `web/src/lib/dashboardCards.js` | Register the two new cards |
| `web/src/components/Settings.svelte` | Danger Zone entries + notification toggles |

---

### Task 1: Database migration

**Files:**
- Create: `migrations/000018_add_receiver_distance_records.up.sql`
- Create: `migrations/000018_add_receiver_distance_records.down.sql`

**Interfaces:**
- Produces: columns `aircraft_data.{min,max}_distance_receiver[_altitude|_bearing]`, `aircraft_data.{nearest,furthest}_processed`; columns `flight_history.{min,max}_distance_receiver[_altitude|_bearing]`; `records.category` accepts `'nearest'` and `'furthest_range'`; `user_settings` rows `notify_record_nearest`, `notify_record_furthest_range`.

- [ ] **Step 1: Write the up migration**

```sql
-- Löpande min/max-avstånd till mottagaren per flygning, uppdaterat på varje
-- 2s-positionstick (aircraft.go), till skillnad från de befintliga
-- kategoriernas engångs-snapshot. Höjd/bearing sparas från samma tick som
-- satte respektive extremvärde.
ALTER TABLE aircraft_data ADD COLUMN min_distance_receiver NUMERIC(7,2);
ALTER TABLE aircraft_data ADD COLUMN min_distance_receiver_altitude INTEGER;
ALTER TABLE aircraft_data ADD COLUMN min_distance_receiver_bearing NUMERIC(5,2);
ALTER TABLE aircraft_data ADD COLUMN max_distance_receiver NUMERIC(7,2);
ALTER TABLE aircraft_data ADD COLUMN max_distance_receiver_altitude INTEGER;
ALTER TABLE aircraft_data ADD COLUMN max_distance_receiver_bearing NUMERIC(5,2);
ALTER TABLE aircraft_data ADD COLUMN nearest_processed BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE aircraft_data ADD COLUMN furthest_processed BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE flight_history ADD COLUMN min_distance_receiver NUMERIC(7,2);
ALTER TABLE flight_history ADD COLUMN min_distance_receiver_altitude INTEGER;
ALTER TABLE flight_history ADD COLUMN min_distance_receiver_bearing NUMERIC(5,2);
ALTER TABLE flight_history ADD COLUMN max_distance_receiver NUMERIC(7,2);
ALTER TABLE flight_history ADD COLUMN max_distance_receiver_altitude INTEGER;
ALTER TABLE flight_history ADD COLUMN max_distance_receiver_bearing NUMERIC(5,2);

-- records.category was created without an explicit constraint name in
-- migration 000012, so Postgres auto-named it <table>_<column>_check.
ALTER TABLE records DROP CONSTRAINT records_category_check;
ALTER TABLE records ADD CONSTRAINT records_category_check CHECK (category IN
    ('fastest','slowest','highest','lowest','furthest_flown','longest_route','most_remaining',
     'nearest','furthest_range'));

INSERT INTO user_settings (setting_key, setting_value, description) VALUES
    ('notify_record_nearest',        'true', 'Notify on new all-time nearest'),
    ('notify_record_furthest_range', 'true', 'Notify on new all-time furthest range')
ON CONFLICT (setting_key) DO NOTHING;
```

- [ ] **Step 2: Write the down migration**

```sql
-- Reverting while 'nearest'/'furthest_range' rows still exist in `records`
-- will fail the narrower CHECK below by design — clear those categories
-- (Settings → Danger Zone, or DELETE FROM records WHERE category IN
-- ('nearest','furthest_range')) before rolling back.
DELETE FROM user_settings WHERE setting_key IN ('notify_record_nearest', 'notify_record_furthest_range');

ALTER TABLE records DROP CONSTRAINT records_category_check;
ALTER TABLE records ADD CONSTRAINT records_category_check CHECK (category IN
    ('fastest','slowest','highest','lowest','furthest_flown','longest_route','most_remaining'));

ALTER TABLE flight_history DROP COLUMN IF EXISTS min_distance_receiver;
ALTER TABLE flight_history DROP COLUMN IF EXISTS min_distance_receiver_altitude;
ALTER TABLE flight_history DROP COLUMN IF EXISTS min_distance_receiver_bearing;
ALTER TABLE flight_history DROP COLUMN IF EXISTS max_distance_receiver;
ALTER TABLE flight_history DROP COLUMN IF EXISTS max_distance_receiver_altitude;
ALTER TABLE flight_history DROP COLUMN IF EXISTS max_distance_receiver_bearing;

ALTER TABLE aircraft_data DROP COLUMN IF EXISTS min_distance_receiver;
ALTER TABLE aircraft_data DROP COLUMN IF EXISTS min_distance_receiver_altitude;
ALTER TABLE aircraft_data DROP COLUMN IF EXISTS min_distance_receiver_bearing;
ALTER TABLE aircraft_data DROP COLUMN IF EXISTS max_distance_receiver;
ALTER TABLE aircraft_data DROP COLUMN IF EXISTS max_distance_receiver_altitude;
ALTER TABLE aircraft_data DROP COLUMN IF EXISTS max_distance_receiver_bearing;
ALTER TABLE aircraft_data DROP COLUMN IF EXISTS nearest_processed;
ALTER TABLE aircraft_data DROP COLUMN IF EXISTS furthest_processed;
```

- [ ] **Step 3: Verify migration file naming and numbering**

Run: `ls migrations/ | tail -5`
Expected: `000018_add_receiver_distance_records.down.sql` and `.up.sql` are the two highest-numbered files, following directly after `000017_add_route_attempts`.

- [ ] **Step 4: Commit**

```bash
git add migrations/000018_add_receiver_distance_records.up.sql migrations/000018_add_receiver_distance_records.down.sql
git commit -m "feat: add migration for nearest/furthest receiver-distance records"
```

---

### Task 2: Bearing helper (TDD)

**Files:**
- Modify: `core/aircraft.go` (add after `getDistance`, currently `core/aircraft.go:80-84`)
- Create: `core/aircraft_test.go`

**Interfaces:**
- Produces: `func normalizeBearing(bearing float64) float64` — pure, testable. `func getBearing(aircraft []float64) float64` — returns compass degrees `[0, 360)` from the receiver to the given `[lon, lat]` point; NOT nullable/pointer (unlike `getDistance`, which returns `*float64`), since bearing has no "unavailable" state — callers always dereference `getDistance()` immediately when pairing the two.

- [ ] **Step 1: Write the failing tests**

Create `core/aircraft_test.go`:

```go
package main

import "testing"

func TestNormalizeBearing_Positive(t *testing.T) {
	if got := normalizeBearing(90); got != 90 {
		t.Errorf("expected 90, got %f", got)
	}
}

func TestNormalizeBearing_Negative(t *testing.T) {
	if got := normalizeBearing(-90); got != 270 {
		t.Errorf("expected 270, got %f", got)
	}
}

func TestNormalizeBearing_Zero(t *testing.T) {
	if got := normalizeBearing(0); got != 0 {
		t.Errorf("expected 0, got %f", got)
	}
}

func TestNormalizeBearing_UpperBound(t *testing.T) {
	// cheap-ruler's Bearing() range is (-180, 180], so 180 is the largest
	// value it can return and must pass through unchanged.
	if got := normalizeBearing(180); got != 180 {
		t.Errorf("expected 180, got %f", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail to compile**

Run: `cd core && go test ./... -run TestNormalizeBearing -v`
Expected: FAIL — `undefined: normalizeBearing`

- [ ] **Step 3: Implement `normalizeBearing` and `getBearing`**

In `core/aircraft.go`, immediately after the existing `getDistance` function (ends at line 84 with its closing `}`), add:

```go
func getBearing(aircraft []float64) float64 {
	loc := []float64{getLon(), getLat()}
	return normalizeBearing(getRuler().Bearing(loc, aircraft))
}

// normalizeBearing converts cheap-ruler's Bearing() output, which is in
// (-180, 180], to a compass bearing in [0, 360).
func normalizeBearing(bearing float64) float64 {
	if bearing < 0 {
		return bearing + 360
	}
	return bearing
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd core && go test ./... -run TestNormalizeBearing -v`
Expected: PASS (all 4 subtests)

- [ ] **Step 5: Build to confirm nothing else broke**

Run: `cd core && go build -o /tmp/skystats-build-check`
Expected: exits 0, no compile errors

- [ ] **Step 6: Commit**

```bash
git add core/aircraft.go core/aircraft_test.go
git commit -m "feat: add self-computed receiver bearing helper"
```

---

### Task 3: Aircraft struct fields and hot-path wiring

**Files:**
- Modify: `core/models.go:64-67` (Aircraft struct, after `SlowestProcessed`)
- Modify: `core/aircraft.go:98-177` (`getAircraftsRecentlySeen`)
- Modify: `core/aircraft.go:179-300` (`insertNewAircrafts`)
- Modify: `core/aircraft.go:302-409` (`updateExistingAircrafts`)

**Interfaces:**
- Consumes: `getBearing([]float64) float64`, `normalizeBearing` (Task 2); DB columns from Task 1.
- Produces: `Aircraft.MinDistanceReceiver sql.NullFloat64`, `Aircraft.MinDistanceReceiverAltitude int`, `Aircraft.MinDistanceReceiverBearing float64`, `Aircraft.MaxDistanceReceiver sql.NullFloat64`, `Aircraft.MaxDistanceReceiverAltitude int`, `Aircraft.MaxDistanceReceiverBearing float64`, `Aircraft.NearestProcessed bool`, `Aircraft.FurthestProcessed bool` — populated and persisted every 2s tick. Task 5 (finalize ticker) reads these via its own query, not via this struct's DB round-trip.

- [ ] **Step 1: Add struct fields**

In `core/models.go`, after line 67 (`SlowestProcessed    bool`) and before the `// Fields used by the distance leaderboards` comment on line 69, add:

```go

	// Running min/max distance-to-receiver for Nearest/Furthest, updated on
	// every 2s position tick (see updateExistingAircrafts in aircraft.go),
	// finalized by updateReceiverDistanceStatistics once the flight goes
	// stale. Distance uses sql.NullFloat64 so "never observed this session"
	// is distinguishable from "0 km away"; altitude/bearing are always
	// written in the same branch as the distance so they don't need
	// independent nullability.
	MinDistanceReceiver         sql.NullFloat64
	MinDistanceReceiverAltitude int
	MinDistanceReceiverBearing  float64
	MaxDistanceReceiver         sql.NullFloat64
	MaxDistanceReceiverAltitude int
	MaxDistanceReceiverBearing  float64
	NearestProcessed            bool
	FurthestProcessed           bool
```

- [ ] **Step 2: Build to confirm the struct still compiles**

Run: `cd core && go build -o /tmp/skystats-build-check`
Expected: exits 0

- [ ] **Step 3: Extend `getAircraftsRecentlySeen`'s query and scan**

In `core/aircraft.go`, the query at lines ~122-139 currently ends its column list with `tas` before `FROM aircraft_data`. Change it to:

```go
	query := `
		SELECT DISTINCT ON (hex)
			id,
			hex,
			last_seen_epoch,
			last_seen_lat,
			last_seen_lon,
			last_seen_distance,
			alt_baro,
			alt_geom,
			gs,
			ias,
			tas,
			min_distance_receiver,
			min_distance_receiver_altitude,
			min_distance_receiver_bearing,
			max_distance_receiver,
			max_distance_receiver_altitude,
			max_distance_receiver_bearing
		FROM aircraft_data
		WHERE hex = ANY($1::text[])
			AND last_seen_epoch > $2
		ORDER BY hex, last_seen DESC;
	`
```

And the `rows.Scan(...)` call a few lines below (currently ending `&a.Tas)`) becomes:

```go
		err := rows.Scan(
			&a.Id,
			&a.Hex,
			&a.LastSeenEpoch,
			&a.LastSeenLat,
			&a.LastSeenLon,
			&a.LastSeenDistance,
			&a.AltBaro,
			&a.AltGeom,
			&a.Gs,
			&a.Ias,
			&a.Tas,
			&a.MinDistanceReceiver,
			&a.MinDistanceReceiverAltitude,
			&a.MinDistanceReceiverBearing,
			&a.MaxDistanceReceiver,
			&a.MaxDistanceReceiverAltitude,
			&a.MaxDistanceReceiverBearing)
```

This step matters even though it looks like read-only plumbing: without it, every cache-miss reload of an in-progress flight would come back with `MinDistanceReceiver`/`MaxDistanceReceiver` reset to invalid, silently discarding everything accumulated so far.

- [ ] **Step 4: Extend `insertNewAircrafts`' INSERT statement**

In `core/aircraft.go`, inside `insertNewAircrafts`, the per-aircraft block currently does:

```go
			lastSeenDistance := getDistance([]float64{aircraft.Lon, aircraft.Lat})
			aircraftsToInsert = append(aircraftsToInsert, aircraft)
```

Change it to also compute bearing:

```go
			lastSeenDistance := getDistance([]float64{aircraft.Lon, aircraft.Lat})
			bearing := getBearing([]float64{aircraft.Lon, aircraft.Lat})
			aircraftsToInsert = append(aircraftsToInsert, aircraft)
```

The INSERT statement's column list ends with `db_flags` (43rd column, placeholder `$43`). Change the closing of the column list from:

```sql
					seen,
					rssi,
					db_flags
				) VALUES (
```

to:

```sql
					seen,
					rssi,
					db_flags,
					min_distance_receiver,
					min_distance_receiver_altitude,
					min_distance_receiver_bearing,
					max_distance_receiver,
					max_distance_receiver_altitude,
					max_distance_receiver_bearing
				) VALUES (
```

and the placeholder list from:

```sql
					$29, $30, $31, $32, $33, $34, $35, $36, $37, $38, $39, $40, $41, $42, $43
				)`
```

to:

```sql
					$29, $30, $31, $32, $33, $34, $35, $36, $37, $38, $39, $40, $41, $42, $43,
					$44, $45, $46, $47, $48, $49
				)`
```

And the `batch.Queue(insertStatement, ...)` call, which currently ends:

```go
				aircraft.Seen,
				aircraft.Rssi,
				aircraft.DbFlags)
```

becomes (min = max = the first observation for a brand-new flight):

```go
				aircraft.Seen,
				aircraft.Rssi,
				aircraft.DbFlags,
				*lastSeenDistance,
				aircraft.AltBaro,
				bearing,
				*lastSeenDistance,
				aircraft.AltBaro,
				bearing)
```

- [ ] **Step 5: Extend `updateExistingAircrafts`' running min/max and UPDATE statement**

In `core/aircraft.go`, inside `updateExistingAircrafts`, immediately after the existing block:

```go
		lastSeenDistance := getDistance([]float64{aircraft.Lon, aircraft.Lat})
		existingAircraft.LastSeenDistance = sql.NullFloat64{Float64: *lastSeenDistance, Valid: true}
```

add:

```go

		// Update running min/max distance-to-receiver for Nearest/Furthest —
		// tracked across the whole flight, not snapshotted once. See
		// updateReceiverDistanceStatistics (stats-receiver-distance.go) for
		// where the final value gets written out.
		bearing := getBearing([]float64{aircraft.Lon, aircraft.Lat})
		if !existingAircraft.MinDistanceReceiver.Valid || *lastSeenDistance < existingAircraft.MinDistanceReceiver.Float64 {
			existingAircraft.MinDistanceReceiver = sql.NullFloat64{Float64: *lastSeenDistance, Valid: true}
			existingAircraft.MinDistanceReceiverAltitude = aircraft.AltBaro
			existingAircraft.MinDistanceReceiverBearing = bearing
		}
		if !existingAircraft.MaxDistanceReceiver.Valid || *lastSeenDistance > existingAircraft.MaxDistanceReceiver.Float64 {
			existingAircraft.MaxDistanceReceiver = sql.NullFloat64{Float64: *lastSeenDistance, Valid: true}
			existingAircraft.MaxDistanceReceiverAltitude = aircraft.AltBaro
			existingAircraft.MaxDistanceReceiverBearing = bearing
		}
```

Then the `updateStatement` (currently ending `flight = $13` / `WHERE id = $14`):

```go
		updateStatement := `UPDATE aircraft_data
							SET last_seen = $1,
								last_seen_epoch = $2,
								last_seen_lat = $3,
								last_seen_lon = $4,
								last_seen_distance = $5,
								destination_distance = $6,
								track = $7,
								alt_baro = $8,
								alt_geom = $9,
								gs = $10,
								ias = $11,
								tas = $12,
								flight = $13
							WHERE id = $14`
```

becomes:

```go
		updateStatement := `UPDATE aircraft_data
							SET last_seen = $1,
								last_seen_epoch = $2,
								last_seen_lat = $3,
								last_seen_lon = $4,
								last_seen_distance = $5,
								destination_distance = $6,
								track = $7,
								alt_baro = $8,
								alt_geom = $9,
								gs = $10,
								ias = $11,
								tas = $12,
								flight = $13,
								min_distance_receiver = $14,
								min_distance_receiver_altitude = $15,
								min_distance_receiver_bearing = $16,
								max_distance_receiver = $17,
								max_distance_receiver_altitude = $18,
								max_distance_receiver_bearing = $19
							WHERE id = $20`
```

And the corresponding `batch.Queue(...)` call, currently:

```go
		batch.Queue(
			updateStatement,
			existingAircraft.LastSeen,
			existingAircraft.LastSeenEpoch,
			existingAircraft.LastSeenLat,
			existingAircraft.LastSeenLon,
			existingAircraft.LastSeenDistance,
			existingAircraft.DestinationDistance,
			existingAircraft.Track,
			existingAircraft.AltBaro,
			existingAircraft.AltGeom,
			existingAircraft.Gs,
			existingAircraft.Ias,
			existingAircraft.Tas,
			existingAircraft.Flight,
			existingAircraft.Id,
		)
```

becomes:

```go
		batch.Queue(
			updateStatement,
			existingAircraft.LastSeen,
			existingAircraft.LastSeenEpoch,
			existingAircraft.LastSeenLat,
			existingAircraft.LastSeenLon,
			existingAircraft.LastSeenDistance,
			existingAircraft.DestinationDistance,
			existingAircraft.Track,
			existingAircraft.AltBaro,
			existingAircraft.AltGeom,
			existingAircraft.Gs,
			existingAircraft.Ias,
			existingAircraft.Tas,
			existingAircraft.Flight,
			existingAircraft.MinDistanceReceiver,
			existingAircraft.MinDistanceReceiverAltitude,
			existingAircraft.MinDistanceReceiverBearing,
			existingAircraft.MaxDistanceReceiver,
			existingAircraft.MaxDistanceReceiverAltitude,
			existingAircraft.MaxDistanceReceiverBearing,
			existingAircraft.Id,
		)
```

- [ ] **Step 6: Build**

Run: `cd core && go build -o /tmp/skystats-build-check`
Expected: exits 0

- [ ] **Step 7: Run the full Go test suite**

Run: `cd core && go test ./...`
Expected: PASS (no existing test touches these functions, so this only guards against an accidental break elsewhere)

- [ ] **Step 8: Manual verification note**

This task has no DB available in an automated context (per `CLAUDE.md`, the DB layer has no test harness). When run against a real Postgres with migration 000018 applied: start the daemon, let it track a real aircraft for a couple of minutes, then `SELECT hex, last_seen_distance, min_distance_receiver, max_distance_receiver, min_distance_receiver_bearing, max_distance_receiver_bearing FROM aircraft_data WHERE hex = '<hex>';` and confirm `min_distance_receiver <= last_seen_distance <= max_distance_receiver` and bearings are in `[0, 360)`.

- [ ] **Step 9: Commit**

```bash
git add core/models.go core/aircraft.go
git commit -m "feat: track running min/max receiver distance per flight"
```

---

### Task 4: Register the two new record categories

**Files:**
- Modify: `core/records-meta.go:17-25` (`recordCategories` map)
- Modify: `core/records-meta_test.go:9-21` (`TestBestFirstSQL`)

**Interfaces:**
- Consumes: nothing new.
- Produces: `recordCategories["nearest"]` (`MetricName: "min_distance_receiver"`, `KeepMax: false`), `recordCategories["furthest_range"]` (`MetricName: "max_distance_receiver"`, `KeepMax: true`) — consumed by Task 5 (`writeRecords`), Task 6 (notifications), Task 7 (API read).

- [ ] **Step 1: Write the failing test**

In `core/records-meta_test.go`, extend the `cases` map in `TestBestFirstSQL` (currently 4 entries) to:

```go
func TestBestFirstSQL(t *testing.T) {
	cases := map[string]string{
		"fastest":        "DESC",
		"slowest":        "ASC",
		"highest":        "DESC",
		"lowest":         "ASC",
		"nearest":        "ASC",
		"furthest_range": "DESC",
	}
	for cat, want := range cases {
		if got := recordCategories[cat].bestFirstSQL(); got != want {
			t.Errorf("%s: got %s want %s", cat, got, want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd core && go test ./... -run TestBestFirstSQL -v`
Expected: FAIL — `recordCategories["nearest"]`/`["furthest_range"]` are zero-value (`bestFirstSQL()` on a zero-value `recordCategory` returns `"ASC"` since `KeepMax` defaults to `false`, so `nearest` would spuriously pass; `furthest_range` wanting `"DESC"` will fail since the zero value gives `"ASC"`) — confirms the test can catch a missing/wrong entry.

- [ ] **Step 3: Add the map entries**

In `core/records-meta.go`, change:

```go
var recordCategories = map[string]recordCategory{
	"fastest":        {Name: "fastest", MetricName: "ground_speed", KeepMax: true},
	"slowest":        {Name: "slowest", MetricName: "ground_speed", KeepMax: false},
	"highest":        {Name: "highest", MetricName: "barometric_altitude", KeepMax: true},
	"lowest":         {Name: "lowest", MetricName: "barometric_altitude", KeepMax: false},
	"furthest_flown": {Name: "furthest_flown", MetricName: "distance_flown", KeepMax: true},
	"longest_route":  {Name: "longest_route", MetricName: "route_distance", KeepMax: true},
	"most_remaining": {Name: "most_remaining", MetricName: "distance_remaining", KeepMax: true},
}
```

to:

```go
var recordCategories = map[string]recordCategory{
	"fastest":        {Name: "fastest", MetricName: "ground_speed", KeepMax: true},
	"slowest":        {Name: "slowest", MetricName: "ground_speed", KeepMax: false},
	"highest":        {Name: "highest", MetricName: "barometric_altitude", KeepMax: true},
	"lowest":         {Name: "lowest", MetricName: "barometric_altitude", KeepMax: false},
	"furthest_flown": {Name: "furthest_flown", MetricName: "distance_flown", KeepMax: true},
	"longest_route":  {Name: "longest_route", MetricName: "route_distance", KeepMax: true},
	"most_remaining": {Name: "most_remaining", MetricName: "distance_remaining", KeepMax: true},
	"nearest":        {Name: "nearest", MetricName: "min_distance_receiver", KeepMax: false},
	"furthest_range": {Name: "furthest_range", MetricName: "max_distance_receiver", KeepMax: true},
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd core && go test ./... -run TestBestFirstSQL -v`
Expected: PASS

- [ ] **Step 5: Run the full package test suite**

Run: `cd core && go test ./...`
Expected: PASS — in particular `TestValidateRecordCategories_AllKnown`/`_Unknown` are unaffected since they use their own hardcoded lists, not the full map.

- [ ] **Step 6: Commit**

```bash
git add core/records-meta.go core/records-meta_test.go
git commit -m "feat: register nearest/furthest_range record categories"
```

---

### Task 5: Finalize ticker

**Files:**
- Create: `core/stats-receiver-distance.go`
- Modify: `core/core.go:156-158` (existing `updateStatisticsTicker` case)

**Interfaces:**
- Consumes: `Aircraft.{Min,Max}DistanceReceiver[...]` fields (Task 3), `recordCategories["nearest"]`/`["furthest_range"]` (Task 4), `upsertFlightHistory`/`recordCandidate`/`writeRecords`/`MarkProcessed` (existing, `records-ingest.go`/`db-utils.go`).
- Produces: `func updateReceiverDistanceStatistics(pg *postgres)` — called once per 120s tick, same cadence as `updateMeasurementStatistics`.

- [ ] **Step 1: Write the finalize ticker**

Create `core/stats-receiver-distance.go`:

```go
package main

import (
	"context"

	"github.com/rs/zerolog/log"
)

// updateReceiverDistanceStatistics finalizes Nearest/Furthest for flights
// that are both unprocessed and truly over. Unlike every other category's
// ticker (stats-motion.go, stats-distance.go), which processes a row the
// moment it sees processed=false regardless of whether the aircraft is
// still being tracked, this one additionally requires last_seen to be 10+
// minutes old — the same boundary aircraft.go already uses elsewhere to
// decide a hex's next sighting starts a new flight. Without that gate, the
// running min/max accumulated so far in aircraft_data (updated every 2s in
// updateExistingAircrafts) could be finalized before the flight is actually
// done, silently discarding a more extreme value the flight would still go
// on to set.
func updateReceiverDistanceStatistics(pg *postgres) {
	aircrafts := getAircraftsForReceiverDistanceStatistics(pg)
	if len(aircrafts) == 0 {
		return
	}
	updateNearestAircraft(pg, aircrafts)
	updateFurthestRangeAircraft(pg, aircrafts)
}

func getAircraftsForReceiverDistanceStatistics(pg *postgres) []Aircraft {
	query := `SELECT id, hex, flight, r, t, first_seen, last_seen,
				min_distance_receiver, min_distance_receiver_altitude, min_distance_receiver_bearing,
				max_distance_receiver, max_distance_receiver_altitude, max_distance_receiver_bearing,
				nearest_processed, furthest_processed
				FROM aircraft_data
				WHERE last_seen < now() - interval '10 minutes'
					AND (nearest_processed = false OR furthest_processed = false)`

	rows, err := pg.db.Query(context.Background(), query)
	if err != nil {
		log.Error().Err(err).Msg("getAircraftsForReceiverDistanceStatistics() - Error querying db")
		return nil
	}
	defer rows.Close()

	var aircrafts []Aircraft
	for rows.Next() {
		var aircraft Aircraft
		err := rows.Scan(
			&aircraft.Id,
			&aircraft.Hex,
			&aircraft.Flight,
			&aircraft.R,
			&aircraft.T,
			&aircraft.FirstSeen,
			&aircraft.LastSeen,
			&aircraft.MinDistanceReceiver,
			&aircraft.MinDistanceReceiverAltitude,
			&aircraft.MinDistanceReceiverBearing,
			&aircraft.MaxDistanceReceiver,
			&aircraft.MaxDistanceReceiverAltitude,
			&aircraft.MaxDistanceReceiverBearing,
			&aircraft.NearestProcessed,
			&aircraft.FurthestProcessed)
		if err != nil {
			log.Error().Err(err).Msg("getAircraftsForReceiverDistanceStatistics() - Error scanning rows")
			continue
		}
		aircrafts = append(aircrafts, aircraft)
	}

	log.Debug().Msgf("Receiver-distance stats: %d stale unprocessed flights", len(aircrafts))
	return aircrafts
}

func updateNearestAircraft(pg *postgres, aircrafts []Aircraft) {
	var toProcess []Aircraft
	for _, a := range aircrafts {
		if !a.NearestProcessed {
			toProcess = append(toProcess, a)
		}
	}
	if len(toProcess) == 0 {
		return
	}

	var candidates []recordCandidate
	for _, a := range toProcess {
		if !a.MinDistanceReceiver.Valid { // validity: never actually got a position tick
			continue
		}
		upsertFlightHistory(pg, a.Hex, a.Flight, a.R, a.T, a.FirstSeen, a.LastSeen, map[string]any{
			"min_distance_receiver":          a.MinDistanceReceiver.Float64,
			"min_distance_receiver_altitude": a.MinDistanceReceiverAltitude,
			"min_distance_receiver_bearing":  a.MinDistanceReceiverBearing,
		})
		candidates = append(candidates, recordCandidate{
			Hex: a.Hex, Flight: a.Flight, Registration: a.R, Type: a.T,
			FirstSeen: a.FirstSeen, LastSeen: a.LastSeen,
			MetricValue: a.MinDistanceReceiver.Float64,
			Details: map[string]any{
				"min_distance_receiver_altitude": a.MinDistanceReceiverAltitude,
				"min_distance_receiver_bearing":  a.MinDistanceReceiverBearing,
			},
		})
	}
	writeRecords(pg, "nearest", candidates)
	MarkProcessed(pg, "nearest_processed", toProcess)
}

func updateFurthestRangeAircraft(pg *postgres, aircrafts []Aircraft) {
	var toProcess []Aircraft
	for _, a := range aircrafts {
		if !a.FurthestProcessed {
			toProcess = append(toProcess, a)
		}
	}
	if len(toProcess) == 0 {
		return
	}

	var candidates []recordCandidate
	for _, a := range toProcess {
		if !a.MaxDistanceReceiver.Valid {
			continue
		}
		upsertFlightHistory(pg, a.Hex, a.Flight, a.R, a.T, a.FirstSeen, a.LastSeen, map[string]any{
			"max_distance_receiver":          a.MaxDistanceReceiver.Float64,
			"max_distance_receiver_altitude": a.MaxDistanceReceiverAltitude,
			"max_distance_receiver_bearing":  a.MaxDistanceReceiverBearing,
		})
		candidates = append(candidates, recordCandidate{
			Hex: a.Hex, Flight: a.Flight, Registration: a.R, Type: a.T,
			FirstSeen: a.FirstSeen, LastSeen: a.LastSeen,
			MetricValue: a.MaxDistanceReceiver.Float64,
			Details: map[string]any{
				"max_distance_receiver_altitude": a.MaxDistanceReceiverAltitude,
				"max_distance_receiver_bearing":  a.MaxDistanceReceiverBearing,
			},
		})
	}
	writeRecords(pg, "furthest_range", candidates)
	MarkProcessed(pg, "furthest_processed", toProcess)
}
```

- [ ] **Step 2: Build**

Run: `cd core && go build -o /tmp/skystats-build-check`
Expected: exits 0

- [ ] **Step 3: Wire into the existing 120s tick**

In `core/core.go`, change:

```go
		case <-updateStatisticsTicker.C:
			log.Debug().Msg("Update Statistics")
			updateMeasurementStatistics(pg)
```

to:

```go
		case <-updateStatisticsTicker.C:
			log.Debug().Msg("Update Statistics")
			updateMeasurementStatistics(pg)
			updateReceiverDistanceStatistics(pg)
```

- [ ] **Step 4: Build**

Run: `cd core && go build -o /tmp/skystats-build-check`
Expected: exits 0

- [ ] **Step 5: Run the full Go test suite**

Run: `cd core && go test ./...`
Expected: PASS

- [ ] **Step 6: Manual verification note**

Verified by running the stack: after migration 000018 is applied and an aircraft has been tracked and then gone quiet for 10+ minutes, confirm rows appear via `SELECT * FROM records WHERE category IN ('nearest','furthest_range');` and that `aircraft_data.nearest_processed`/`furthest_processed` flip to `true` for that hex/first_seen.

- [ ] **Step 7: Commit**

```bash
git add core/stats-receiver-distance.go core/core.go
git commit -m "feat: finalize nearest/furthest records once a flight goes stale"
```

---

### Task 6: Notification wiring

**Files:**
- Modify: `core/notifications.go:93-101` (`recordDisplay` map)
- Modify: `core/notifications.go:118-129` (notification config map, inside `loadConfig` or equivalent — the block already containing `"furthest_flown": getBoolSetting(...)`)

**Interfaces:**
- Consumes: `recordCategories["nearest"]`/`["furthest_range"]` (Task 4, for consistency only — `recordDisplay` is a separate, parallel map keyed the same way).
- Produces: `recordDisplay["nearest"]`, `recordDisplay["furthest_range"]`; `cfg.Records["nearest"]`, `cfg.Records["furthest_range"]` populated from the `notify_record_nearest`/`notify_record_furthest_range` settings seeded in Task 1.

- [ ] **Step 1: Add `recordDisplay` entries**

In `core/notifications.go`, change:

```go
var recordDisplay = map[string]struct{ Name, Metric, Unit string }{
	"fastest":        {"Fastest", "Ground speed", "kt"},
	"slowest":        {"Slowest", "Ground speed", "kt"},
	"highest":        {"Highest", "Altitude", "ft"},
	"lowest":         {"Lowest", "Altitude", "ft"},
	"furthest_flown": {"Furthest flown", "Distance flown", "km"},
	"longest_route":  {"Longest route", "Route distance", "km"},
	"most_remaining": {"Most remaining", "Distance remaining", "km"},
}
```

to:

```go
var recordDisplay = map[string]struct{ Name, Metric, Unit string }{
	"fastest":        {"Fastest", "Ground speed", "kt"},
	"slowest":        {"Slowest", "Ground speed", "kt"},
	"highest":        {"Highest", "Altitude", "ft"},
	"lowest":         {"Lowest", "Altitude", "ft"},
	"furthest_flown": {"Furthest flown", "Distance flown", "km"},
	"longest_route":  {"Longest route", "Route distance", "km"},
	"most_remaining": {"Most remaining", "Distance remaining", "km"},
	"nearest":        {"Nearest", "Distance", "km"},
	"furthest_range": {"Furthest", "Distance", "km"},
}
```

- [ ] **Step 2: Add notification config map entries**

In `core/notifications.go`, find the block (currently lines ~118-129) that reads:

```go
			"fastest":        getBoolSetting(n.pg, "notify_record_fastest", true),
			"slowest":        getBoolSetting(n.pg, "notify_record_slowest", true),
			"highest":        getBoolSetting(n.pg, "notify_record_highest", true),
			"lowest":         getBoolSetting(n.pg, "notify_record_lowest", true),
			"furthest_flown": getBoolSetting(n.pg, "notify_record_furthest_flown", true),
			"longest_route":  getBoolSetting(n.pg, "notify_record_longest_route", true),
			"most_remaining": getBoolSetting(n.pg, "notify_record_most_remaining", true),
```

and change it to:

```go
			"fastest":        getBoolSetting(n.pg, "notify_record_fastest", true),
			"slowest":        getBoolSetting(n.pg, "notify_record_slowest", true),
			"highest":        getBoolSetting(n.pg, "notify_record_highest", true),
			"lowest":         getBoolSetting(n.pg, "notify_record_lowest", true),
			"furthest_flown": getBoolSetting(n.pg, "notify_record_furthest_flown", true),
			"longest_route":  getBoolSetting(n.pg, "notify_record_longest_route", true),
			"most_remaining": getBoolSetting(n.pg, "notify_record_most_remaining", true),
			"nearest":        getBoolSetting(n.pg, "notify_record_nearest", true),
			"furthest_range": getBoolSetting(n.pg, "notify_record_furthest_range", true),
```

- [ ] **Step 3: Build**

Run: `cd core && go build -o /tmp/skystats-build-check`
Expected: exits 0

- [ ] **Step 4: Run the full Go test suite**

Run: `cd core && go test ./...`
Expected: PASS — `core/notifications_test.go` does not enumerate `recordDisplay`/the config map exhaustively, so this is a safety net, not a targeted check.

- [ ] **Step 5: Commit**

```bash
git add core/notifications.go
git commit -m "feat: wire nearest/furthest into record notifications"
```

---

### Task 7: API routes

**Files:**
- Modify: `core/api.go:98-104` (stats route group)

**Interfaces:**
- Consumes: `recordCategories["nearest"]`/`["furthest_range"]` (Task 4), existing `(s *APIServer) getRecords(c *gin.Context, category string)`.
- Produces: `GET /api/stats/motion/nearest`, `GET /api/stats/motion/furthest`.

- [ ] **Step 1: Add the two routes**

In `core/api.go`, change:

```go
			stats.GET("/motion/fastest", func(c *gin.Context) { s.getRecords(c, "fastest") })
			stats.GET("/motion/slowest", func(c *gin.Context) { s.getRecords(c, "slowest") })
			stats.GET("/motion/highest", func(c *gin.Context) { s.getRecords(c, "highest") })
			stats.GET("/motion/lowest", func(c *gin.Context) { s.getRecords(c, "lowest") })
			stats.GET("/motion/furthest-flown", func(c *gin.Context) { s.getRecords(c, "furthest_flown") })
			stats.GET("/motion/most-remaining", func(c *gin.Context) { s.getRecords(c, "most_remaining") })
			stats.GET("/motion/longest-route", func(c *gin.Context) { s.getRecords(c, "longest_route") })
			stats.GET("/motion/recent", s.getRecentObservations)
```

to:

```go
			stats.GET("/motion/fastest", func(c *gin.Context) { s.getRecords(c, "fastest") })
			stats.GET("/motion/slowest", func(c *gin.Context) { s.getRecords(c, "slowest") })
			stats.GET("/motion/highest", func(c *gin.Context) { s.getRecords(c, "highest") })
			stats.GET("/motion/lowest", func(c *gin.Context) { s.getRecords(c, "lowest") })
			stats.GET("/motion/furthest-flown", func(c *gin.Context) { s.getRecords(c, "furthest_flown") })
			stats.GET("/motion/most-remaining", func(c *gin.Context) { s.getRecords(c, "most_remaining") })
			stats.GET("/motion/longest-route", func(c *gin.Context) { s.getRecords(c, "longest_route") })
			stats.GET("/motion/nearest", func(c *gin.Context) { s.getRecords(c, "nearest") })
			stats.GET("/motion/furthest", func(c *gin.Context) { s.getRecords(c, "furthest_range") })
			stats.GET("/motion/recent", s.getRecentObservations)
```

- [ ] **Step 2: Build**

Run: `cd core && go build -o /tmp/skystats-build-check`
Expected: exits 0

- [ ] **Step 3: Run the full Go test suite**

Run: `cd core && go test ./...`
Expected: PASS

- [ ] **Step 4: Manual verification note**

Verified by running the stack: `curl http://localhost:8080/api/stats/motion/nearest` and `curl http://localhost:8080/api/stats/motion/furthest` both return `200` with a JSON array (empty `[]` is fine before any flight has finalized).

- [ ] **Step 5: Commit**

```bash
git add core/api.go
git commit -m "feat: add nearest/furthest API routes"
```

---

### Task 8: Frontend leaderboard cards

**Files:**
- Create: `web/src/components/MotionNearestAircraft.svelte`
- Create: `web/src/components/MotionFurthestRangeAircraft.svelte`
- Modify: `web/src/lib/dashboardCards.js`

**Interfaces:**
- Consumes: `GET /api/stats/motion/nearest` (fields: `hex`, `registration`, `type`, `min_distance_receiver`, `min_distance_receiver_altitude`, `min_distance_receiver_bearing`, `first_seen`), `GET /api/stats/motion/furthest` (same shape, `max_distance_receiver*`) — Task 7. Existing `MotionStats.svelte` component (`endpoint`/`title`/`columns`/`icon` props).
- Produces: dashboard cards registered under `tab: 'motion-stat'` with ids `motion_nearest`/`motion_furthest`.

- [ ] **Step 1: Create the Nearest card**

Create `web/src/components/MotionNearestAircraft.svelte`:

```svelte
<script>
    import MotionStats from './MotionStats.svelte';
    import { IconTarget } from '@tabler/icons-svelte';

    const columns = [
        { header: 'Reg', field: 'registration', class: 'font-mono whitespace-nowrap' },
        { header: 'Type', field: 'type' },
        {
            header: 'Distance',
            field: 'min_distance_receiver',
            formatter: (value) => value != null ? `${Math.round(value).toLocaleString()} km` : '-'
        },
        {
            header: 'Altitude',
            field: 'min_distance_receiver_altitude',
            formatter: (value) => value != null ? `${Math.round(value).toLocaleString()} ft` : '-'
        },
        {
            header: 'Bearing',
            field: 'min_distance_receiver_bearing',
            formatter: (value) => value != null ? `${Math.round(value)}°` : '-'
        },
        {
            header: 'First Seen',
            field: 'first_seen',
            class: 'whitespace-nowrap',
            formatter: (value) => value ? new Date(value).toLocaleString() : '-'
        }
    ];
</script>

<MotionStats
    endpoint="api/stats/motion/nearest"
    title="Nearest"
    {columns}
    icon={IconTarget}
/>
```

(`value != null` rather than the truthy `value ?` used by sibling km-distance columns: `0°` bearing and `0 ft` altitude are legitimate values, not "missing data", so a truthy check would wrongly render them as `-`.)

- [ ] **Step 2: Create the Furthest card**

Create `web/src/components/MotionFurthestRangeAircraft.svelte`:

```svelte
<script>
    import MotionStats from './MotionStats.svelte';
    import { IconTelescope } from '@tabler/icons-svelte';

    const columns = [
        { header: 'Reg', field: 'registration', class: 'font-mono whitespace-nowrap' },
        { header: 'Type', field: 'type' },
        {
            header: 'Distance',
            field: 'max_distance_receiver',
            formatter: (value) => value != null ? `${Math.round(value).toLocaleString()} km` : '-'
        },
        {
            header: 'Altitude',
            field: 'max_distance_receiver_altitude',
            formatter: (value) => value != null ? `${Math.round(value).toLocaleString()} ft` : '-'
        },
        {
            header: 'Bearing',
            field: 'max_distance_receiver_bearing',
            formatter: (value) => value != null ? `${Math.round(value)}°` : '-'
        },
        {
            header: 'First Seen',
            field: 'first_seen',
            class: 'whitespace-nowrap',
            formatter: (value) => value ? new Date(value).toLocaleString() : '-'
        }
    ];
</script>

<MotionStats
    endpoint="api/stats/motion/furthest"
    title="Furthest"
    {columns}
    icon={IconTelescope}
/>
```

- [ ] **Step 3: Register both cards**

In `web/src/lib/dashboardCards.js`, add imports after the existing `MotionLongestRouteAircraft` import (line 29):

```js
import MotionLongestRouteAircraft from '../components/MotionLongestRouteAircraft.svelte';
import MotionNearestAircraft from '../components/MotionNearestAircraft.svelte';
import MotionFurthestRangeAircraft from '../components/MotionFurthestRangeAircraft.svelte';
```

And add card entries after the `motion_longest_route` entry (line 64):

```js
    { id: 'motion_longest_route', title: 'Longest Route', tab: 'motion-stat', component: MotionLongestRouteAircraft },
    { id: 'motion_nearest', title: 'Nearest', tab: 'motion-stat', component: MotionNearestAircraft },
    { id: 'motion_furthest', title: 'Furthest', tab: 'motion-stat', component: MotionFurthestRangeAircraft },
```

- [ ] **Step 4: Verify the frontend builds**

Run: `cd web && npm run build`
Expected: exits 0, no import or syntax errors

- [ ] **Step 5: Manual verification note**

Verified by running `npm run dev -- --host` against a backend with migration 000018 applied: the Record Holders tab shows "Nearest" and "Furthest" cards (empty state "No data available" is correct until a flight finalizes), and both are toggleable in Settings → Cards like every other card.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/MotionNearestAircraft.svelte web/src/components/MotionFurthestRangeAircraft.svelte web/src/lib/dashboardCards.js
git commit -m "feat: add Nearest/Furthest dashboard cards"
```

---

### Task 9: Settings — Danger Zone and notification toggles

**Files:**
- Modify: `web/src/components/Settings.svelte`

**Interfaces:**
- Consumes: `notify_record_nearest`/`notify_record_furthest_range` settings (Task 1); `DELETE /api/records` body `{categories: [...]}` (existing, generic — no backend change needed since `validateRecordCategories` reads the same `recordCategories` map extended in Task 4).
- Produces: two new Danger Zone checkboxes wired to the existing generic clear flow; two new notification toggles wired to the existing generic save flow.

- [ ] **Step 1: Add notification toggle state variables**

In `web/src/components/Settings.svelte`, change line 43:

```js
    let notifyRecordFurthestFlown = true, notifyRecordLongestRoute = true, notifyRecordMostRemaining = true;
```

to:

```js
    let notifyRecordFurthestFlown = true, notifyRecordLongestRoute = true, notifyRecordMostRemaining = true;
    let notifyRecordNearest = true, notifyRecordFurthestRange = true;
```

- [ ] **Step 2: Add Danger Zone category entries**

Change the `recordCategories` array (lines 58-66):

```js
    const recordCategories = [
        { key: 'fastest', label: 'Fastest' },
        { key: 'slowest', label: 'Slowest' },
        { key: 'highest', label: 'Highest' },
        { key: 'lowest', label: 'Lowest' },
        { key: 'furthest_flown', label: 'Furthest flown' },
        { key: 'longest_route', label: 'Longest route' },
        { key: 'most_remaining', label: 'Most remaining' }
    ];
```

to:

```js
    const recordCategories = [
        { key: 'fastest', label: 'Fastest' },
        { key: 'slowest', label: 'Slowest' },
        { key: 'highest', label: 'Highest' },
        { key: 'lowest', label: 'Lowest' },
        { key: 'furthest_flown', label: 'Furthest flown' },
        { key: 'longest_route', label: 'Longest route' },
        { key: 'most_remaining', label: 'Most remaining' },
        { key: 'nearest', label: 'Nearest' },
        { key: 'furthest_range', label: 'Furthest' }
    ];
```

(No template change needed here — the Danger Zone list at lines ~470-482 already renders via `{#each recordCategories as category}`.)

- [ ] **Step 3: Load the new settings**

Change line 149 area (after the existing `notify_record_most_remaining` load line):

```js
        if ($settings.notify_record_most_remaining) notifyRecordMostRemaining = $settings.notify_record_most_remaining.setting_value === 'true';
```

to:

```js
        if ($settings.notify_record_most_remaining) notifyRecordMostRemaining = $settings.notify_record_most_remaining.setting_value === 'true';
        if ($settings.notify_record_nearest) notifyRecordNearest = $settings.notify_record_nearest.setting_value === 'true';
        if ($settings.notify_record_furthest_range) notifyRecordFurthestRange = $settings.notify_record_furthest_range.setting_value === 'true';
```

- [ ] **Step 4: Save the new settings**

Change the `updates` object in `saveNotificationSettings` (currently ending at `notify_record_most_remaining: notifyRecordMostRemaining.toString()`):

```js
            notify_record_furthest_flown: notifyRecordFurthestFlown.toString(),
            notify_record_longest_route: notifyRecordLongestRoute.toString(),
            notify_record_most_remaining: notifyRecordMostRemaining.toString()
        };
```

to:

```js
            notify_record_furthest_flown: notifyRecordFurthestFlown.toString(),
            notify_record_longest_route: notifyRecordLongestRoute.toString(),
            notify_record_most_remaining: notifyRecordMostRemaining.toString(),
            notify_record_nearest: notifyRecordNearest.toString(),
            notify_record_furthest_range: notifyRecordFurthestRange.toString()
        };
```

- [ ] **Step 5: Add the two checkboxes**

In the "All-time records" grid (currently ending with the `Most remaining` label), change:

```svelte
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyRecordMostRemaining} on:change={handleNotificationChange} /><span class="text-sm">Most remaining</span></label>
                                </div>
                            </div>
```

to:

```svelte
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyRecordMostRemaining} on:change={handleNotificationChange} /><span class="text-sm">Most remaining</span></label>
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyRecordNearest} on:change={handleNotificationChange} /><span class="text-sm">Nearest</span></label>
                                    <label class="flex items-center gap-2"><input type="checkbox" class="checkbox checkbox-sm" bind:checked={notifyRecordFurthestRange} on:change={handleNotificationChange} /><span class="text-sm">Furthest</span></label>
                                </div>
                            </div>
```

- [ ] **Step 6: Verify the frontend builds**

Run: `cd web && npm run build`
Expected: exits 0

- [ ] **Step 7: Manual verification note**

Verified by running the stack: Settings → Notifications shows "Nearest"/"Furthest" checkboxes that persist across reload; Settings → Danger Zone shows "Nearest"/"Furthest" in the clear-records list, and selecting + clearing one empties only that leaderboard (confirm via `GET /api/stats/motion/nearest` returning `[]` afterward while the other stays populated).

- [ ] **Step 8: Commit**

```bash
git add web/src/components/Settings.svelte
git commit -m "feat: add nearest/furthest to Settings notifications and Danger Zone"
```

---

## Post-plan verification (run once all tasks are done)

- [ ] `cd core && go build -o skystats-daemon && go test ./...` — full backend build + test pass
- [ ] `cd web && npm run build` — full frontend build pass
- [ ] Against a real Postgres with migration 000018 applied: run the daemon, track a real aircraft through a full pass (arrival to 10+ minutes of silence), confirm a `nearest`/`furthest_range` row appears in `records` with a plausible `metric_value`, non-null altitude/bearing details, and that it shows up in the UI cards and is clearable independently via Settings → Danger Zone.
