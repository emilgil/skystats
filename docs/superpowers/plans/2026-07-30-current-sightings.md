# Current Sightings Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a "Current Sightings" tab listing every aircraft the receiver can see right now within `RADIUS`, sorted nearest first, with no row cap.

**Architecture:** The 2-second ticker that already fetches `aircraft.json` builds the complete response payload once per tick and stores it in a mutex-guarded package-level store; `GET /api/stats/current` only serialises that store, so cost is constant regardless of how many browser tabs are open. Live altitude/speed/position come from the readsb snapshot (the `aircraft_data` columns hold per-visit *maxima*, not current values); registration, type, airline, route and the interesting flag are joined in from Postgres in a single query.

**Tech Stack:** Go 1.25 (pgx v5, gin, zerolog, cheap-ruler), PostgreSQL, Svelte 5 + Tailwind 4 + DaisyUI, Vite.

## Global Constraints

- Spec: `docs/superpowers/specs/2026-07-30-current-sightings-design.md` — read it before starting.
- Work happens in the worktree `/mnt/c/temp/github/claude/skystats-current-sightings` on branch `feat/current-sightings`. **Never `git checkout` in `/mnt/c/temp/github/claude/skystats`** — a parallel session owns that directory.
- Go module is `github.com/tomcarman/skystats`; the daemon is a single `main` package in `core/`.
- Go binary is at `~/.local/go/bin/go` (not on `PATH`). There is no Docker locally.
- Repo has tests despite what `CLAUDE.md` says: `core/haversine_test.go`, `core/records-meta_test.go`. Run with `~/.local/go/bin/go test ./core/...`. Match their style: standard library only, `t.Errorf`, no assertion libraries.
- **Task 7 is blocked** until `feat/aircraft-info-modal` is merged to `main`. Do Tasks 1-6 and 8 first.
- **Do not deploy.** Deployment to 192.168.1.251 requires explicit user sign-off that the aircraft info modal is verified.
- No schema changes, no migrations.

---

### Task 1: Snapshot-to-row transformation

The pure function that turns a readsb snapshot plus database enrichment into
sorted table rows. Kept free of database and environment access so it is
testable.

**Files:**
- Create: `core/current-sightings.go`
- Create: `core/current-sightings_test.go`
- Modify: `core/models.go` (add `Desc` to `Aircraft`)

**Interfaces:**
- Consumes: `Aircraft` from `core/models.go`.
- Produces: `CurrentSighting` struct, `aircraftEnrichment` struct, and
  `buildCurrentSightings(aircraft []Aircraft, enrichment map[string]aircraftEnrichment, nowEpoch float64, distanceKm func(lat, lon float64) float64) []CurrentSighting`.
  Tasks 3 and 4 use all three.

- [ ] **Step 1: Add the `Desc` field to `Aircraft`**

The feed carries a human-readable type description (`"desc": "BOEING 737 MAX 8"`)
that the struct does not currently capture. In `core/models.go`, immediately
after the `T` field (line 20, `T string \`json:"t"\``), add:

```go
	Desc                string  `json:"desc"`
```

- [ ] **Step 2: Write the failing tests**

Create `core/current-sightings_test.go`:

