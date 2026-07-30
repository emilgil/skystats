# Aircraft Info Modal — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a shared aircraft-detail modal, opened by clicking any Record Holders or Interesting Aircraft row, showing live status, sighting history, and a photo.

**Architecture:** A new read-only endpoint `GET /api/stats/aircraft/:hex` assembles identity, live status (`aircraft_data`), history (`flight_history`), photo (`registration_data`, adsbdb), and interesting metadata (`interesting_aircraft_seen`) server-side. On the frontend a single `AircraftModal.svelte`, mounted once in `App.svelte` and driven by a small store, fetches that endpoint on open; the photo falls back to a client-side planespotters lookup by hex. Existing list payloads are untouched.

**Tech Stack:** Go (gin + pgx v5), Svelte 5 + Tailwind 4 + DaisyUI. No new dependencies.

## Global Constraints

- **No schema change, no migration, no new dependencies.** All data comes from existing tables/columns.
- **Go version:** `1.25.3` (per `core/go.mod`). If `go` is not on `PATH`, download it locally (see Task 1 notes) — do not install system-wide.
- **No test framework exists in this repo** (see `CLAUDE.md`). Each task's "test cycle" is compile/build + runtime verification (`curl` for the API, browser for the UI), mirroring how earlier features were verified. There is no `go test`/`vitest` to run.
- **Numeric scan rule:** when selecting a Postgres `NUMERIC` column into a Go `float64`, cast it `::float8` in SQL (matches `getRecords`).
- **Follow existing patterns:** the DaisyUI `<dialog class="modal">` singleton pattern already used by `Settings.svelte` (mounted once in `App.svelte`); the client-side planespotters fetch already in `AboveTimeline.svelte` (`getImage`); stores via `writable` from `svelte/store` in `web/src/stores/`.
- **Frontend↔API wiring:** `web/vite.config.js` proxies `/api` → `http://localhost:8080`. A running daemon + Postgres is required for runtime verification (local `docker compose up -d`, a locally-run daemon, or deploy to 251).
- **Branch:** `feat/aircraft-info-modal` (already created off `main`).

---

## Task 1: Backend — `GET /api/stats/aircraft/:hex`

**Files:**
- Create: `core/aircraft-detail.go`
- Modify: `core/api.go` (register the route, next to the other `/stats/` routes)

**Interfaces:**
- Produces: HTTP `GET /api/stats/aircraft/:hex` returning JSON:
  ```json
  {
    "hex": "4ca7b5", "registration": "EI-DYY", "type": "B738", "operator": "Ryanair",
    "live": { "altitude": 37000, "ground_speed": 456.0, "track": 270.0,
              "distance_km": 12.3, "bearing": 210.0, "lat": 57.1, "lon": 12.2 },
    "history": { "times_seen": 42, "last_seen": "2026-07-29T08:12:00Z" },
    "photo": { "url": "...", "thumbnail": "...", "source": "adsbdb" },
    "interesting": { "group": "Mil", "operator": "RAF", "tags": ["Fighter"], "images": ["..."] }
  }
  ```
  `live`, `photo`, and `interesting` are `null` when they don't apply.

- [ ] **Step 1: Create the handler `core/aircraft-detail.go`**

