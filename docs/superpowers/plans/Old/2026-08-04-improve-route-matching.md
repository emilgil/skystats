# Improve Route Matching Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop permanently giving up on a flight's origin/destination lookup after a single failed attempt, so genuinely-resolvable routes get a few retries instead of showing "-" forever.

**Architecture:** `updateRoutes()` in `core/routes.go` currently marks every aircraft it sends to the adsbdb routeset API as `route_processed = true`, whether or not a route was actually matched — that's the bug (confirmed by reading the code: `existing = append(existing, new...)` runs unconditionally before `MarkProcessed`). The fix adds a bounded per-aircraft attempt counter (`route_attempts`, new column) so a failed match only gives up permanently after 5 tries; a pure `classifyRouteAttempt` function decides matched/retry/exhausted given a match result and the attempt count so far, kept free of DB/HTTP for unit testing per this repo's usual pattern (see `core/haversine_test.go`, `core/watches-match_test.go`).

**Tech Stack:** Go (`core/`), PostgreSQL via pgx, golang-migrate SQL files in `migrations/`.

## Global Constraints

- Do not touch `route_distance` / `destination_distance` (cheap-ruler) calculations — explicitly out of scope per the spec.
- Do not change how "-" is rendered in the UI when data is genuinely missing — separate, later concern per the spec.
- Commit task-by-task (one deliverable per commit), per the spec's definition of done.
- No regressions in Top Routes, Above Me, or the four/seven record-holder tables — all of these read `route_data` / `route_processed` the same way they do today; this plan only changes *when* a row becomes permanently processed, not the shape of any table.
- This repo has no local Postgres and no test harness for the DB/HTTP layer (confirmed: no `.env`, no local `psql`, nothing listening on 5432) — verification for DB-touching tasks is `go build ./...` plus the final deploy-and-observe task, consistent with `CLAUDE.md`'s testing note. Only the pure `classifyRouteAttempt` logic gets a real unit test.

## Baseline (measured 2026-08-04 against the 192.168.1.251 production DB)

```
flights_total | flights_processed | flights_pending | flights_matched | match_pct_of_processed
2123          | 2122               | 1                | 1896             | 89.3
```

226 flights (10.7% of processed ones) are permanently stuck unmatched under today's logic. This is the "before" number Task 7 compares against.

---

### Task 1: Migration — add `route_attempts` column

**Files:**
- Create: `migrations/000017_add_route_attempts.up.sql`
- Create: `migrations/000017_add_route_attempts.down.sql`

**Interfaces:**
- Produces: column `aircraft_data.route_attempts SMALLINT NOT NULL DEFAULT 0`, consumed by Task 2 (read) and Task 3 (write).

- [ ] **Step 1: Write the up migration**

```sql
-- migrations/000017_add_route_attempts.up.sql
ALTER TABLE aircraft_data ADD COLUMN route_attempts SMALLINT NOT NULL DEFAULT 0;
```

- [ ] **Step 2: Write the down migration**

```sql
-- migrations/000017_add_route_attempts.down.sql
ALTER TABLE aircraft_data DROP COLUMN IF EXISTS route_attempts;
```

- [ ] **Step 3: Verify against sibling migrations**

Compare against `migrations/000010_add_distance_stats.up.sql` (same `ALTER TABLE ... ADD COLUMN ... DEFAULT` pattern) to confirm style/naming match. There is no local Postgres to apply this against; it will be applied automatically by `RunDatabaseMigrations()` when the daemon starts against the real DB in Task 7.

- [ ] **Step 4: Commit**

```bash
git add migrations/000017_add_route_attempts.up.sql migrations/000017_add_route_attempts.down.sql
git commit -m "feat: add route_attempts column for bounded route-lookup retries"
```

---

### Task 2: Track `route_attempts` on the `Aircraft` model and read it back

**Files:**
- Modify: `core/models.go` (Aircraft struct, ~line 62)
- Modify: `core/routes.go` (`unprocessedRoutes`, lines 49-91)