```go
package main

import (
	"testing"
	"time"
)

// A fixed distance function keyed on latitude keeps the tests independent of
// LAT/LON environment variables and of cheap-ruler's exact arithmetic.
func fakeDistance(lat, lon float64) float64 {
	return lat
}

func TestBuildCurrentSightingsSortsNearestFirst(t *testing.T) {
	aircraft := []Aircraft{
		{Hex: "aaa111", Lat: 30},
		{Hex: "bbb222", Lat: 10},
		{Hex: "ccc333", Lat: 20},
	}

	got := buildCurrentSightings(aircraft, nil, 1785399041, fakeDistance)

	want := []string{"bbb222", "ccc333", "aaa111"}
	if len(got) != len(want) {
		t.Fatalf("got %d rows want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Hex != want[i] {
			t.Errorf("position %d: got %s want %s", i, got[i].Hex, want[i])
		}
	}
}

func TestBuildCurrentSightingsPrefersFeedValuesOverDatabase(t *testing.T) {
	dbReg := "SE-DBB"
	dbType := "B737"
	aircraft := []Aircraft{{Hex: "aaa111", R: "SE-RTM", T: "B38M", Lat: 5}}
	enrichment := map[string]aircraftEnrichment{
		"aaa111": {Registration: &dbReg, IcaoType: &dbType},
	}

	got := buildCurrentSightings(aircraft, enrichment, 1785399041, fakeDistance)

	if got[0].Registration != "SE-RTM" {
		t.Errorf("registration: got %s want SE-RTM", got[0].Registration)
	}
	if got[0].Type != "B38M" {
		t.Errorf("type: got %s want B38M", got[0].Type)
	}
}

func TestBuildCurrentSightingsFallsBackToDatabaseValues(t *testing.T) {
	dbReg := "SE-DBB"
	dbType := "B737"
	aircraft := []Aircraft{{Hex: "aaa111", Lat: 5}}
	enrichment := map[string]aircraftEnrichment{
		"aaa111": {Registration: &dbReg, IcaoType: &dbType},
	}

	got := buildCurrentSightings(aircraft, enrichment, 1785399041, fakeDistance)

	if got[0].Registration != "SE-DBB" {
		t.Errorf("registration: got %s want SE-DBB", got[0].Registration)
	}
	if got[0].Type != "B737" {
		t.Errorf("type: got %s want B737", got[0].Type)
	}
}

// Airline and operator stay separate fields: most aircraft here are not
// scheduled airline traffic, and putting a private owner's name in a column
// labelled Airline would misrepresent the data.
func TestBuildCurrentSightingsKeepsAirlineAndOperatorSeparate(t *testing.T) {
	owner := "Some Private Owner"
	aircraft := []Aircraft{{Hex: "aaa111", Lat: 5}}
	enrichment := map[string]aircraftEnrichment{
		"aaa111": {RegisteredOwner: &owner},
	}

	got := buildCurrentSightings(aircraft, enrichment, 1785399041, fakeDistance)

	if got[0].Airline != nil {
		t.Errorf("airline: got %v want nil", *got[0].Airline)
	}
	if got[0].Operator == nil || *got[0].Operator != owner {
		t.Errorf("operator: got %v want %s", got[0].Operator, owner)
	}
}

func TestBuildCurrentSightingsDerivesLastSeenFromSeen(t *testing.T) {
	var nowEpoch float64 = 1785399041
	aircraft := []Aircraft{{Hex: "aaa111", Lat: 5, Seen: 12}}

	got := buildCurrentSightings(aircraft, nil, nowEpoch, fakeDistance)

	want := time.Unix(int64(nowEpoch), 0).Add(-12 * time.Second)
	if !got[0].LastSeen.Equal(want) {
		t.Errorf("last seen: got %v want %v", got[0].LastSeen, want)
	}
}

func TestBuildCurrentSightingsLeavesUnknownDescriptionNil(t *testing.T) {
	aircraft := []Aircraft{{Hex: "aaa111", Lat: 5}}

	got := buildCurrentSightings(aircraft, nil, 1785399041, fakeDistance)

	if got[0].TypeDescription != nil {
		t.Errorf("type description: got %v want nil", *got[0].TypeDescription)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd /mnt/c/temp/github/claude/skystats-current-sightings && ~/.local/go/bin/go test ./core/... -run CurrentSightings -v`
Expected: FAIL — `undefined: buildCurrentSightings`, `undefined: aircraftEnrichment`.

- [ ] **Step 4: Write the implementation**

Create `core/current-sightings.go`:

```go
package main

import (
	"math"
	"sort"
	"time"
)

// CurrentSighting is one row of the Current Sightings table: an aircraft the
// receiver can see right now. Live values come from the readsb snapshot; the
// optional fields are joined in from Postgres and stay nil until the
// registration and route enrichment jobs have caught up with the aircraft.
type CurrentSighting struct {
	Hex              string    `json:"hex"`
	Flight           string    `json:"flight"`
	Registration     string    `json:"registration"`
	Type             string    `json:"type"`
	TypeDescription  *string   `json:"type_description"`
	Airline          *string   `json:"airline"`
	Operator         *string   `json:"operator"`
	Altitude         int       `json:"altitude"`
	GroundSpeed      float64   `json:"ground_speed"`
	Track            float64   `json:"track"`
	DistanceKm       float64   `json:"distance_km"`
	OriginIata       *string   `json:"origin_iata"`
	OriginName       *string   `json:"origin_name"`
	DestinationIata  *string   `json:"destination_iata"`
	DestinationName  *string   `json:"destination_name"`
	InterestingGroup *string   `json:"interesting_group"`
	LastSeen         time.Time `json:"last_seen"`
}

// aircraftEnrichment holds the per-hex details that live in Postgres rather
// than in the readsb feed.
type aircraftEnrichment struct {
	Registration     *string
	IcaoType         *string
	RegisteredOwner  *string
	AirlineName      *string
	OriginIata       *string
	OriginName       *string
	DestinationIata  *string
	DestinationName  *string
	InterestingGroup *string
}

// buildCurrentSightings turns a readsb snapshot plus database enrichment into
// the rows the API serves, nearest first.
//
// distanceKm is injected rather than calling getDistance directly so this stays
// testable without LAT/LON set in the environment.
func buildCurrentSightings(
	aircraft []Aircraft,
	enrichment map[string]aircraftEnrichment,
	nowEpoch float64,
	distanceKm func(lat, lon float64) float64,
) []CurrentSighting {

	now := time.Unix(int64(nowEpoch), 0)
	sightings := make([]CurrentSighting, 0, len(aircraft))

	for _, a := range aircraft {
		e := enrichment[a.Hex]

		sightings = append(sightings, CurrentSighting{
			Hex:              a.Hex,
			Flight:           a.Flight,
			Registration:     firstNonEmpty(a.R, stringValue(e.Registration)),
			Type:             firstNonEmpty(a.T, stringValue(e.IcaoType)),
			TypeDescription:  nilIfEmpty(a.Desc),
			Airline:          e.AirlineName,
			Operator:         e.RegisteredOwner,
			Altitude:         a.AltBaro,
			GroundSpeed:      a.Gs,
			Track:            a.Track,
			DistanceKm:       math.Round(distanceKm(a.Lat, a.Lon)*100) / 100,
			OriginIata:       e.OriginIata,
			OriginName:       e.OriginName,
			DestinationIata:  e.DestinationIata,
			DestinationName:  e.DestinationName,
			InterestingGroup: e.InterestingGroup,
			LastSeen:         now.Add(-time.Duration(a.Seen * float64(time.Second))),
		})
	}

	sort.SliceStable(sightings, func(i, j int) bool {
		return sightings[i].DistanceKm < sightings[j].DistanceKm
	})

	return sightings
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func nilIfEmpty(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd /mnt/c/temp/github/claude/skystats-current-sightings && ~/.local/go/bin/go test ./core/... -run CurrentSightings -v`
Expected: PASS, all six tests.