```go
package main

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// getAircraftDetail assembles a single aircraft's detail (identity, live status,
// history, photo, interesting metadata) for the info modal. Read-only, on-demand.
func (s *APIServer) getAircraftDetail(c *gin.Context) {
	hex := c.Param("hex")
	if hex == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "hex is required"})
		return
	}

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
	if urlPhoto != nil || urlPhotoThumb != nil {
		resp["photo"] = gin.H{"url": urlPhoto, "thumbnail": urlPhotoThumb, "source": "adsbdb"}
	}

	// 2) Live status: newest aircraft_data row for this hex, airline via route_data.
	var flight, airlineName *string
	var altBaro *int
	var gs, track, distance, bearing, lat, lon *float64
	var lastSeen *time.Time
	err = s.pg.db.QueryRow(context.Background(), `
		SELECT ad.flight, ad.alt_baro,
		       ad.gs::float8, ad.track::float8,
		       ad.last_seen_distance::float8, ad.last_seen_bearing::float8,
		       ad.last_seen_lat::float8, ad.last_seen_lon::float8,
		       ad.last_seen, rt.airline_name
		FROM aircraft_data ad
		LEFT JOIN route_data rt ON ad.flight = rt.route_callsign
		WHERE ad.hex = $1
		ORDER BY ad.last_seen DESC
		LIMIT 1`, hex).
		Scan(&flight, &altBaro, &gs, &track, &distance, &bearing, &lat, &lon, &lastSeen, &airlineName)
	if err != nil && err != pgx.ErrNoRows {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err == nil {
		// Airline name (from the route) is a better "operator" than registered owner.
		if airlineName != nil && *airlineName != "" {
			resp["operator"] = airlineName
		}
		if lastSeen != nil && time.Since(*lastSeen) <= 60*time.Second {
			resp["live"] = gin.H{
				"altitude":     altBaro,
				"ground_speed": gs,
				"track":        track,
				"distance_km":  distance,
				"bearing":      bearing,
				"lat":          lat,
				"lon":          lon,
			}
		}
	}

	// 3) History: count of visits + most recent visit from flight_history.
	var timesSeen int
	var histLastSeen *time.Time
	err = s.pg.db.QueryRow(context.Background(), `
		SELECT COUNT(*), MAX(last_seen)
		FROM flight_history
		WHERE hex = $1`, hex).
		Scan(&timesSeen, &histLastSeen)
	if err != nil && err != pgx.ErrNoRows {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if histLastSeen == nil {
		// Not yet swept into flight_history — fall back to the live-query timestamp.
		histLastSeen = lastSeen
	}
	resp["history"] = gin.H{"times_seen": timesSeen, "last_seen": histLastSeen}

	// 4) Interesting metadata: newest sighting for this hex.
	var iGroup, iOperator, tag1, tag2, tag3, img1, img2, img3 *string
	err = s.pg.db.QueryRow(context.Background(), `
		SELECT "group", operator, tag1, tag2, tag3,
		       image_link_1, image_link_2, image_link_3
		FROM interesting_aircraft_seen
		WHERE hex = $1
		ORDER BY seen DESC
		LIMIT 1`, hex).
		Scan(&iGroup, &iOperator, &tag1, &tag2, &tag3, &img1, &img2, &img3)
	if err != nil && err != pgx.ErrNoRows {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err == nil {
		tags := []string{}
		for _, t := range []*string{tag1, tag2, tag3} {
			if t != nil && *t != "" {
				tags = append(tags, *t)
			}
		}
		images := []string{}
		for _, im := range []*string{img1, img2, img3} {
			if im != nil && *im != "" {
				images = append(images, *im)
			}
		}
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

- [ ] **Step 2: Register the route in `core/api.go`**

Find (around line 83, in the `stats` route group):

```go
			stats.GET("/above", s.getAboveStats)
```

Insert directly after it:

```go
			stats.GET("/aircraft/:hex", s.getAircraftDetail)
```

- [ ] **Step 3: Verify it compiles**

If `go` is not on `PATH`, download the pinned toolchain locally first (skip if `go version` already prints `go1.25.3`):

```bash
curl -sSL https://go.dev/dl/go1.25.3.linux-amd64.tar.gz -o /tmp/go.tgz && tar -xzf /tmp/go.tgz -C /tmp
export PATH=/tmp/go/bin:$PATH
```

Then:

```bash
cd core && go build -o /tmp/skystats-daemon ./... && go vet ./...
```

Expected: both succeed with no output/errors.

- [ ] **Step 4: Runtime verification against a running stack**

With a running daemon + Postgres (local `docker compose up -d`, a locally-run daemon on `:8080`, or after deploy to 251 on `:5173`), pick a hex from an existing endpoint and hit the new one. Adjust the base URL/port to your stack:

```bash
BASE=http://localhost:8080   # or http://localhost:5173 on 251
# a) any recorded aircraft
HEX=$(curl -s "$BASE/api/stats/motion/fastest?period=all_time" | jq -r '.[0].hex')
curl -s "$BASE/api/stats/aircraft/$HEX" | jq .
# b) a currently-visible aircraft (expect non-null "live")
VHEX=$(curl -s "$BASE/api/stats/above" | jq -r '.[0].hex // empty')
[ -n "$VHEX" ] && curl -s "$BASE/api/stats/aircraft/$VHEX" | jq '.live'
# c) an unknown hex (expect 200, mostly nulls, times_seen 0)
curl -s "$BASE/api/stats/aircraft/zzzzzz" | jq .
```

Expected:
- (a) returns the full object; `history.times_seen` ≥ 1; `photo` is an object or `null`; `interesting` is an object (if the plane is in plane-alert-db) or `null`.
- (b) if a plane is currently overhead, `live` is a non-null object with numeric fields.
- (c) HTTP 200, `registration`/`type`/`live`/`photo`/`interesting` all `null`, `history.times_seen` = 0.

- [ ] **Step 5: Commit**

```bash
git add core/aircraft-detail.go core/api.go
git commit -m "feat: add GET /api/stats/aircraft/:hex detail endpoint"
```

---

## Task 2: Frontend — store + shared modal, mounted once

**Files:**
- Create: `web/src/stores/aircraftModal.js`
- Create: `web/src/components/AircraftModal.svelte`
- Modify: `web/src/App.svelte` (import + mount the modal once)

**Interfaces:**
- Consumes: `GET /api/stats/aircraft/:hex` (Task 1).
- Produces: store API `openAircraftModal(hex)` / `closeAircraftModal()` and the reactive `selectedHex` store, imported from `../stores/aircraftModal`. Any row calls `openAircraftModal(aircraft.hex)` (used by Task 3).

- [ ] **Step 1: Create the store `web/src/stores/aircraftModal.js`**

```js
import { writable } from 'svelte/store';

