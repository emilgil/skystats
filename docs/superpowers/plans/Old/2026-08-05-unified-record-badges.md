# Unified Record Badges Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every aircraft detail card always shows all 7 record badges its data supports, each showing that aircraft's own true best/worst reading (never a stale leaderboard snapshot), grouped Speed/Altitude/Distance, and colored/labeled distinctly when the value also currently places in the fleet-wide top-100.

**Architecture:** Backend (`core/aircraft-detail.go`) replaces its `records` leaderboard-table read with a fresh `MAX`/`MIN` aggregate over this hex's full `flight_history`, plus a cheap membership check against `records` for the color/label decision. A new pure helper assembles the 7-category list so the logic is unit-testable without a database. Frontend (`AircraftModal.svelte`) groups the returned records into Speed/Altitude/Distance sections with a divider, and renders an accent+trophy badge when `is_global_record` is true, otherwise the existing neutral badge style.

**Tech Stack:** Go (Gin, pgx) backend; Svelte 5 + Tailwind/DaisyUI + `@tabler/icons-svelte` frontend. No new dependencies, no migration.

## Global Constraints

- No DB schema change — reuse `flight_history` (per-session archive) and `records` (fleet-wide top-100 leaderboard) exactly as they exist today.
- `core/records-meta.go`'s `recordCategories` map stays the single source of truth for each category's `MetricName` — don't hardcode a second copy of that mapping.
- Go tests use stdlib `testing`, table-driven style, no assertion library — match `core/records-meta_test.go` / `core/recent-observations_test.go`.
- Frontend has no test harness (per `CLAUDE.md`) — verify with `npm run build` plus a manual browser check, not an automated test.
- No hardcoded hex colors — reuse the existing `badge-accent text-white` treatment already used for "highlighted" chips elsewhere (`CurrentSightings.svelte`, `AboveTimeline.svelte`, this same file's `interesting.tags` badges).
- `@tabler/icons-svelte` is already installed; `IconTrophy` exists in the package — no new npm dependency.
- Combine the value/color redesign and the group/divider layout in one pass (both specs say to implement together), so there is exactly one visual state to verify, not two.

---

## File Structure

- **Modify `core/aircraft-detail.go`**: add `personalBestRecord` type + `buildPersonalBestRecords` pure function (no I/O); rewrite the handler's step 5 to query `flight_history` aggregates and `records` membership instead of reading `records` directly; drop the now-unused `sort` import.
- **Create `core/aircraft-detail_test.go`**: table-driven tests for `buildPersonalBestRecords`.
- **Modify `web/src/components/AircraftModal.svelte`**: import `IconTrophy`; add a `BADGE_GROUPS` constant and a `groupedBadges` reactive value; replace the badge-row markup to render grouped, divided, color-coded badges.

---

### Task 1: Backend — fresh personal-best computation + global-record membership

**Files:**
- Modify: `core/aircraft-detail.go:3-12` (imports), `core/aircraft-detail.go:170-210` (step 5 block)
- Create: `core/aircraft-detail_test.go`

**Interfaces:**
- Produces: `type personalBestRecord struct { Category string; MetricName string; Value float64; IsGlobalRecord bool }` and `func buildPersonalBestRecords(maxGs, minGs, maxAlt, minAlt, maxRouteDist, maxDistFlown, maxDistRemaining *float64, globalRecordCategories map[string]bool) []personalBestRecord` — pure, no I/O. Fixed output order: `fastest, slowest, highest, lowest, longest_route, furthest_flown, most_remaining`, skipping any category whose input pointer is `nil`.
- Consumes: `recordCategories` map from `core/records-meta.go` (already in the package) for `MetricName` lookup.
- API response shape change: each entry in the JSON `records` array gains `"is_global_record": bool` alongside the existing `category`/`metric_name`/`value` fields. Task 2's frontend work consumes this new field.

- [ ] **Step 1: Write the failing tests**

Create `core/aircraft-detail_test.go`:

```go
package main

import "testing"

func TestBuildPersonalBestRecordsAllNil(t *testing.T) {
	got := buildPersonalBestRecords(nil, nil, nil, nil, nil, nil, nil, map[string]bool{})
	if len(got) != 0 {
		t.Errorf("all nil: got %d records, want 0", len(got))
	}
}

func TestBuildPersonalBestRecordsPartial(t *testing.T) {
	gs := 390.0
	got := buildPersonalBestRecords(&gs, nil, nil, nil, nil, nil, nil, map[string]bool{})
	if len(got) != 1 {
		t.Fatalf("partial: got %d records, want 1: %+v", len(got), got)
	}
	want := personalBestRecord{Category: "fastest", MetricName: "ground_speed", Value: 390.0, IsGlobalRecord: false}
	if got[0] != want {
		t.Errorf("partial: got %+v want %+v", got[0], want)
	}
}

func TestBuildPersonalBestRecordsFullSetOrderAndMembership(t *testing.T) {
	maxGs, minGs := 479.0, 210.0
	maxAlt, minAlt := 37000.0, 19000.0
	maxRouteDist, maxDistFlown, maxDistRemaining := 3016.0, 2615.0, 409.0
	global := map[string]bool{"fastest": true, "longest_route": true}

	got := buildPersonalBestRecords(&maxGs, &minGs, &maxAlt, &minAlt, &maxRouteDist, &maxDistFlown, &maxDistRemaining, global)

	want := []personalBestRecord{
		{Category: "fastest", MetricName: "ground_speed", Value: 479.0, IsGlobalRecord: true},
		{Category: "slowest", MetricName: "ground_speed", Value: 210.0, IsGlobalRecord: false},
		{Category: "highest", MetricName: "barometric_altitude", Value: 37000.0, IsGlobalRecord: false},
		{Category: "lowest", MetricName: "barometric_altitude", Value: 19000.0, IsGlobalRecord: false},
		{Category: "longest_route", MetricName: "route_distance", Value: 3016.0, IsGlobalRecord: true},
		{Category: "furthest_flown", MetricName: "distance_flown", Value: 2615.0, IsGlobalRecord: false},
		{Category: "most_remaining", MetricName: "distance_remaining", Value: 409.0, IsGlobalRecord: false},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d records, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("record %d: got %+v want %+v", i, got[i], want[i])
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd core && go test ./... -run TestBuildPersonalBestRecords -v`
Expected: build failure — `undefined: personalBestRecord` / `undefined: buildPersonalBestRecords`.

- [ ] **Step 3: Implement**

In `core/aircraft-detail.go`, remove `"sort"` from the import block (lines 3-12), since it is only used by the code being replaced below:

```go
import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)
```

Immediately after that import block (before the `getAircraftDetail` doc comment), add:

```go
// personalBestRecord is one badge on the aircraft detail card: this
// aircraft's own best/worst reading for one metric, and whether that same
// category also currently places in the fleet-wide top-100 records table.
type personalBestRecord struct {
	Category       string
	MetricName     string
	Value          float64
	IsGlobalRecord bool
}

// buildPersonalBestRecords assembles the badge list for one aircraft from its
// freshly-aggregated personal-best values (nil = no qualifying observation
// for that metric, so the category is omitted entirely) and the set of
// categories where this hex currently holds a fleet-wide record. Order is
// fixed (fastest, slowest, highest, lowest, longest_route, furthest_flown,
// most_remaining) to match the Speed/Altitude/Distance grouping the
// frontend renders.
func buildPersonalBestRecords(maxGs, minGs, maxAlt, minAlt, maxRouteDist, maxDistFlown, maxDistRemaining *float64, globalRecordCategories map[string]bool) []personalBestRecord {
	candidates := []struct {
		category string
		value    *float64
	}{
		{"fastest", maxGs},
		{"slowest", minGs},
		{"highest", maxAlt},
		{"lowest", minAlt},
		{"longest_route", maxRouteDist},
		{"furthest_flown", maxDistFlown},
		{"most_remaining", maxDistRemaining},
	}
	out := []personalBestRecord{}
	for _, cand := range candidates {
		if cand.value == nil {
			continue
		}
		out = append(out, personalBestRecord{
			Category:       cand.category,
			MetricName:     recordCategories[cand.category].MetricName,
			Value:          *cand.value,
			IsGlobalRecord: globalRecordCategories[cand.category],
		})
	}
	return out
}
```

Then replace the existing step 5 block (currently `core/aircraft-detail.go:170-210`, from `// 5) Records this aircraft holds...` through `resp["records"] = records`) with:

```go
	// 5) Personal-best per category, computed fresh from this hex's full
	// flight_history rather than the trimmed/windowed records leaderboard
	// snapshot (which freezes at whatever value existed the one time a
	// session row was processed — see
	// docs/superpowers/specs/2026-08-03-unified-record-badges-spec.md).
	var maxGs, minGs, maxAlt, minAlt, maxRouteDist, maxDistFlown, maxDistRemaining *float64
	err = s.pg.db.QueryRow(context.Background(), `
		SELECT MAX(ground_speed)::float8, MIN(ground_speed)::float8,
		       MAX(barometric_altitude)::float8, MIN(barometric_altitude)::float8,
		       MAX(route_distance)::float8, MAX(distance_flown)::float8, MAX(distance_remaining)::float8
		FROM flight_history
		WHERE hex = $1`, hex).
		Scan(&maxGs, &minGs, &maxAlt, &minAlt, &maxRouteDist, &maxDistFlown, &maxDistRemaining)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	globalRecordCategories := map[string]bool{}
	catRows, err := s.pg.db.Query(context.Background(), `SELECT DISTINCT category FROM records WHERE hex = $1`, hex)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for catRows.Next() {
		var category string
		if err := catRows.Scan(&category); err != nil {
			continue
		}
		globalRecordCategories[category] = true
	}
	catRows.Close()

	records := []gin.H{}
	for _, r := range buildPersonalBestRecords(maxGs, minGs, maxAlt, minAlt, maxRouteDist, maxDistFlown, maxDistRemaining, globalRecordCategories) {
		records = append(records, gin.H{
			"category":         r.Category,
			"metric_name":      r.MetricName,
			"value":            r.Value,
			"is_global_record": r.IsGlobalRecord,
		})
	}
	resp["records"] = records
```

- [ ] **Step 4: Run tests, vet, and build to verify everything passes**

Run: `cd core && go test ./... -v && go vet ./... && go build -o /dev/null .`
Expected: all tests `PASS`, `go vet` silent, build succeeds (confirms the `sort` import removal didn't break anything else and no other unused-import/type errors exist).

- [ ] **Step 5: Commit**

```bash
git add core/aircraft-detail.go core/aircraft-detail_test.go
git commit -m "feat: compute aircraft badge values fresh from flight_history, flag fleet-wide records"
```

---

### Task 2: Frontend — grouped, color-coded badge row

**Files:**
- Modify: `web/src/components/AircraftModal.svelte:1-4` (imports), `:15-23` (add constant after `RECORD_LABELS`), `:13` area (add reactive statement), `:142-150` (badge markup)

**Interfaces:**
- Consumes: `data.records[]` from `GET /api/stats/aircraft/:hex` (Task 1's new shape), each item `{ category, metric_name, value, is_global_record }`.

- [ ] **Step 1: Add the `IconTrophy` import**

In `web/src/components/AircraftModal.svelte`, change the import block (currently lines 1-4):

```svelte
<script>
// @ts-nocheck
    import { selectedHex, closeAircraftModal } from '../stores/aircraftModal';
    import { settings } from '../stores/settings';
    import { IconTrophy } from '@tabler/icons-svelte';
```

- [ ] **Step 2: Add the group definition and reactive grouping**

Immediately after the existing `RECORD_LABELS` constant (currently lines 15-23), add:

```js
    const BADGE_GROUPS = [
        { categories: ['fastest', 'slowest'] },
        { categories: ['highest', 'lowest'] },
        { categories: ['longest_route', 'furthest_flown', 'most_remaining'] },
    ];
```

Then, alongside the existing `$: disableTags = ...` reactive statement (currently line 13), add:

```js
    $: groupedBadges = data?.records
        ? BADGE_GROUPS.map((group) => ({
              records: group.categories
                  .map((cat) => data.records.find((r) => r.category === cat))
                  .filter(Boolean),
          })).filter((group) => group.records.length > 0)
        : [];
```

- [ ] **Step 3: Replace the badge-row markup**

Replace the current block (lines 142-150):

```svelte
            {#if data.records?.length}
                <div class="flex flex-wrap gap-2 mb-4">
                    {#each data.records as rec}
                        <div class="badge badge-primary badge-outline gap-1">
                            {RECORD_LABELS[rec.category]?.label ?? rec.category} — {Math.round(rec.value)} {RECORD_LABELS[rec.category]?.unit ?? ''}
                        </div>
                    {/each}
                </div>
            {/if}
```

with:

```svelte
            {#if groupedBadges.length}
                <div class="flex flex-wrap items-center gap-2 mb-4">
                    {#each groupedBadges as group, i}
                        {#if i > 0}
                            <div class="w-px self-stretch bg-base-300" aria-hidden="true"></div>
                        {/if}
                        {#each group.records as rec}
                            {#if rec.is_global_record}
                                <div class="badge badge-accent text-white gap-1">
                                    <IconTrophy size={12} />
                                    {RECORD_LABELS[rec.category]?.label ?? rec.category} — {Math.round(rec.value)} {RECORD_LABELS[rec.category]?.unit ?? ''}
                                </div>
                            {:else}
                                <div class="badge badge-primary badge-outline gap-1">
                                    {RECORD_LABELS[rec.category]?.label ?? rec.category} — {Math.round(rec.value)} {RECORD_LABELS[rec.category]?.unit ?? ''}
                                </div>
                            {/if}
                        {/each}
                    {/each}
                </div>
            {/if}
```

- [ ] **Step 4: Build to verify no syntax/compile errors**

Run: `cd web && npm run build`
Expected: build succeeds with no errors (this file uses `@ts-nocheck`, so this checks Svelte compilation, not types).

- [ ] **Step 5: Manual browser check**

Run: `cd web && npm run dev -- --host`, open the dashboard, click an aircraft in the Above Me timeline.
Check:
- All applicable categories show (not just leaderboard members) — confirms against Task 1's backend change.
- Badges appear in three visual groups (Speed, Altitude, Distance) in that order, with a thin divider between non-empty groups.
- If the divider looks broken when chips wrap onto a second line, adjust (e.g. add `flex-basis` or move the divider inside a per-group wrapper) until it reads cleanly on both a wide and a narrow (mobile-width) viewport.
- Any badge for a category this aircraft currently holds fleet-wide shows the accent color + trophy icon; all others show the existing neutral outline style.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/AircraftModal.svelte
git commit -m "feat: group aircraft badges by type, mark fleet-wide records distinctly"
```

---

### Task 3: Live-data verification against real Postgres

This project has no local Postgres — verifying real values requires either a local dev stack with real data, or the non-destructive scratch-database procedure previously used on 192.168.1.251 (throwaway DB inside the existing `skystats-db` container, host-run binary, read-only queries — see the project's `verify-scratch-db-251` notes for exact commands). Touching 192.168.1.251, even non-destructively, is a shared resource — confirm with the user before doing this step.

- [ ] **Step 1: Confirm with the user before touching 192.168.1.251**

Ask whether to proceed with the scratch-DB verification on 251 now, or defer it.

- [ ] **Step 2: Find two real aircraft to check**

Query the scratch (or otherwise available) DB for:
- An aircraft with modest performance that has never placed in any fleet leaderboard, e.g.:
  ```sql
  SELECT hex, MAX(ground_speed), MIN(ground_speed), MAX(barometric_altitude), MIN(barometric_altitude)
  FROM flight_history GROUP BY hex
  HAVING NOT EXISTS (SELECT 1 FROM records r WHERE r.hex = flight_history.hex)
  LIMIT 1;
  ```
- An aircraft with at least one row in `records` (a real fleet-wide record holder):
  ```sql
  SELECT DISTINCT hex, category FROM records LIMIT 5;
  ```

- [ ] **Step 3: Confirm the API output matches**

`curl localhost:<port>/api/stats/aircraft/<hex>` for each and confirm:
- The modest aircraft's badges all show `is_global_record: false`, and each `value` matches the hand-computed `MAX`/`MIN` from Step 2's query.
- The record-holder aircraft shows `is_global_record: true` for the category found in `records`, and `false` for its other badges.

- [ ] **Step 4: Clean up**

Follow the scratch-DB teardown (kill the scratch process by resolving `/proc/<pid>/exe`, `DROP DATABASE`, `rm -rf` the scratch directory) — never `pkill -f skystats-daemon` on 251, it can match the production daemon.

No commit — this task only verifies Tasks 1-2's already-committed work.