**Interfaces:**
- Consumes: `aircraft_data.route_attempts` column from Task 1.
- Produces: `Aircraft.RouteAttempts int`, consumed by Task 4's `classifyRouteAttempt` and Task 6's `updateRoutes`.

- [ ] **Step 1: Add the field to the Aircraft struct**

In `core/models.go`, add `RouteAttempts` next to the other route-lookup bookkeeping fields:

```go
	LastSeenLat         sql.NullFloat64
	LastSeenLon         sql.NullFloat64
	LastSeenDistance    sql.NullFloat64
	DestinationDistance sql.NullFloat64
	RouteAttempts       int
	LowestProcessed     bool
```

- [ ] **Step 2: Select and scan the new column in `unprocessedRoutes`**

In `core/routes.go`, update the query and scan:

```go
func unprocessedRoutes(pg *postgres) []Aircraft {

	query := `
		SELECT id, flight, last_seen_lat, last_seen_lon, route_attempts
		FROM aircraft_data
		WHERE 
			hex != '' AND
			flight != '' AND
			route_processed = false
		ORDER BY first_seen ASC`

	rows, err := pg.db.Query(context.Background(), query)

	if err != nil {
		log.Error().Err(err).Msg("unprocessedRoutes() - Error querying db")
		return nil
	}
	defer rows.Close()

	var aircrafts []Aircraft

	for rows.Next() {

		var aircraft Aircraft

		err := rows.Scan(
			&aircraft.Id,
			&aircraft.Flight,
			&aircraft.LastSeenLat,
			&aircraft.LastSeenLon,
			&aircraft.RouteAttempts,
		)

		if err != nil {
			log.Error().Err(err).Msg("unprocessedRoutes() - Error scanning rows")
			return nil
		}

		aircrafts = append(aircrafts, aircraft)
	}

	log.Debug().Msgf("Aircrafts that have not have routes processed: %d", len(aircrafts))
	return aircrafts
}
```

- [ ] **Step 3: Verify it builds**

Run: `cd core && go build ./...`
Expected: exits 0, no errors.

- [ ] **Step 4: Commit**

```bash
git add core/models.go core/routes.go
git commit -m "feat: read route_attempts alongside unprocessed routes"
```

---

### Task 3: Add `IncrementRouteAttempts`

**Files:**
- Modify: `core/db-utils.go`

**Interfaces:**
- Consumes: `Aircraft.Id` (existing field).
- Produces: `func IncrementRouteAttempts(pg *postgres, aircrafts []Aircraft)`, consumed by Task 6.

- [ ] **Step 1: Add the function, mirroring `MarkProcessed`**

```go
func IncrementRouteAttempts(pg *postgres, aircrafts []Aircraft) {

	batch := &pgx.Batch{}

	for _, aircraft := range aircrafts {
		batch.Queue(`UPDATE aircraft_data SET route_attempts = route_attempts + 1 WHERE id = $1`, aircraft.Id)
	}

	br := pg.db.SendBatch(context.Background(), batch)
	defer br.Close()

	for i := 0; i < len(aircrafts); i++ {
		_, err := br.Exec()
		if err != nil {
			log.Error().Err(err).Msg("IncrementRouteAttempts() - Unable to update data")
		}
	}
}
```

Full resulting file should have both `MarkProcessed` and `IncrementRouteAttempts` as siblings in `core/db-utils.go`.

- [ ] **Step 2: Verify it builds**

Run: `cd core && go build ./...`
Expected: exits 0, no errors.

- [ ] **Step 3: Commit**

```bash
git add core/db-utils.go
git commit -m "feat: add IncrementRouteAttempts helper"
```

---

### Task 4: Pure retry-classification logic (TDD)

**Files:**
- Modify: `core/routes.go` (add near the top, before `updateRoutes`)
- Create: `core/routes_test.go`