- [ ] **Step 6: Verify the whole package still builds and is clean**

Run: `cd /mnt/c/temp/github/claude/skystats-current-sightings && ~/.local/go/bin/go build ./... && ~/.local/go/bin/go vet ./...`
Expected: no output.

- [ ] **Step 7: Commit**

```bash
cd /mnt/c/temp/github/claude/skystats-current-sightings
git add core/current-sightings.go core/current-sightings_test.go core/models.go
git commit -m "feat: build current sightings rows from readsb snapshot

Live altitude and speed must come from the feed: the aircraft_data
columns hold per-visit maxima, not current values.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 2: Shared store

A mutex-guarded store the ticker writes and the API goroutine reads.

**Files:**
- Modify: `core/current-sightings.go` (append)
- Modify: `core/current-sightings_test.go` (append)

**Interfaces:**
- Consumes: `CurrentSighting` from Task 1.
- Produces: package-level `currentSightings *currentSightingsStore` with
  `replace(aircraft []CurrentSighting, generatedAt time.Time)` and
  `snapshot() ([]CurrentSighting, time.Time)`. Tasks 3 and 4 use both.

- [ ] **Step 1: Write the failing tests**

Append to `core/current-sightings_test.go`:

```go
func TestCurrentSightingsStoreReturnsWhatWasStored(t *testing.T) {
	store := &currentSightingsStore{}
	generatedAt := time.Unix(1785399041, 0)

	store.replace([]CurrentSighting{{Hex: "aaa111"}}, generatedAt)
	got, at := store.snapshot()

	if len(got) != 1 || got[0].Hex != "aaa111" {
		t.Errorf("aircraft: got %v want one row with hex aaa111", got)
	}
	if !at.Equal(generatedAt) {
		t.Errorf("generatedAt: got %v want %v", at, generatedAt)
	}
}

// snapshot must hand back a copy: the API goroutine ranges over the result
// while the 2s ticker is replacing the store's contents.
func TestCurrentSightingsStoreSnapshotIsACopy(t *testing.T) {
	store := &currentSightingsStore{}
	store.replace([]CurrentSighting{{Hex: "aaa111"}}, time.Now())

	got, _ := store.snapshot()
	got[0].Hex = "mutated"

	again, _ := store.snapshot()
	if again[0].Hex != "aaa111" {
		t.Errorf("store was mutated through the snapshot: got %s", again[0].Hex)
	}
}

func TestCurrentSightingsStoreIsRaceFree(t *testing.T) {
	store := &currentSightingsStore{}
	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				store.replace([]CurrentSighting{{Hex: "aaa111", DistanceKm: float64(n)}}, time.Now())
			}
		}(i)
	}

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				aircraft, _ := store.snapshot()
				for range aircraft {
				}
			}
		}()
	}

	wg.Wait()
}
```

Add `"sync"` to the test file's import block.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd /mnt/c/temp/github/claude/skystats-current-sightings && ~/.local/go/bin/go test ./core/... -run CurrentSightingsStore -v`
Expected: FAIL — `undefined: currentSightingsStore`.

- [ ] **Step 3: Write the implementation**

Append to `core/current-sightings.go` (and add `"sync"` to its imports):

```go
// currentSightingsStore holds the payload the 2s ticker builds so the API
// goroutine can serve it without touching the database per request.
type currentSightingsStore struct {
	mu          sync.RWMutex
	aircraft    []CurrentSighting
	generatedAt time.Time
}

var currentSightings = &currentSightingsStore{}

func (s *currentSightingsStore) replace(aircraft []CurrentSighting, generatedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.aircraft = aircraft
	s.generatedAt = generatedAt
}

// snapshot returns a copy so callers can range over the result while the
// ticker replaces the store's contents.
func (s *currentSightingsStore) snapshot() ([]CurrentSighting, time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	aircraft := make([]CurrentSighting, len(s.aircraft))
	copy(aircraft, s.aircraft)
	return aircraft, s.generatedAt
}
```

- [ ] **Step 4: Run the tests with the race detector**

Run: `cd /mnt/c/temp/github/claude/skystats-current-sightings && ~/.local/go/bin/go test -race ./core/... -run CurrentSightings -v`
Expected: PASS, no `WARNING: DATA RACE`.

- [ ] **Step 5: Commit**