// Holds the hex of the aircraft whose detail modal is open, or null when closed.
export const selectedHex = writable(null);

export function openAircraftModal(hex) {
    if (!hex) return;
    selectedHex.set(hex);
}

export function closeAircraftModal() {
    selectedHex.set(null);
}
```

- [ ] **Step 2: Create `web/src/components/AircraftModal.svelte`**

```svelte
<script>
// @ts-nocheck
    import { selectedHex, closeAircraftModal } from '../stores/aircraftModal';

    let data = null;
    let loading = false;
    let error = null;
    let planespotters = null; // client-side fallback photo

    async function load(hex) {
        loading = true;
        error = null;
        data = null;
        planespotters = null;
        document.getElementById('aircraft-modal').showModal();
        try {
            const res = await fetch('/api/stats/aircraft/' + hex);
            if (!res.ok) throw new Error(`${res.status}`);
            data = await res.json();
            const hasInterestingImages = data.interesting?.images?.length > 0;
            if (!hasInterestingImages && !data.photo) {
                planespotters = await fetchPlanespotters(hex);
            }
        } catch (err) {
            error = err.message;
        } finally {
            loading = false;
        }
    }

    async function fetchPlanespotters(hex) {
        try {
            const res = await fetch(`https://api.planespotters.net/pub/photos/hex/${hex}`);
            if (!res.ok) return null;
            const result = await res.json();
            const photo = result.photos?.[0];
            if (!photo) return null;
            return {
                url: photo.thumbnail_large?.src,
                photographer: photo.photographer,
                link: photo.link
            };
        } catch {
            return null;
        }
    }

    function onClose() {
        closeAircraftModal();
        data = null;
        error = null;
        planespotters = null;
    }

    // Open + fetch whenever a hex is selected.
    $: if ($selectedHex) {
        load($selectedHex);
    }
</script>