**Interfaces:**
- Consumes: `Aircraft.RouteAttempts` (Task 2).
- Produces: `const maxRouteAttempts`, `type routeOutcome`, consts `routeMatched`/`routeRetry`/`routeExhausted`, and `func classifyRouteAttempt(matched bool, attemptsSoFar int) routeOutcome` — consumed by Task 6's `updateRoutes`.

- [ ] **Step 1: Write the failing tests**

```go
// core/routes_test.go
package main

import "testing"

func TestClassifyRouteAttempt_Matched(t *testing.T) {
	outcome := classifyRouteAttempt(true, 0)
	if outcome != routeMatched {
		t.Errorf("expected routeMatched, got %v", outcome)
	}
}

func TestClassifyRouteAttempt_RetryUnderCap(t *testing.T) {
	outcome := classifyRouteAttempt(false, 0)
	if outcome != routeRetry {
		t.Errorf("expected routeRetry on first miss, got %v", outcome)
	}

	outcome = classifyRouteAttempt(false, maxRouteAttempts-2)
	if outcome != routeRetry {
		t.Errorf("expected routeRetry with one attempt left, got %v", outcome)
	}
}

func TestClassifyRouteAttempt_ExhaustedAtCap(t *testing.T) {
	outcome := classifyRouteAttempt(false, maxRouteAttempts-1)
	if outcome != routeExhausted {
		t.Errorf("expected routeExhausted on the %dth miss, got %v", maxRouteAttempts, outcome)
	}
}

func TestClassifyRouteAttempt_ExhaustedPastCap(t *testing.T) {
	outcome := classifyRouteAttempt(false, maxRouteAttempts+10)
	if outcome != routeExhausted {
		t.Errorf("expected routeExhausted past the cap, got %v", outcome)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd core && go test ./... -run TestClassifyRouteAttempt -v`
Expected: FAIL — `classifyRouteAttempt`, `maxRouteAttempts`, `routeMatched`, `routeRetry`, `routeExhausted` undefined.

- [ ] **Step 3: Implement**

Add to `core/routes.go`, above `updateRoutes`:

```go
const maxRouteAttempts = 5

type routeOutcome int

const (
	routeMatched routeOutcome = iota
	routeRetry
	routeExhausted
)

// classifyRouteAttempt decides what to do with a single aircraft after one
// adsbdb lookup round: keep the match, try again next tick, or give up for
// good once maxRouteAttempts lookups have all failed.
func classifyRouteAttempt(matched bool, attemptsSoFar int) routeOutcome {
	if matched {
		return routeMatched
	}
	if attemptsSoFar+1 >= maxRouteAttempts {
		return routeExhausted
	}
	return routeRetry
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd core && go test ./... -run TestClassifyRouteAttempt -v`
Expected: PASS (4/4).

- [ ] **Step 5: Commit**

```bash
git add core/routes.go core/routes_test.go
git commit -m "feat: add pure route-attempt classification with tests"
```

---

### Task 5: Refactor `insertRoutes` to report which callsigns matched, log why others were skipped

**Files:**
- Modify: `core/routes.go` (`insertRoutes`, lines 188-326)

**Interfaces:**
- Consumes: `[]RouteInfo` (unchanged, from `getRoutes`).
- Produces: `func insertRoutes(pg *postgres, routes []RouteInfo) map[string]bool` (keyed by `route.Callsign`, `true` for every callsign a row was actually written for) — consumed by Task 6.

- [ ] **Step 1: Change the signature and add DEBUG logging on each skip branch**

Replace the three `continue`-on-skip blocks near the top of the loop:

```go
	for _, route := range routes {

		// Skip callsigns that were not matched
		if route.AirportCodesIata == "unknown" {
			log.Debug().Str("callsign", route.Callsign).Msg("insertRoutes() - callsign not found by adsbdb")
			continue
		}

		// Skip any "unplausible" routes
		if route.Plausible == false {
			log.Debug().Str("callsign", route.Callsign).Msg("insertRoutes() - route marked implausible by adsbdb")
			continue
		}

		// Skip any empty or multihop routes - for now
		if route.Airports == nil || len(route.Airports) != 2 {
			log.Debug().Str("callsign", route.Callsign).Int("airport_count", len(route.Airports)).Msg("insertRoutes() - empty or multi-hop route")
			continue
		}
```