```bash
cd /mnt/c/temp/github/claude/skystats-current-sightings
git add core/current-sightings.go core/current-sightings_test.go
git commit -m "feat: add mutex-guarded store for current sightings payload

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 3: Database enrichment and ticker wiring

**Files:**
- Modify: `core/current-sightings.go` (append)
- Modify: `core/aircraft.go:54` (call the refresh after the database update)

**Interfaces:**
- Consumes: `buildCurrentSightings` and `currentSightings` from Tasks 1-2;
  `postgres` from `core/db-connector.go`; `getDistance` from `core/aircraft.go:74`.
- Produces: `refreshCurrentSightings(pg *postgres, nowEpoch float64, aircraft []Aircraft)`.

- [ ] **Step 1: Write the enrichment query and refresh function**

Append to `core/current-sightings.go`. Add `"context"` and
`"github.com/rs/zerolog/log"` to the imports.

`LEFT JOIN LATERAL ... LIMIT 1` is deliberate: a plain `LEFT JOIN` against
`route_data` duplicates the row when a callsign appears more than once, which is
invisible in `getAboveStats` because it caps at 5 rows but would show as
duplicate rows in an uncapped table.

```go
// fetchAircraftEnrichment looks up the slower-moving details for a whole
// snapshot in one round trip, keyed by hex.
func fetchAircraftEnrichment(pg *postgres, hexes []string, flights []string) (map[string]aircraftEnrichment, error) {

	query := `
		SELECT s.hex,
		       reg.registration,
		       reg.icao_type,
		       reg.registered_owner,
		       rt.airline_name,
		       rt.origin_iata_code,
		       rt.origin_name,
		       rt.destination_iata_code,
		       rt.destination_name,
		       ia."group"
		FROM unnest($1::text[], $2::text[]) AS s(hex, flight)
		LEFT JOIN registration_data reg ON reg.mode_s = s.hex
		LEFT JOIN LATERAL (
			SELECT airline_name, origin_iata_code, origin_name,
			       destination_iata_code, destination_name
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
			&e.RegisteredOwner,
			&e.AirlineName,
			&e.OriginIata,
			&e.OriginName,
			&e.DestinationIata,
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
}

// refreshCurrentSightings rebuilds the Current Sightings payload from the
// snapshot the ingest ticker just processed.
func refreshCurrentSightings(pg *postgres, nowEpoch float64, aircraft []Aircraft) {

	generatedAt := time.Unix(int64(nowEpoch), 0)

	if len(aircraft) == 0 {
		currentSightings.replace([]CurrentSighting{}, generatedAt)
		return
	}

	hexes := make([]string, 0, len(aircraft))
	flights := make([]string, 0, len(aircraft))
	for _, a := range aircraft {
		hexes = append(hexes, a.Hex)
		flights = append(flights, a.Flight)
	}

	enrichment, err := fetchAircraftEnrichment(pg, hexes, flights)
	if err != nil {
		// Serve the live rows without enrichment rather than freezing the
		// table on stale data.
		log.Error().Err(err).Msg("refreshCurrentSightings() - unable to fetch enrichment")
		enrichment = map[string]aircraftEnrichment{}
	}

	sightings := buildCurrentSightings(aircraft, enrichment, nowEpoch, func(lat, lon float64) float64 {
		return *getDistance([]float64{lon, lat})
	})

	currentSightings.replace(sightings, generatedAt)
}
```

Note the argument order in the closure: `getDistance` takes `[]float64{lon, lat}`,
longitude first, matching cheap-ruler.

- [ ] **Step 2: Wire it into the ticker**

In `core/aircraft.go`, `updateAircraftDatabase` currently ends at line 54 with:

```go
	pg.updateDatabase(response.Now, aircraftsInRange)
}
```

Change to:

```go
	pg.updateDatabase(response.Now, aircraftsInRange)
	refreshCurrentSightings(pg, response.Now, aircraftsInRange)
}
```

Placing it after `updateDatabase` means newly discovered aircraft already exist
in `aircraft_data` when the enrichment query runs.

- [ ] **Step 3: Verify it builds and existing tests still pass**

Run: `cd /mnt/c/temp/github/claude/skystats-current-sightings && ~/.local/go/bin/go build ./... && ~/.local/go/bin/go vet ./... && ~/.local/go/bin/go test ./core/...`
Expected: no build or vet output; tests `ok`.

- [ ] **Step 4: Commit**

```bash
cd /mnt/c/temp/github/claude/skystats-current-sightings
git add core/current-sightings.go core/aircraft.go
git commit -m "feat: enrich current sightings from Postgres each tick

LATERAL LIMIT 1 rather than a plain LEFT JOIN: duplicate callsigns in
route_data would otherwise duplicate rows in an uncapped table.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 4: API endpoint

**Files:**
- Modify: `core/current-sightings.go` (append handler)
- Modify: `core/api.go:83` (register the route)

**Interfaces:**
- Consumes: `currentSightings.snapshot()` from Task 2; `APIServer` from `core/api.go`.
- Produces: `GET /api/stats/current` returning
  `{"generated_at": <RFC3339 string|null>, "aircraft": [CurrentSighting]}`.
  Task 5 consumes this shape.

- [ ] **Step 1: Write the handler**

Append to `core/current-sightings.go`. Add `"net/http"` and
`"github.com/gin-gonic/gin"` to the imports.

```go
// getCurrentSightings serves the payload the ticker has already assembled.
// generated_at is null until the first tick has completed, and lets the
// frontend tell "nothing in range" apart from "the feed stopped answering".
func (s *APIServer) getCurrentSightings(c *gin.Context) {

	aircraft, generatedAt := currentSightings.snapshot()

	var generated *time.Time
	if !generatedAt.IsZero() {
		generated = &generatedAt
	}

	c.JSON(http.StatusOK, gin.H{
		"generated_at": generated,
		"aircraft":     aircraft,
	})
}
```

- [ ] **Step 2: Register the route**

In `core/api.go`, line 83 currently reads:

```go
			stats.GET("/above", s.getAboveStats)
```

Add directly beneath it:

```go
			stats.GET("/current", s.getCurrentSightings)
```

- [ ] **Step 3: Verify build and vet**

Run: `cd /mnt/c/temp/github/claude/skystats-current-sightings && ~/.local/go/bin/go build ./... && ~/.local/go/bin/go vet ./... && ~/.local/go/bin/go test ./core/...`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
cd /mnt/c/temp/github/claude/skystats-current-sightings
git add core/current-sightings.go core/api.go
git commit -m "feat: add GET /api/stats/current endpoint

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 5: Frontend tab and table

Renders the tab and the table with a single fetch. Polling comes in Task 6, so
this task is reviewable on its own: the tab appears, shows real data once, and
is togglable in Settings.

**Files:**
- Create: `web/src/components/CurrentSightings.svelte`
- Create: `web/src/components/TabCurrentSightings.svelte`
- Modify: `web/src/lib/dashboardCards.js`
- Modify: `web/src/App.svelte:13-21`
- Modify: `web/src/components/Settings.svelte:7-14`

**Interfaces:**
- Consumes: `GET /api/stats/current` from Task 4; `HideableCard.svelte`;
  `dashboardCards` from `web/src/lib/dashboardCards.js`.
- Produces: card id `current_sightings`, tab key `current-stat`. Task 6 modifies
  `CurrentSightings.svelte`; Task 7 adds the row click.

- [ ] **Step 1: Create the card component**

Create `web/src/components/CurrentSightings.svelte`:

```svelte
<script>
    import { onMount } from 'svelte';
    import { IconPlane } from '@tabler/icons-svelte';

    const endpoint = 'api/stats/current';

    let data = [];
    let generatedAt = null;
    let loading = true;
    let error = null;

    async function fetchData() {
        try {
            const response = await fetch(endpoint);
            if (!response.ok) {
                throw new Error(`${response.status}`);
            }
            const result = await response.json();
            data = result.aircraft || [];
            generatedAt = result.generated_at;
            error = null;
        } catch (err) {
            error = err.message;
        } finally {
            loading = false;
        }
    }

    onMount(() => {
        fetchData();
    });

    function formatAltitude(feet) {
        return feet ? `${feet.toLocaleString('en-US')} ft` : '—';
    }

    function formatSpeed(knots) {
        return knots ? `${Math.round(knots)} kt` : '—';
    }

    function formatRoute(aircraft) {
        if (!aircraft.origin_iata && !aircraft.destination_iata) {
            return '—';
        }
        return `${aircraft.origin_iata || '???'} → ${aircraft.destination_iata || '???'}`;
    }

    function formatOperator(aircraft) {
        return aircraft.airline || aircraft.operator || '—';
    }
</script>

<div class="card bg-base-100 mb-4 shadow-sm rounded hover:shadow-md transition-all duration-200">
    <div class="card-body">
        <div class="flex items-center gap-2 mb-5">
            <div class="w-8 h-8 rounded-lg flex items-center justify-center">
                <IconPlane class="w-6 h-6 text-primary" />
            </div>
            <h2 class="text-2xl font-extralight tracking-wider">Current Sightings</h2>
        </div>

        {#if loading}
            <div class="flex justify-center py-8">
                <span class="loading loading-ring loading-lg"></span>
            </div>
        {:else if error}
            <div class="flex alert alert-error">
                <span>Something went wrong: {error}</span>
            </div>
        {:else if data.length === 0}
            <div class="alert alert-info">
                <span>No aircraft currently visible</span>
            </div>
        {:else}
            <div class="overflow-x-auto max-h-[70vh] overflow-y-auto">
                <table class="table table-sm table-pin-rows">
                    <thead class="uppercase tracking-wider">
                        <tr>
                            <th>Flight</th>
                            <th>Reg</th>
                            <th>Type</th>
                            <th>Airline</th>
                            <th>Altitude</th>
                            <th>Speed</th>
                            <th>Distance</th>
                            <th>Route</th>
                            <th></th>
                            <th>Last Seen</th>
                        </tr>
                    </thead>
                    <tbody>
                        {#each data as aircraft (aircraft.hex)}
                            <tr>
                                <td class="font-mono whitespace-nowrap">{aircraft.flight || '—'}</td>
                                <td class="font-mono whitespace-nowrap">{aircraft.registration || '—'}</td>
                                <td class="whitespace-nowrap" title={aircraft.type_description || ''}>
                                    {aircraft.type || '—'}
                                </td>
                                <td>{formatOperator(aircraft)}</td>
                                <td class="whitespace-nowrap">{formatAltitude(aircraft.altitude)}</td>
                                <td class="whitespace-nowrap">{formatSpeed(aircraft.ground_speed)}</td>
                                <td class="whitespace-nowrap">{aircraft.distance_km.toFixed(1)} km</td>
                                <td class="font-mono whitespace-nowrap">{formatRoute(aircraft)}</td>
                                <td>
                                    {#if aircraft.interesting_group}
                                        <div class="badge badge-accent text-white">{aircraft.interesting_group}</div>
                                    {/if}
                                </td>
                                <td class="whitespace-nowrap">
                                    {aircraft.last_seen ? new Date(aircraft.last_seen).toLocaleTimeString() : '—'}
                                </td>
                            </tr>
                        {/each}
                    </tbody>
                </table>
            </div>
        {/if}
    </div>
</div>
```

- [ ] **Step 2: Create the tab wrapper**

Create `web/src/components/TabCurrentSightings.svelte`, following
`TabRouteStats.svelte`'s shape but as a single full-width card:

```svelte
<script>
    import HideableCard from './HideableCard.svelte';
    import { dashboardCards } from '../lib/dashboardCards';

    const cards = dashboardCards.filter((c) => c.tab === 'current-stat');
</script>

<div class="grid grid-cols-1 mt-6 gap-6">
    {#each cards as card (card.id)}
        <HideableCard id={card.id} title={card.title}>
            <svelte:component this={card.component} {...(card.props || {})} />
        </HideableCard>
    {/each}
</div>
```

- [ ] **Step 3: Register the card**

In `web/src/lib/dashboardCards.js`, add the import after the `AboveTimeline`
import on line 1:

```js
import CurrentSightings from '../components/CurrentSightings.svelte';
```

and add this entry directly after the `above_timeline` entry (line 30):

```js
    { id: 'current_sightings', title: 'Current Sightings', tab: 'current-stat', component: CurrentSightings },
```

- [ ] **Step 4: Add the tab**

In `web/src/App.svelte`, add the import beside the other tab imports (after
line 8):

```js
  import TabCurrentSightings from './components/TabCurrentSightings.svelte';
```

Replace the `activeTab` initialiser and the `tabs` array (lines 13-21) with:

```js
  let activeTab = 'current-stat';
  let tabsElement;

  const tabs = [
    { name: 'current-stat', label: 'Current Sightings', component: TabCurrentSightings },
    { name: 'activity', label: 'Activity', component: TabActivity },
    { name: 'route-stat', label: 'Route Information', component: TabRouteStats },
    { name: 'interesting-stat', label: 'Interesting Aircraft', component: TabInterestingStats },
    { name: 'motion-stat', label: 'Record Holders', component: TabMotionStats }
  ];
```

- [ ] **Step 5: Add the tab to Settings**

Without this the card is missing from the "Visible Cards" list. In
`web/src/components/Settings.svelte`, replace lines 7-14 with:

```js
    const cardTabLabels = {
        global: 'Always Visible',
        'current-stat': 'Current Sightings',
        activity: 'Activity',
        'route-stat': 'Route Information',
        'interesting-stat': 'Interesting Aircraft',
        'motion-stat': 'Record Holders'
    };
    const cardTabOrder = ['global', 'current-stat', 'activity', 'route-stat', 'interesting-stat', 'motion-stat'];
```

- [ ] **Step 6: Verify the build**

Run: `cd /mnt/c/temp/github/claude/skystats-current-sightings/web && npm install && npm run build`
Expected: build succeeds, no unresolved imports.

- [ ] **Step 7: Commit**

```bash
cd /mnt/c/temp/github/claude/skystats-current-sightings
git add web/src/components/CurrentSightings.svelte web/src/components/TabCurrentSightings.svelte web/src/lib/dashboardCards.js web/src/App.svelte web/src/components/Settings.svelte
git commit -m "feat: add Current Sightings tab and table

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 6: Polling lifecycle and live states

Turns the static table into a live one. Split from Task 5 because the failure
modes here are what a reviewer actually needs to scrutinise.

**Files:**
- Modify: `web/src/components/CurrentSightings.svelte`

**Interfaces:**
- Consumes: everything from Task 5.
- Produces: no new exports.

- [ ] **Step 1: Replace the script block**

`App.svelte` mounts *every* tab component and hides the inactive ones with
`class="hidden"` (`App.svelte:103-107`). The other tabs fetch once in `onMount`
so it never mattered; a 2-second poller would otherwise keep running while you
look at another tab or minimise the browser. The component therefore decides for
itself whether it is on screen.

Replace the `<script>` block of `web/src/components/CurrentSightings.svelte`
with:

```svelte
<script>
    import { onMount, onDestroy } from 'svelte';
    import { IconPlane } from '@tabler/icons-svelte';

    const endpoint = 'api/stats/current';
    const refreshRate = 2000;
    const staleAfterMs = 30000;

    let data = [];
    let generatedAt = null;
    let loading = true;
    let error = null;
    let interval = null;
    let observer = null;
    let onScreen = false;
    let cardElement;
    let now = Date.now();

    async function fetchData() {
        try {
            const response = await fetch(endpoint);
            if (!response.ok) {
                throw new Error(`${response.status}`);
            }
            const result = await response.json();
            data = result.aircraft || [];
            generatedAt = result.generated_at;
            error = null;
        } catch (err) {
            // Keep the last good table on screen — one dropped poll out of
            // thirty per minute should not blank the view.
            error = err.message;
        } finally {
            // Never set loading back to true here: the skeleton would flash
            // over the table every two seconds.
            loading = false;
            now = Date.now();
        }
    }

    function startPolling() {
        if (interval) return;
        fetchData();
        interval = setInterval(fetchData, refreshRate);
    }

    function stopPolling() {
        if (!interval) return;
        clearInterval(interval);
        interval = null;
    }

    function syncPolling() {
        if (onScreen && !document.hidden) {
            startPolling();
        } else {
            stopPolling();
        }
    }

    onMount(() => {
        observer = new IntersectionObserver((entries) => {
            onScreen = entries[0].isIntersecting;
            syncPolling();
        });
        observer.observe(cardElement);
        document.addEventListener('visibilitychange', syncPolling);
    });

    onDestroy(() => {
        stopPolling();
        if (observer) {
            observer.disconnect();
        }
        document.removeEventListener('visibilitychange', syncPolling);
    });

    $: stale = generatedAt !== null && now - new Date(generatedAt).getTime() > staleAfterMs;

    function formatAltitude(feet) {
        return feet ? `${feet.toLocaleString('en-US')} ft` : '—';
    }

    function formatSpeed(knots) {
        return knots ? `${Math.round(knots)} kt` : '—';
    }

    function formatRoute(aircraft) {
        if (!aircraft.origin_iata && !aircraft.destination_iata) {
            return '—';
        }
        return `${aircraft.origin_iata || '???'} → ${aircraft.destination_iata || '???'}`;
    }

    function formatOperator(aircraft) {
        return aircraft.airline || aircraft.operator || '—';
    }
</script>
```

- [ ] **Step 2: Bind the observed element and add the warning banners**

In the same file, change the outermost `<div class="card ...">` to add the bind:

```svelte
<div bind:this={cardElement} class="card bg-base-100 mb-4 shadow-sm rounded hover:shadow-md transition-all duration-200">
```

Then replace the `{#if loading} … {:else if error} …` chain so that an error no
longer replaces the table. The block from `{#if loading}` down to its closing
`{/if}` becomes:

```svelte
        {#if loading}
            <div class="flex justify-center py-8">
                <span class="loading loading-ring loading-lg"></span>
            </div>
        {:else}
            {#if error}
                <div class="alert alert-warning mb-4 py-2 text-sm">
                    <span>Live update failed ({error}) — showing last known data</span>
                </div>
            {:else if stale}
                <div class="alert alert-warning mb-4 py-2 text-sm">
                    <span>Feed has not updated since {new Date(generatedAt).toLocaleTimeString()}</span>
                </div>
            {/if}

            {#if data.length === 0}
                <div class="alert alert-info">
                    <span>No aircraft currently visible</span>
                </div>
            {:else}
                <div class="overflow-x-auto max-h-[70vh] overflow-y-auto">
                    <!-- leave the existing <table> element and everything
                         inside it exactly as it already is in this file -->
                </div>
            {/if}
        {/if}
```

This is an edit to the surrounding control flow only. The `<table>` element
already present in the file, from its `<table class="table table-sm table-pin-rows">`
opening tag through its closing `</table>`, moves inside the new
`overflow-x-auto` div unchanged — do not retype or restyle it.

- [ ] **Step 3: Verify the build**

Run: `cd /mnt/c/temp/github/claude/skystats-current-sightings/web && npm run build`
Expected: build succeeds.

- [ ] **Step 4: Verify polling behaviour in the browser**

Start the daemon and the dev server, open the network tab, and confirm:

1. Requests to `api/stats/current` arrive roughly every 2 seconds while the
   Current Sightings tab is open.
2. They **stop** when you switch to another tab in the app.
3. They **resume** when you switch back.
4. They **stop** when you minimise or background the browser window.
5. The table does not flash a loading spinner between updates.

- [ ] **Step 5: Commit**

```bash
cd /mnt/c/temp/github/claude/skystats-current-sightings
git add web/src/components/CurrentSightings.svelte
git commit -m "feat: poll current sightings live and pause when off screen

App.svelte keeps every tab mounted and hides inactive ones with CSS, so
the component gates its own polling on an IntersectionObserver plus
document visibility.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 7: Row click opens the aircraft modal

> **BLOCKED** until `feat/aircraft-info-modal` is merged to `main`. Before
> starting: `git fetch origin && git log origin/main --oneline | head` and confirm
> the modal work is in, then
> `git rebase origin/main`.

**Files:**
- Modify: `web/src/components/CurrentSightings.svelte`

**Interfaces:**
- Consumes: `openAircraftModal(hex)` — a **named export** from
  `web/src/stores/aircraftModal.js`, created by `feat/aircraft-info-modal`.
  It is not `aircraftModal.open(hex)`; verify the signature after rebasing by
  reading `web/src/components/MotionStats.svelte`, which uses the same call.
- Produces: nothing new.

- [ ] **Step 1: Confirm the dependency exists**

Run: `cd /mnt/c/temp/github/claude/skystats-current-sightings && cat web/src/stores/aircraftModal.js && grep -n "openAircraftModal" web/src/components/MotionStats.svelte`
Expected: the store file exists and exports `openAircraftModal`; `MotionStats.svelte` calls it with a hex. If the export is named differently, use the actual name and note the discrepancy.

- [ ] **Step 2: Import the store**

In `web/src/components/CurrentSightings.svelte`, add to the `<script>` imports:

```js
    import { openAircraftModal } from '../stores/aircraftModal';
```

- [ ] **Step 3: Make the row clickable**

Change the row element inside `{#each data as aircraft (aircraft.hex)}` from:

```svelte
                            <tr>
```

to:

```svelte
                            <tr class="cursor-pointer hover:bg-base-300" on:click={() => openAircraftModal(aircraft.hex)}>
```

This matches how `MotionStats.svelte` and `InterestingAircraft.svelte` make
rows clickable.

- [ ] **Step 4: Verify the build and the behaviour**

Run: `cd /mnt/c/temp/github/claude/skystats-current-sightings/web && npm run build`
Expected: build succeeds.

Then in the browser: click a row and confirm the shared modal opens showing that
aircraft, and that it still opens correctly for a row whose aircraft has no
registration or route data.

- [ ] **Step 5: Commit**

```bash
cd /mnt/c/temp/github/claude/skystats-current-sightings
git add web/src/components/CurrentSightings.svelte
git commit -m "feat: open aircraft modal from current sightings rows

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 8: End-to-end verification and handoff

**Files:** none modified.

- [ ] **Step 1: Full backend check**

Run:
```bash
cd /mnt/c/temp/github/claude/skystats-current-sightings
~/.local/go/bin/go build ./... && ~/.local/go/bin/go vet ./... && ~/.local/go/bin/go test -race ./core/...
```
Expected: clean build, clean vet, tests `ok`.

- [ ] **Step 2: Endpoint check against a running daemon**

With the daemon running and a populated `.env`:

```bash
curl -s localhost:8080/api/stats/current | head -c 2000
```

Confirm: `generated_at` is a recent timestamp; `aircraft` is sorted ascending by
`distance_km`; `airline` and `operator` are distinct fields; `interesting_group`
is `null` or one of `Mil`/`Gov`/`Pol`/`Civ`.

- [ ] **Step 3: Feed-outage check**

Point `READSB_AIRCRAFT_JSON` at an unreachable URL, restart the daemon, and
confirm the endpoint still answers with `generated_at: null` and an empty
`aircraft` array rather than erroring. Restore the real URL afterwards.

- [ ] **Step 4: Frontend check**

Run `npm run build`, then `npm run dev -- --host`, and walk through: sorting
nearest first, the sticky header while scrolling a long list, the empty state
when nothing is in range, the polling start/stop behaviour from Task 6, and the
row click from Task 7.

- [ ] **Step 5: Push and open a pull request**

```bash
cd /mnt/c/temp/github/claude/skystats-current-sightings
git push
gh pr create --base main --title "feat: current sightings tab" --body "$(cat <<'EOF'
Adds a Current Sightings tab listing every aircraft within `RADIUS` right
now, sorted nearest first, with no row cap.

Live altitude, speed and position come from the readsb snapshot rather
than `aircraft_data`, whose `alt_baro`/`gs`/`ias`/`tas` columns hold
per-visit maxima rather than current values. The payload is assembled
once per 2s ingest tick and held in a mutex-guarded store, so serving it
costs the same whether zero or twenty browser tabs are open.

Row clicks reuse the shared aircraft modal. The component gates its own
polling on an IntersectionObserver plus document visibility, because
`App.svelte` keeps every tab component mounted and hides the inactive
ones with CSS.

Design: `docs/superpowers/specs/2026-07-30-current-sightings-design.md`
Plan: `docs/superpowers/plans/2026-07-30-current-sightings.md`

Verified: `go build`, `go vet`, `go test -race ./core/...`,
`npm run build`, plus manual endpoint and browser checks.
Not deployed — awaiting sign-off that the aircraft info modal is verified.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 6: STOP — do not deploy**

Deployment to 192.168.1.251 requires explicit user sign-off that the aircraft
info modal has been verified. Report what was built and verified, and wait.

---

## Notes carried forward from the spec

**Flagged, deliberately not fixed here:** readsb reports `alt_baro` as the
string `"ground"` for aircraft on the ground, while `Aircraft.AltBaro` is an
`int` and the `json.Unmarshal` error is discarded at `core/aircraft.go:30`.
Ground aircraft therefore get altitude 0, which shows as `—` in this table and
almost certainly pollutes the `lowest_aircraft` leaderboard with 0-foot
"records". That is a pre-existing bug deserving its own branch. If you want to
confirm the behaviour while working here, watch for aircraft with
`"alt_baro": "ground"` in the raw feed.

**Also out of scope:** no map, no sortable column headers, no filtering or
search, no schema changes, and no cleanup of `AboveTimeline.svelte`'s own inline
modal (which becomes redundant once `AircraftModal` exists, but which neither
brief covers).