<dialog id="aircraft-modal" class="modal" on:close={onClose}>
    <div class="modal-box max-w-3xl">
        {#if loading}
            <div class="flex justify-center py-8">
                <span class="loading loading-ring loading-lg"></span>
            </div>
        {:else if error}
            <div class="flex alert alert-error">
                <span>Something went wrong: {error}</span>
            </div>
        {:else if data}
            <div class="flex items-center justify-between mb-2">
                <h3 class="text-lg font-bold">
                    {data.registration || data.hex}{#if data.type} - {data.type}{/if}
                </h3>
                {#if data.interesting?.tags?.length}
                    <div class="flex gap-2">
                        {#each data.interesting.tags as tag}
                            <div class="badge badge-accent text-white">{tag}</div>
                        {/each}
                    </div>
                {/if}
            </div>
            {#if data.operator}
                <p class="text-sm text-gray-600 mb-4">{data.operator}</p>
            {/if}

            {#if data.live}
                <div class="grid grid-cols-2 sm:grid-cols-3 gap-2 mb-4">
                    <div><span class="text-xs uppercase text-gray-500">Altitude</span><div>{data.live.altitude ?? '-'} ft</div></div>
                    <div><span class="text-xs uppercase text-gray-500">Speed</span><div>{data.live.ground_speed ?? '-'} kt</div></div>
                    <div><span class="text-xs uppercase text-gray-500">Track</span><div>{data.live.track ?? '-'}&deg;</div></div>
                    <div><span class="text-xs uppercase text-gray-500">Distance</span><div>{data.live.distance_km ?? '-'} km</div></div>
                    <div><span class="text-xs uppercase text-gray-500">Bearing</span><div>{data.live.bearing ?? '-'}&deg;</div></div>
                    <div><span class="text-xs uppercase text-gray-500">Position</span><div>{data.live.lat ?? '-'}, {data.live.lon ?? '-'}</div></div>
                </div>
            {:else}
                <div class="alert alert-info mb-4">
                    <span>Not currently visible to the receiver</span>
                </div>
            {/if}

            <div class="flex gap-6 mb-4 text-sm">
                <div><span class="text-xs uppercase text-gray-500">Times seen</span><div>{data.history?.times_seen ?? 0}</div></div>
                <div><span class="text-xs uppercase text-gray-500">Last seen</span><div>{data.history?.last_seen ? new Date(data.history.last_seen).toLocaleString() : '-'}</div></div>
            </div>

            {#if data.interesting?.images?.length}
                <div class="grid grid-cols-1 sm:grid-cols-3 gap-4">
                    {#each data.interesting.images as img}
                        <img src={img} alt="{data.registration} photo" class="w-full h-auto rounded-lg" />
                    {/each}
                </div>
            {:else if data.photo}
                <img src={data.photo.url || data.photo.thumbnail} alt="{data.registration} photo" class="w-full h-auto rounded-lg" />
            {:else if planespotters}
                <div>
                    <img src={planespotters.url} alt="{data.registration} photo" class="w-full h-auto rounded-lg" />
                    {#if planespotters.photographer}
                        <p class="text-xs text-gray-500 mt-1">
                            &copy; {planespotters.photographer}
                            {#if planespotters.link} &middot; <a class="link" href={planespotters.link} target="_blank" rel="noopener">planespotters.net</a>{/if}
                        </p>
                    {/if}
                </div>
            {:else}
                <p class="text-center text-gray-500 py-8">No photo available</p>
            {/if}
        {/if}
        <div class="modal-action">
            <form method="dialog">
                <button class="btn">Close</button>
            </form>
        </div>
    </div>
    <form method="dialog" class="modal-backdrop">
        <button>close</button>
    </form>
</dialog>
```

- [ ] **Step 3: Mount the modal once in `web/src/App.svelte`**

Find the imports block and add (next to the other component imports, e.g. after the `Settings` import on line 10):

```js
  import AircraftModal from './components/AircraftModal.svelte';
```

Find (near the end of the markup, line 113):

```svelte
<Settings />
```

Insert directly after it:

```svelte
<AircraftModal />
```

- [ ] **Step 4: Verify it builds**

```bash
cd web && npm run build
```

Expected: builds with no errors.

- [ ] **Step 5: Manual smoke test (modal not yet wired to rows)**

```bash
cd web && npm run dev -- --host
```

Open the app, then in the browser devtools console run:

```js
const { openAircraftModal } = await import('/src/stores/aircraftModal.js');
openAircraftModal('4ca7b5'); // any hex from /api/stats/motion/fastest
```

Expected: the modal opens, shows the loading ring, then renders the aircraft's detail (or "No photo available" / "Not currently visible" states). Closing it (Close button, backdrop, or Esc) dismisses it cleanly.

- [ ] **Step 6: Commit**

```bash
git add web/src/stores/aircraftModal.js web/src/components/AircraftModal.svelte web/src/App.svelte
git commit -m "feat: shared aircraft detail modal + store, mounted in App"
```

---

## Task 3: Frontend — open the modal from table rows

**Files:**
- Modify: `web/src/components/MotionStats.svelte` (make each row open the modal)
- Modify: `web/src/components/InterestingAircraft.svelte` (redirect the existing row click to the shared modal; remove the local dialog)

**Interfaces:**
- Consumes: `openAircraftModal(hex)` from `../stores/aircraftModal` (Task 2).

- [ ] **Step 1: Wire row click in `web/src/components/MotionStats.svelte`**

Add the import at the top of the `<script>` block (after the existing imports on line 4):

```js
    import { openAircraftModal } from '../stores/aircraftModal';
```

Find (the row element, line 90):

```svelte
                            {#each data as aircraft}
                            <tr>
```

Replace with:

```svelte
                            {#each data as aircraft}
                            <tr class="cursor-pointer hover:bg-base-300" on:click={() => openAircraftModal(aircraft.hex)}>
```

- [ ] **Step 2: Redirect the click in `web/src/components/InterestingAircraft.svelte`**

Add the import after the existing imports (line 3):

```js
    import { openAircraftModal } from '../stores/aircraftModal';
```

Find (the row click, line 105):

```svelte
                            <tr class="hover:bg-base-300 cursor-pointer" on:click={() => showAircraftModal(aircraft)}>
```

Replace with:

```svelte
                            <tr class="hover:bg-base-300 cursor-pointer" on:click={() => openAircraftModal(aircraft.hex)}>
```

- [ ] **Step 3: Remove the now-unused local modal from `InterestingAircraft.svelte`**

Delete the local modal machinery so there's no dead code:

1. Delete the `selectedAircraft`, `imageLoadingStates`, `showAircraftModal`, and `closeModal` declarations (lines ~13-18 and ~37-50 in the `<script>`).
2. Delete the entire `<!--modal-->` `<dialog id={aircraftType} ...> ... </dialog>` block at the bottom of the file (lines ~120-198).
3. The `aircraftType` prop is now unused by the template but is still passed by the parent Interesting components; leave the `export let aircraftType;` line so the prop contract is unchanged, or remove it and the corresponding attribute in the four `Interesting{Mil,Gov,Pol,Civ}Aircraft.svelte` parents. Simplest: **leave `export let aircraftType;`** to avoid touching the four parents.

After deletion, confirm no remaining references to `selectedAircraft`, `imageLoadingStates`, `showAircraftModal`, `closeModal`, or `disableTags` in the file:

```bash
grep -nE "selectedAircraft|imageLoadingStates|showAircraftModal|closeModal|disableTags" web/src/components/InterestingAircraft.svelte
```

Expected: no output. (If `disableTags`/`settings` are now unused, remove their `import`/`$:` lines too so the build has no unused-variable warnings.)

- [ ] **Step 4: Verify it builds**

```bash
cd web && npm run build
```

Expected: builds with no errors or unused-variable warnings.

- [ ] **Step 5: Manual verification in the browser**

```bash
cd web && npm run dev -- --host
```

Against a running API, verify:
- **Record Holders tab:** clicking anywhere on a row in each of the 7 tables opens the modal for that row's aircraft; the header shows its reg/type; live block shows fields (or "Not currently visible"); history shows times-seen + last-seen; a photo (adsbdb or planespotters) or "No photo available".
- **Interesting Aircraft tab:** clicking a row opens the SAME modal; for interesting aircraft the plane-alert-db images and tags appear, now alongside live status + history. The old per-card dialog no longer appears.
- Rows are visibly clickable (pointer cursor + hover highlight).

- [ ] **Step 6: Commit**

```bash
git add web/src/components/MotionStats.svelte web/src/components/InterestingAircraft.svelte
git commit -m "feat: open aircraft detail modal from Record Holders and Interesting rows"
```

---

## Task 4: Full end-to-end verification

**Files:** none — verification pass across the whole feature.

- [ ] **Step 1: Backend build + vet**

```bash
export PATH=/tmp/go/bin:$PATH   # if Go was downloaded locally
cd core && go build -o /tmp/skystats-daemon ./... && go vet ./...
```
Expected: clean.

- [ ] **Step 2: Frontend build**

```bash
cd web && npm run build
```
Expected: clean.

- [ ] **Step 3: End-to-end against a running stack**

Bring up the stack (local `docker compose up -d`, or deploy to 251), then confirm all four data shapes render correctly in the modal by exercising:
- a fast/record aircraft that is **not** currently visible → "Not currently visible", history populated, photo or fallback.
- a currently-visible aircraft (open the app while something is overhead, or use `/api/stats/above` to find a hex) → live fields populated.
- an interesting aircraft → tags + plane-alert-db images + live/history.
- an aircraft with no photo anywhere → "No photo available", no console errors.

Confirm no errors in the browser console and none in `docker logs skystats` (if deployed).

- [ ] **Step 4: Confirm the `flight_history` history-count assumption**

For a hex you know has been seen multiple times, compare:

```bash
BASE=http://localhost:8080
HEX=<known hex>
curl -s "$BASE/api/stats/aircraft/$HEX" | jq '.history'
```

Sanity-check `times_seen` against expectation. If `flight_history` coverage proves too sparse (e.g. recently-seen aircraft show 0), switch the Task 1 history query to count `aircraft_data` rows per hex instead — see the design doc's "Öppen punkt". This is the single call-out flagged during design.

---

## Notes for the implementer

- **Deploy is a separate, user-triggered step** — do not deploy as part of executing this plan. When the user asks, deploy to 251 with the verified flow: merge to `main`, `git archive HEAD | ssh root@192.168.1.251 'tar -x -C /opt/skystats'`, then `ssh root@192.168.1.251 'cd /opt/skystats && docker compose up -d --build'` (run `docker builder prune -f` first if disk is tight — the box has ~5.9 GB root).
- **No migration is added by this plan.** If a reviewer expects one, that is a misunderstanding — the feature is pure read path over existing tables.