- [ ] **Step 2: Track queued callsigns instead of just a count**

Replace `queuedCount := 0` (near the top, alongside `countryLookup`) with:

```go
	var queuedCallsigns []string
```

Replace `queuedCount++` at the end of the per-route loop body with:

```go
		queuedCallsigns = append(queuedCallsigns, route.Callsign)
```

- [ ] **Step 3: Build the matched-callsigns map from the exec results and return it**

Replace the final exec loop:

```go
	br := pg.db.SendBatch(context.Background(), batch)
	defer br.Close()

	matched := make(map[string]bool, len(queuedCallsigns))
	for _, callsign := range queuedCallsigns {
		_, err := br.Exec()
		if err != nil {
			log.Error().Err(err).Str("callsign", callsign).Msg("insertRoutes() - Unable to insert data")
			continue
		}
		matched[callsign] = true
	}

	return matched
}
```

And update the function signature at the top:

```go
func insertRoutes(pg *postgres, routes []RouteInfo) map[string]bool {
```

- [ ] **Step 4: Verify it builds**

Run: `cd core && go build ./...`
Expected: exits 0. `updateRoutes` still calls `insertRoutes(pg, routes)` without capturing the new return value at this point — that's valid Go (an unused return value is not a compile error), so the build stays green until Task 6 wires the map into `updateRoutes`.

- [ ] **Step 5: Commit**

```bash
git add core/routes.go
git commit -m "feat: have insertRoutes report matched callsigns and log skip reasons"
```

---

### Task 6: Rewire `updateRoutes` to retry instead of permanently marking every attempt processed

**Files:**
- Modify: `core/routes.go` (`updateRoutes`, lines 22-47)

**Interfaces:**
- Consumes: `classifyRouteAttempt` (Task 4), `insertRoutes` returning `map[string]bool` (Task 5), `IncrementRouteAttempts` (Task 3), `Aircraft.RouteAttempts` (Task 2).
- Produces: corrected `route_processed` semantics — the behavior the whole plan exists to fix.

- [ ] **Step 1: Replace the function body**

```go
func updateRoutes(pg *postgres) {

	aircrafts := unprocessedRoutes(pg)

	if len(aircrafts) == 0 {
		return
	}

	if len(aircrafts) > 100 {
		aircrafts = aircrafts[:100]
	}

	existing, new := checkRouteExists(pg, aircrafts)

	routes, err := getRoutes(new)
	if err != nil {
		log.Error().Err(err).Msg("Error getting routes")
		return
	}

	matchedCallsigns := insertRoutes(pg, routes)

	var retry []Aircraft
	for _, a := range new {
		switch classifyRouteAttempt(matchedCallsigns[a.Flight], a.RouteAttempts) {
		case routeMatched, routeExhausted:
			existing = append(existing, a)
		case routeRetry:
			retry = append(retry, a)
		}
	}

	MarkProcessed(pg, "route_processed", existing)

	if len(retry) > 0 {
		IncrementRouteAttempts(pg, retry)
	}

}
```

- [ ] **Step 2: Verify it builds**

Run: `cd core && go build ./...`
Expected: exits 0, no errors, no unused-variable warnings.

- [ ] **Step 3: Run the full test suite**

Run: `cd core && go test ./...`
Expected: PASS, including the Task 4 tests and all pre-existing tests (`current-sightings_test.go`, `watches-*_test.go`, etc. — none of them touch routes, so this only proves no unrelated breakage).

- [ ] **Step 4: Commit**

```bash
git add core/routes.go
git commit -m "fix: retry failed route lookups up to 5 times before giving up"
```

---

### Task 7: Deploy and verify against production

