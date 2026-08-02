# Recent Observations Card — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a global "Recent Observations" card to the Record Holders tab showing the N most recently observed flights (from `flight_history`), regardless of whether they hold any record, sorted newest-first and filtered by the tab's existing period selector.

**Architecture:** One new read-only Go handler (`getRecentObservations`) queries `flight_history` directly — not the `records` leaderboard table — filtered by a cutoff timestamp derived from the same `periodWindow()` helper the leaderboard sweep job already uses, `LEFT JOIN route_data` for `airline_name` exactly like `getRecords`. One new frontend card component reuses the existing `MotionStats.svelte` fetch/table machinery unchanged. No schema change, no migration, no new settings, no ingest changes.

**Tech Stack:** Go (gin + pgx v5), Svelte 5 + Tailwind + DaisyUI (Vite).

## Global Constraints

- **No schema change, no migration, no new settings.** Reuses `flight_history` (migration 000012), `route_data`, and the existing `record_holder_table_limit` setting.
- **Go version:** `1.25.3`. Already installed locally at `~/.local/go/bin/go` — `export PATH=~/.local/go/bin:$PATH` before running `go` commands. Verified working in this environment (`go build ./...` + `go vet ./...` both clean before this plan's changes).
- **No test framework for frontend or DB/HTTP code** (per `CLAUDE.md` and prior plans in this repo). Pure Go helpers get `go test`; the Gin handler and Svelte component are verified by build + `gofmt` + manual/curl checks, not automated tests.
- **Period types (exact strings):** `24h`, `7d`, `30d`, `90d`, `365d`, `all_time` — matches `allPeriodTypes` in `core/records-meta.go`.
- **Endpoint path stays under `/api/stats/motion/...`** for consistency with the other 7 leaderboard routes.
- **Branch:** `feat/recent-observations`, created off `main` (spec already committed there at `docs/superpowers/specs/2026-08-02-recent-observations-design.md`).

---

## Task 1: Backend — `recentObservationsCutoff` pure helper

**Files:**
- Create: `core/recent-observations.go`
- Test: `core/recent-observations_test.go`

**Interfaces:**
- Produces: `func recentObservationsCutoff(period string, now time.Time) time.Time` — the earliest `first_seen` a "recent observations" query should include. Returns the zero `time.Time` for `"all_time"` (and for any period `periodWindow` doesn't recognize), which is always earlier than any real `flight_history.first_seen` and therefore imposes no lower bound. Consumed by Task 2's handler.

- [ ] **Step 1: Create the branch**

```bash
git checkout main && git pull --ff-only
git checkout -b feat/recent-observations
```

- [ ] **Step 2: Write the failing tests**

Create `core/recent-observations_test.go`:

```go
package main

import (
	"testing"
	"time"
)

func TestRecentObservationsCutoffAllTime(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	got := recentObservationsCutoff("all_time", now)
	if !got.IsZero() {
		t.Errorf("all_time: got %v want zero time", got)
	}
}

func TestRecentObservationsCutoff7d(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	got := recentObservationsCutoff("7d", now)
	want := now.Add(-7 * 24 * time.Hour)
	if !got.Equal(want) {
		t.Errorf("7d: got %v want %v", got, want)
	}
}

func TestRecentObservationsCutoffInvalidPeriod(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	got := recentObservationsCutoff("not-a-period", now)
	if !got.IsZero() {
		t.Errorf("invalid period: got %v want zero time", got)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

```bash
export PATH=~/.local/go/bin:$PATH
cd core && go test ./... -run TestRecentObservationsCutoff -v
```
Expected: FAIL — `recentObservationsCutoff` is undefined (compile error).

- [ ] **Step 4: Create the minimal implementation**

Create `core/recent-observations.go`:

```go
package main

import (
	"time"
)

// recentObservationsCutoff returns the earliest first_seen a "recent
// observations" query should include for the given period, relative to now.
// The zero time.Time is returned for "all_time" (and any unrecognized
// period), which is always before any real flight_history.first_seen and so
// imposes no lower bound.
func recentObservationsCutoff(period string, now time.Time) time.Time {
	window, ok := periodWindow(period)
	if !ok {
		return time.Time{}
	}
	return now.Add(-window)
}
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
cd core && go test ./... -run TestRecentObservationsCutoff -v
```
Expected: all three PASS.

- [ ] **Step 6: Commit**

```bash
git add core/recent-observations.go core/recent-observations_test.go
git commit -m "feat: add recentObservationsCutoff helper for recent-observations period filtering"
```

---

## Task 2: Backend — `getRecentObservations` handler + route

**Files:**
- Modify: `core/recent-observations.go` (add the handler alongside Task 1's helper)
- Modify: `core/api.go:104` (register the route)

**Interfaces:**
- Consumes: `recentObservationsCutoff` (Task 1), `isValidPeriodType`/`periodWindow` (`core/records-meta.go`, existing), `(s *APIServer) getLimit(settingKey ...string) int` (`core/api.go`, existing), `s.pg.db` (`*pgxpool.Pool`, existing field on `APIServer`).
- Produces: `GET /api/stats/motion/recent?period=<24h|7d|30d|90d|365d|all_time>` → `200` with a JSON array (possibly empty) of:
  ```json
  {
    "hex": "c023aa", "flight": "ACA872", "registration": "C-FIUJ", "type": "B77L",
    "first_seen": "2026-08-01T09:12:00Z", "last_seen": "2026-08-01T09:45:00Z",
    "origin_iata_code": "YYZ", "destination_iata_code": "LHR",
    "airline_name": "Air Canada"
  }
  ```
  `flight`, `registration`, `type`, `last_seen`, `origin_iata_code`, `destination_iata_code`, `airline_name` are nullable (JSON `null`) — `flight_history` has no `NOT NULL` constraint on them. `400` with `{"error":"invalid period"}` for an unrecognized `period`. This is the endpoint `MotionRecentObservations.svelte` (Task 3) fetches.

- [ ] **Step 1: Add the handler to `core/recent-observations.go`**

Find the entire file as created in Task 1:
```go
package main

import (
	"time"
)

// recentObservationsCutoff returns the earliest first_seen a "recent
// observations" query should include for the given period, relative to now.
// The zero time.Time is returned for "all_time" (and any unrecognized
// period), which is always before any real flight_history.first_seen and so
// imposes no lower bound.
func recentObservationsCutoff(period string, now time.Time) time.Time {
	window, ok := periodWindow(period)
	if !ok {
		return time.Time{}
	}
	return now.Add(-window)
}
```

Replace the whole file with:

```go
package main

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// recentObservationsCutoff returns the earliest first_seen a "recent
// observations" query should include for the given period, relative to now.
// The zero time.Time is returned for "all_time" (and any unrecognized
// period), which is always before any real flight_history.first_seen and so
// imposes no lower bound.
func recentObservationsCutoff(period string, now time.Time) time.Time {
	window, ok := periodWindow(period)
	if !ok {
		return time.Time{}
	}
	return now.Add(-window)
}

// getRecentObservations returns the N most recently observed flights
// (flight_history rows), newest first, optionally windowed by period. Unlike
// getRecords, this is not ranked on a single metric — it is the raw
// chronological feed of every evaluated flight, records or not.
func (s *APIServer) getRecentObservations(c *gin.Context) {
	period := c.DefaultQuery("period", "all_time")
	if !isValidPeriodType(period) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid period"})
		return
	}

	limit := s.getLimit("record_holder_table_limit")
	if limit > 100 {
		limit = 100
	}

	cutoff := recentObservationsCutoff(period, time.Now())

	query := `
		SELECT fh.hex, fh.flight, fh.registration, fh.type, fh.first_seen, fh.last_seen,
		       fh.origin_iata_code, fh.destination_iata_code, rt.airline_name
		FROM flight_history fh
		LEFT JOIN route_data rt ON fh.flight = rt.route_callsign
		WHERE fh.first_seen >= $1
		ORDER BY fh.first_seen DESC
		LIMIT $2`

	rows, err := s.pg.db.Query(context.Background(), query, cutoff, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	out := []gin.H{}
	for rows.Next() {
		var hex string
		var flight, registration, aircraftType *string
		var firstSeen time.Time
		var lastSeen *time.Time
		var originIata, destinationIata, airlineName *string

		if err := rows.Scan(&hex, &flight, &registration, &aircraftType,
			&firstSeen, &lastSeen, &originIata, &destinationIata, &airlineName); err != nil {
			continue
		}

		out = append(out, gin.H{
			"hex":                   hex,
			"flight":                flight,
			"registration":          registration,
			"type":                  aircraftType,
			"first_seen":            firstSeen,
			"last_seen":             lastSeen,
			"origin_iata_code":      originIata,
			"destination_iata_code": destinationIata,
			"airline_name":          airlineName,
		})
	}

	c.JSON(http.StatusOK, out)
}
```

- [ ] **Step 2: Register the route in `core/api.go`**

Find (around line 104):
```go
			stats.GET("/motion/furthest-flown", func(c *gin.Context) { s.getRecords(c, "furthest_flown") })
			stats.GET("/motion/most-remaining", func(c *gin.Context) { s.getRecords(c, "most_remaining") })
			stats.GET("/motion/longest-route", func(c *gin.Context) { s.getRecords(c, "longest_route") })
```
Replace with:
```go
			stats.GET("/motion/furthest-flown", func(c *gin.Context) { s.getRecords(c, "furthest_flown") })
			stats.GET("/motion/most-remaining", func(c *gin.Context) { s.getRecords(c, "most_remaining") })
			stats.GET("/motion/longest-route", func(c *gin.Context) { s.getRecords(c, "longest_route") })
			stats.GET("/motion/recent", s.getRecentObservations)
```

- [ ] **Step 3: gofmt, build, vet, test**

```bash
export PATH=~/.local/go/bin:$PATH
gofmt -l core/recent-observations.go core/api.go
```
Expected: no output (if it lists either file, run `gofmt -w` on it and re-check).
```bash
cd core && go build ./... && go vet ./... && go test ./...
```
Expected: all clean, `ok`/no failures (the three `TestRecentObservationsCutoff*` tests from Task 1 still pass).

- [ ] **Step 4: Runtime verification (if a stack is running — local `docker compose up -d` or against the 251 test server)**

```bash
BASE=http://localhost:8080   # or the relevant host:port
curl -s "$BASE/api/stats/motion/recent?period=all_time" | jq '. | length, .[0]'
curl -s "$BASE/api/stats/motion/recent?period=24h" | jq 'length'
curl -s -o /dev/null -w '%{http_code}\n' "$BASE/api/stats/motion/recent?period=bogus"
```
Expected: `all_time` returns an array (non-empty on a populated DB) whose first element has `hex`, `first_seen`, and nullable fields either populated or `null`; `24h` returns `<=` the `all_time` count; `bogus` → `400`. This step needs a running stack — skip if none is available in this environment and rely on Task 5's full verification instead.

- [ ] **Step 5: Commit**

```bash
git add core/recent-observations.go core/api.go
git commit -m "feat: add GET /api/stats/motion/recent endpoint"
```

---

## Task 3: Frontend — `MotionRecentObservations.svelte`

**Files:**
- Create: `web/src/components/MotionRecentObservations.svelte`

**Interfaces:**
- Consumes: `MotionStats.svelte` (existing, unmodified — takes `endpoint`, `title`, `columns`, `icon` props), `IconHistory` from `@tabler/icons-svelte` (confirmed present in `web/node_modules/@tabler/icons-svelte/dist/icons/history.svelte`).
- Produces: a Svelte component with no exported props, ready to be wired into `dashboardCards.js` (Task 4) as `{ id: 'motion_recent', title: 'Recent Observations', tab: 'motion-stat', component: MotionRecentObservations }`.

- [ ] **Step 1: Create the component**

Create `web/src/components/MotionRecentObservations.svelte`, modeled directly on `MotionFurthestFlownAircraft.svelte`:

```svelte
<script>
    import MotionStats from './MotionStats.svelte';
    import { IconHistory } from '@tabler/icons-svelte';

    const columns = [
        { header: 'Reg', field: 'registration', class: 'font-mono whitespace-nowrap' },
        { header: 'Airline', field: 'airline_name', formatter: (value) => value || '-' },
        { header: 'Type', field: 'type' },
        { header: 'From', field: 'origin_iata_code', formatter: (value) => value || '-' },
        { header: 'To', field: 'destination_iata_code', formatter: (value) => value || '-' },
        {
            header: 'First Seen',
            field: 'first_seen',
            class: 'whitespace-nowrap',
            formatter: (value) => value ? new Date(value).toLocaleString() : '-'
        }
    ];
</script>

<MotionStats
    endpoint="api/stats/motion/recent"
    title="Recent Observations"
    {columns}
    icon={IconHistory}
/>
```

- [ ] **Step 2: Verify the frontend builds**

```bash
cd web && npm run build
```
Expected: builds with no errors (confirms the `IconHistory` import resolves and the component is syntactically valid Svelte).

- [ ] **Step 3: Commit**

```bash
git add web/src/components/MotionRecentObservations.svelte
git commit -m "feat: add MotionRecentObservations card component"
```

---

## Task 4: Frontend — wire into `dashboardCards.js`

**Files:**
- Modify: `web/src/lib/dashboardCards.js`

**Interfaces:**
- Consumes: `MotionRecentObservations` (Task 3).
- Produces: the card appears first (top-left, in the 2-column grid) among the `tab: 'motion-stat'` cards rendered by `TabMotionStats.svelte`, and gets a hide/show entry on the Settings → Cards tab automatically (same mechanism as the other 7 motion cards — no `HideableCard`/settings code changes needed).

- [ ] **Step 1: Add the import**

Find:
```js
import MotionFastestAircraft from '../components/MotionFastestAircraft.svelte';
```
Replace with:
```js
import MotionRecentObservations from '../components/MotionRecentObservations.svelte';
import MotionFastestAircraft from '../components/MotionFastestAircraft.svelte';
```

- [ ] **Step 2: Add the card entry first in the motion-stat group**

Find:
```js
    { id: 'motion_fastest', title: 'Fastest Aircraft', tab: 'motion-stat', component: MotionFastestAircraft },
```
Replace with:
```js
    { id: 'motion_recent', title: 'Recent Observations', tab: 'motion-stat', component: MotionRecentObservations },
    { id: 'motion_fastest', title: 'Fastest Aircraft', tab: 'motion-stat', component: MotionFastestAircraft },
```

- [ ] **Step 3: Verify the frontend builds**

```bash
cd web && npm run build
```
Expected: builds with no errors.

- [ ] **Step 4: Manual verification (with a running API)**

```bash
cd web && npm run dev -- --host
```
Open the Record Holders tab. Confirm: the "Recent Observations" card renders first (top-left), shows rows sorted newest-first by "First Seen", switching the period selector (24h/7d/.../All-time) reloads it same as the other 6 cards, clicking a row opens the aircraft detail modal, and the card can be hidden/shown from Settings → Cards like the other motion cards.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/dashboardCards.js
git commit -m "feat: add Recent Observations card to Record Holders tab"
```

---

## Task 5: Full verification

**Files:** none — verification pass.

- [ ] **Step 1:** Backend: `export PATH=~/.local/go/bin:$PATH && cd core && gofmt -l . && go build ./... && go vet ./... && go test ./...` — `gofmt -l .` prints nothing new (pre-existing `db-connector.go` non-gofmt lines, if any, are not this plan's concern), build/vet/test all clean.
- [ ] **Step 2:** Frontend: `cd web && npm run build` — clean.
- [ ] **Step 3:** With a running stack (local `docker compose up -d` or the 251 test server), confirm end-to-end: the Recent Observations card is first in Record Holders, all period values (`24h`, `7d`, `30d`, `90d`, `365d`, `all_time`) return `200` and reload the card, an invalid `?period=` still returns `400` on `/motion/recent` same as the other 6 routes, row click opens the aircraft modal, and hiding the card via Settings → Cards removes it from the tab. Deploy to 251 is a separate, user-triggered step (per [[deployment-251]] / [[deploy-stale-base-hazard]] — rebase on current `origin/main` immediately before deploying).
