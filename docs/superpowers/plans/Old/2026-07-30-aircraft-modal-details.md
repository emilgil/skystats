# Aircraft Info Modal — Details Enrichment — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enrich the existing aircraft info modal with the aircraft's manufacturer/type, the records it holds, and a list of its recent observations (route + timestamps + speed/altitude), plus the total observation count.

**Architecture:** Extend the existing read-only handler `getAircraftDetail` (`core/aircraft-detail.go`) with three additions sourced from existing tables — `manufacturer`/`icao_type` (registration_data), `records[]` (best value per category from `records`, deduped across periods via `recordCategories[cat].KeepMax`), and `observations[]` (newest 10 rows from `flight_history`). The frontend `AircraftModal.svelte` renders three new sections. No schema change, no migration, no new dependencies.

**Tech Stack:** Go (gin + pgx v5), Svelte 5 + Tailwind + DaisyUI.

## Global Constraints

- **No schema change, no migration, no new dependencies.** All data from existing tables (`registration_data`, `records`, `flight_history`).
- **Go version:** `1.25.3` (per `core/go.mod`). If `go` is not on `PATH`, download it locally — do not install system-wide.
- **No test framework exists** (`CLAUDE.md`). Each task's verification is compile/build (`go build ./...` + `go vet ./...`, `npm run build`) plus, where a running stack is available, `curl`. There is no `go test`/`vitest`. Do NOT add a test framework.
- **Numeric scan rule:** cast Postgres `NUMERIC` to `::float8` when scanning into Go `float64` (matches the existing handler).
- **`records[]` and `observations[]` must always be JSON arrays** — empty `[]`, never `null`.
- **Category→metric facts** (from `core/records-meta.go`, same package — usable directly): fastest/slowest → `ground_speed`; highest/lowest → `barometric_altitude`; furthest_flown → `distance_flown`; longest_route → `route_distance`; most_remaining → `distance_remaining`. `recordCategories[cat].KeepMax` is `true` when a larger value is the record (only slowest and lowest are `false`).
- **Branch:** `feat/aircraft-modal-details` (already created off `main`, spec committed).

---

## Task 1: Backend — add manufacturer/icao_type, records[], observations[]

**Files:**
- Modify: `core/aircraft-detail.go`

**Interfaces:**
- Produces: the existing `GET /api/stats/aircraft/:hex` response gains keys:
  ```json
  {
    "manufacturer": "Boeing", "icao_type": "B77L",
    "records": [ {"category":"fastest","metric_name":"ground_speed","value":566.0} ],
    "observations": [
      {"first_seen":"...","last_seen":"...","origin":"YYZ","destination":"LHR",
       "ground_speed":566.0,"altitude":37000}
    ]
  }
  ```
  `records` and `observations` are always arrays (possibly empty). Existing keys are unchanged.

- [ ] **Step 1: Add the `sort` import**

Find:
```go
import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)
```
Replace with:
```go
import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)
```

- [ ] **Step 2: Add the new keys to the response init**

Find:
```go
	resp := gin.H{
		"hex":          hex,
		"registration": nil,
		"type":         nil,
		"operator":     nil,
		"live":         nil,
		"history":      gin.H{"times_seen": 0, "last_seen": nil},
		"photo":        nil,
		"interesting":  nil,
	}
```
Replace with:
```go
	resp := gin.H{
		"hex":          hex,
		"registration": nil,
		"type":         nil,
		"manufacturer": nil,
		"icao_type":    nil,
		"operator":     nil,
		"live":         nil,
		"history":      gin.H{"times_seen": 0, "last_seen": nil},
		"photo":        nil,
		"records":      []gin.H{},
		"observations": []gin.H{},
		"interesting":  nil,
	}
```

- [ ] **Step 3: Pull manufacturer + icao_type from registration_data**

Find:
```go
	// 1) Identity + photo from registration_data (adsbdb-enriched).
	var regType, registration, registeredOwner, urlPhoto, urlPhotoThumb *string
	err := s.pg.db.QueryRow(context.Background(), `
		SELECT type, registration, registered_owner, url_photo, url_photo_thumbnail
		FROM registration_data
		WHERE mode_s = $1`, hex).
		Scan(&regType, &registration, &registeredOwner, &urlPhoto, &urlPhotoThumb)
	if err != nil && err != pgx.ErrNoRows {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if registration != nil {
		resp["registration"] = registration
	}
	if regType != nil {
		resp["type"] = regType
	}
	if registeredOwner != nil {
		resp["operator"] = registeredOwner
	}
```
Replace with:
```go
	// 1) Identity + photo from registration_data (adsbdb-enriched).
	var regType, registration, registeredOwner, manufacturer, icaoType, urlPhoto, urlPhotoThumb *string
	err := s.pg.db.QueryRow(context.Background(), `
		SELECT type, registration, registered_owner, manufacturer, icao_type, url_photo, url_photo_thumbnail
		FROM registration_data
		WHERE mode_s = $1`, hex).
		Scan(&regType, &registration, &registeredOwner, &manufacturer, &icaoType, &urlPhoto, &urlPhotoThumb)
	if err != nil && err != pgx.ErrNoRows {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if registration != nil {
		resp["registration"] = registration
	}
	if regType != nil {
		resp["type"] = regType
	}
	if manufacturer != nil {
		resp["manufacturer"] = manufacturer
	}
	if icaoType != nil {
		resp["icao_type"] = icaoType
	}
	if registeredOwner != nil {
		resp["operator"] = registeredOwner
	}
```