**Files:** none (operational task)

**Interfaces:** none — this task validates Tasks 1-6 against real data per the spec's definition of done.

- [ ] **Step 1: Deploy to 192.168.1.251**

```bash
git archive --format=tar HEAD | ssh root@192.168.1.251 'tar -x -C /opt/skystats'
ssh root@192.168.1.251 'cd /opt/skystats && docker builder prune -f && docker compose up -d --build'
```

- [ ] **Step 2: Confirm the migration applied**

```bash
ssh root@192.168.1.251 "cd /opt/skystats && docker compose logs skystats --since 5m | grep -i migrat"
```

Expected: a line like `Successfully migrated database to version: 17`.

- [ ] **Step 3: Confirm retries are happening**

Temporarily bump `LOG_LEVEL=DEBUG` in the server's `.env`, restart, and watch for the new skip-reason log lines and repeated attempts on the same aircraft id across ticks:

```bash
ssh root@192.168.1.251 "cd /opt/skystats && docker compose logs -f skystats | grep -i 'insertRoutes()'"
```

Revert `LOG_LEVEL` back to `INFO` and restart once confirmed, per the existing convention noted in memory for this deployment.

- [ ] **Step 4: Re-run the baseline query and compare**

```bash
ssh root@192.168.1.251 "cd /opt/skystats && docker compose exec -T skystats-db psql -U skystats-user -d skystats_db -c \"
SELECT
  count(*) FILTER (WHERE flight <> '')                                        AS flights_total,
  count(*) FILTER (WHERE flight <> '' AND route_processed)                    AS flights_processed,
  count(*) FILTER (WHERE flight <> '' AND route_processed = false)            AS flights_pending,
  count(*) FILTER (WHERE flight <> '' AND route_processed
                    AND EXISTS (SELECT 1 FROM route_data rd WHERE rd.route_callsign = aircraft_data.flight))
                                                                                AS flights_matched,
  round(100.0 * count(*) FILTER (WHERE flight <> '' AND route_processed
                    AND EXISTS (SELECT 1 FROM route_data rd WHERE rd.route_callsign = aircraft_data.flight))
        / NULLIF(count(*) FILTER (WHERE flight <> '' AND route_processed), 0), 1) AS match_pct_of_processed
FROM aircraft_data WHERE first_seen > NOW() - INTERVAL '2 hours';
\""
```

Expected: `match_pct_of_processed` for recent flights (i.e. flights seen since the fix went live) measurably above the 89.3% baseline. Record the new number in this plan file's Baseline section for the historical record.

- [ ] **Step 5: Spot-check the UI**

Open `http://192.168.1.251:5173`, check Top Routes and (if merged) Furthest Flown/Most Remaining/Longest Route for fewer "-" rows and no regressions.

---

## Self-Review Notes

- **Spec coverage:** root cause (§1) → Task 6; retry instead of permanent write-off (§2) → Tasks 1-4, 6; implausible/multi-hop filter review (§3) → Task 5 adds DEBUG visibility rather than loosening the filter (my live probe against `adsb.im/api/0/routeset` showed the implausible filter correctly rejects a bogus match for an empty callsign, so loosening it blind would be a regression — logging first lets a future pass decide with real data); measure current rate (§4) → Baseline section + Task 7 Step 4; extra sources/heuristic (§5) → investigated and deliberately dropped: adsbdb/adsb.im's routeset API only accepts callsign, there is no registration-based route lookup to fall back to.
- **Out of scope respected:** no changes to `route_distance`/`destination_distance` math, no changes to how the frontend renders "-".
- **Type consistency:** `classifyRouteAttempt(matched bool, attemptsSoFar int) routeOutcome` (Task 4) is called exactly that way in Task 6; `insertRoutes(pg *postgres, routes []RouteInfo) map[string]bool` (Task 5) return value is consumed as `matchedCallsigns` in Task 6, keyed by the same `Flight`/`Callsign` string on both sides.