- [ ] **Step 4: Add records[] and observations[] before the final `c.JSON`**

Find (the interesting block's end followed by the final response):
```go
		resp["interesting"] = gin.H{
			"group":    iGroup,
			"operator": iOperator,
			"tags":     tags,
			"images":   images,
		}
	}

	c.JSON(http.StatusOK, resp)
}
```
Replace with:
```go
		resp["interesting"] = gin.H{
			"group":    iGroup,
			"operator": iOperator,
			"tags":     tags,
			"images":   images,
		}
	}

	// 5) Records this aircraft holds: best value per category, deduped across periods.
	recRows, err := s.pg.db.Query(context.Background(), `
		SELECT category, metric_name, metric_value::float8
		FROM records
		WHERE hex = $1`, hex)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	best := map[string]gin.H{}
	for recRows.Next() {
		var category, metricName string
		var value float64
		if err := recRows.Scan(&category, &metricName, &value); err != nil {
			continue
		}
		cur, ok := best[category]
		if !ok {
			best[category] = gin.H{"category": category, "metric_name": metricName, "value": value}
			continue
		}
		// recordCategories[category].KeepMax: true when a larger value is the record.
		if recordCategories[category].KeepMax {
			if value > cur["value"].(float64) {
				best[category] = gin.H{"category": category, "metric_name": metricName, "value": value}
			}
		} else {
			if value < cur["value"].(float64) {
				best[category] = gin.H{"category": category, "metric_name": metricName, "value": value}
			}
		}
	}
	recRows.Close()
	records := []gin.H{}
	for _, r := range best {
		records = append(records, r)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i]["category"].(string) < records[j]["category"].(string)
	})
	resp["records"] = records

	// 6) Recent observations (visits) from flight_history, newest first, max 10.
	obsRows, err := s.pg.db.Query(context.Background(), `
		SELECT first_seen, last_seen, origin_iata_code, destination_iata_code,
		       ground_speed::float8, barometric_altitude
		FROM flight_history
		WHERE hex = $1
		ORDER BY first_seen DESC
		LIMIT 10`, hex)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	observations := []gin.H{}
	for obsRows.Next() {
		var firstSeen, lastSeen *time.Time
		var origin, destination *string
		var groundSpeed *float64
		var altitude *int
		if err := obsRows.Scan(&firstSeen, &lastSeen, &origin, &destination, &groundSpeed, &altitude); err != nil {
			continue
		}
		observations = append(observations, gin.H{
			"first_seen":   firstSeen,
			"last_seen":    lastSeen,
			"origin":       origin,
			"destination":  destination,
			"ground_speed": groundSpeed,
			"altitude":     altitude,
		})
	}
	obsRows.Close()
	resp["observations"] = observations

	c.JSON(http.StatusOK, resp)
}
```

- [ ] **Step 5: Verify it compiles**

If `go` is not on `PATH`, download it locally first (skip if `go version` prints `go1.25.3`):
```bash
curl -sSL https://go.dev/dl/go1.25.3.linux-amd64.tar.gz -o /tmp/go.tgz && tar -xzf /tmp/go.tgz -C /tmp
export PATH=/tmp/go/bin:$PATH
```
Then:
```bash
cd core && go build -o /tmp/skystats-daemon ./... && go vet ./...
```
Expected: both clean, no output.

- [ ] **Step 6: Runtime verification (if a stack is running)**

Against a running daemon+Postgres (local `docker compose up -d` or 251 on `:5173`):
```bash
BASE=http://localhost:8080   # or http://localhost:5173 on 251
HEX=$(curl -s "$BASE/api/stats/motion/fastest?period=all_time" | jq -r '.[0].hex')
curl -s "$BASE/api/stats/aircraft/$HEX" | jq '{manufacturer, icao_type, records, observations}'
```
Expected: `manufacturer` a string (e.g. "Boeing"), `records` an array with at least one `{category,metric_name,value}` (e.g. fastest 566), `observations` an array (≤10 rows) with `first_seen`/`origin`/`destination`/`ground_speed`/`altitude`. For an unknown hex, both arrays are `[]`.

- [ ] **Step 7: Commit**
```bash
git add core/aircraft-detail.go
git commit -m "feat: add manufacturer, records held, and observations to aircraft detail endpoint"
```

---

## Task 2: Frontend — Aircraft, Records, and Observations sections

**Files:**
- Modify: `web/src/components/AircraftModal.svelte`

**Interfaces:**
- Consumes: the Task 1 response keys `manufacturer`, `icao_type`, `records[]`, `observations[]`, and existing `history.times_seen`.

- [ ] **Step 1: Add the category→label/unit map to the `<script>`**

Find:
```js
    $: disableTags = $settings['disable_planealertdb_tags']?.setting_value === 'true';
```
Insert directly after it:
```js

    const RECORD_LABELS = {
        fastest: { label: 'Fastest', unit: 'kt' },
        slowest: { label: 'Slowest', unit: 'kt' },
        highest: { label: 'Highest', unit: 'ft' },
        lowest: { label: 'Lowest', unit: 'ft' },
        furthest_flown: { label: 'Furthest flown', unit: 'km' },
        longest_route: { label: 'Longest route', unit: 'km' },
        most_remaining: { label: 'Most remaining', unit: 'km' },
    };
```

- [ ] **Step 2: Add the Aircraft line + Records badges after the operator line**

Find:
```svelte
            {#if data.operator}
                <p class="text-sm text-gray-600 mb-4">{data.operator}</p>
            {/if}
```
Replace with:
```svelte
            {#if data.operator}
                <p class="text-sm text-gray-600 mb-1">{data.operator}</p>
            {/if}
            {#if data.manufacturer || data.type}
                <p class="text-sm text-gray-500 mb-3">
                    {[data.manufacturer, data.type].filter(Boolean).join(' · ')}{#if data.icao_type} ({data.icao_type}){/if}
                </p>
            {/if}
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

- [ ] **Step 3: Add the Observations table after the times-seen/last-seen block**

Find:
```svelte
            <div class="flex gap-6 mb-4 text-sm">
                <div><span class="text-xs uppercase text-gray-500">Times seen</span><div>{data.history?.times_seen ?? 0}</div></div>
                <div><span class="text-xs uppercase text-gray-500">Last seen</span><div>{data.history?.last_seen ? new Date(data.history.last_seen).toLocaleString() : '-'}</div></div>
            </div>
```
Replace with:
```svelte
            <div class="flex gap-6 mb-4 text-sm">
                <div><span class="text-xs uppercase text-gray-500">Times seen</span><div>{data.history?.times_seen ?? 0}</div></div>
                <div><span class="text-xs uppercase text-gray-500">Last seen</span><div>{data.history?.last_seen ? new Date(data.history.last_seen).toLocaleString() : '-'}</div></div>
            </div>

            {#if data.observations?.length}
                <div class="mb-4">
                    <div class="text-xs uppercase text-gray-500 mb-1">Recent observations</div>
                    <div class="overflow-x-auto">
                        <table class="table table-xs">
                            <thead><tr><th>Time</th><th>Route</th><th>Speed</th><th>Alt</th></tr></thead>
                            <tbody>
                                {#each data.observations as obs}
                                    <tr>
                                        <td class="whitespace-nowrap">{obs.first_seen ? new Date(obs.first_seen).toLocaleString() : '-'}</td>
                                        <td class="whitespace-nowrap">{obs.origin || '—'} → {obs.destination || '—'}</td>
                                        <td>{obs.ground_speed != null ? Math.round(obs.ground_speed) + ' kt' : '-'}</td>
                                        <td>{obs.altitude != null ? obs.altitude + ' ft' : '-'}</td>
                                    </tr>
                                {/each}
                            </tbody>
                        </table>
                    </div>
                    {#if (data.history?.times_seen ?? 0) > data.observations.length}
                        <div class="text-xs text-gray-500 mt-1">…and {data.history.times_seen - data.observations.length} more</div>
                    {/if}
                </div>
            {/if}
```

- [ ] **Step 4: Verify it builds**
```bash
cd web && npm run build
```
Expected: builds with no errors.

- [ ] **Step 5: Manual verification (with a running API)**
```bash
cd web && npm run dev -- --host
```
Click a record-holder row. Confirm the modal shows: the aircraft line (e.g. "Boeing · 777 233LR (B77L)"), a Records badge (e.g. "Fastest — 566 kt"), and a "Recent observations" table (time · route · speed · alt), with "…and N more" when the plane has been seen more than 10 times. Confirm sections hide cleanly when their data is empty (e.g. an aircraft with no records shows no Records badges).

- [ ] **Step 6: Commit**
```bash
git add web/src/components/AircraftModal.svelte
git commit -m "feat: show aircraft, records held, and recent observations in the detail modal"
```

---

## Task 3: Full verification

**Files:** none — verification pass.

- [ ] **Step 1:** Backend `cd core && go build -o /tmp/skystats-daemon ./... && go vet ./...` — clean (add the local Go toolchain to `PATH` first if needed).
- [ ] **Step 2:** Frontend `cd web && npm run build` — clean.
- [ ] **Step 3:** With a running stack (local or on deploy), confirm the modal end-to-end for: a record-holder not currently visible (aircraft line + records + observations render; live shows "Not currently visible"), an aircraft with a resolved route (observations show `ORIG → DEST`), and an unknown hex (`records`/`observations` empty → those sections hidden, no errors). Deploy is a separate, user-triggered step (git archive → ssh tar → `docker compose up -d --build`).
