# Route on sight Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fetch an aircraft's route as soon as its callsign first appears in the 2s tick, so Current Sightings can show the route while the aircraft is still overhead.

**Architecture:** The 2s tick already fetches enrichment for the whole snapshot in one round trip, and that result reports which callsigns have no route. Callsigns lacking one are claimed through an in-memory guard and handed to a goroutine that reuses the existing `getRoutes` / `insertRoutes` pair. The on-sight path writes only `route_data`; the 300s ladder keeps sole ownership of `aircraft_data.route_processed` and `route_attempts`.

**Tech Stack:** Go 1.25.3, pgx v5, zerolog, stdlib `net/http` and `sync`. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-08-05-route-on-sight-design.md`

## Global Constraints

- Go toolchain lives at `~/.local/go/bin/go` — prefix commands with `export PATH=$HOME/.local/go/bin:$PATH`.
- Run tests from the repo root with `go test ./...`. Baseline is green; keep it green.
- Tests cover pure logic only. The database and HTTP layers have no harness in this repo — do not write tests that need either.
- Never touch `aircraft_data.route_processed` or `aircraft_data.route_attempts` from the on-sight path. That state machine belongs to `updateRoutes` in `core/routes.go`.
- Follow `core/photos.go` for the cache shape: TTL values are struct fields, not directly-used constants, so tests can set them negative to force expiry (see `core/photos_test.go:161`).
- Do not add `recover()` to the new goroutine. Existing `go notifier.*` calls (`core/records-ingest.go:135`, `core/stats-interesting.go:204`, `core/watches-engine.go:390`) have none, and consistency with the codebase wins.
- All work happens in the worktree `/mnt/c/temp/github/claude/skystats-route-on-sight` on branch `feat/route-on-sight`.

---

### Task 1: Claim-and-cooldown guard

The guard that stops the same callsign being asked about twice while a lookup is in flight, and stops a callsign the source does not know from being asked about every two seconds.

**Files:**
- Create: `core/routes-onsight.go`
- Test: `core/routes-onsight_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `type routeOnSight struct` with fields `unknownCooldown time.Duration`, `errorCooldown time.Duration`, `mu sync.Mutex`, `pending map[string]bool`, `cooldown map[string]time.Time`
  - `func newRouteOnSight() *routeOnSight`
  - `func (r *routeOnSight) claim(callsign string) bool`
  - `func (r *routeOnSight) release(callsign string, found bool, cooldown time.Duration)`
  - `var routeFetcher *routeOnSight` (package-level singleton, assigned in Task 3)
  - constants `routeUnknownCooldown = 30 * time.Minute`, `routeErrorCooldown = 2 * time.Minute`, `routeCooldownPruneAt = 500`

- [ ] **Step 1: Write the failing tests**

Create `core/routes-onsight_test.go`:

```go
package main

import (
	"fmt"
	"testing"
	"time"
)

func TestClaimReservesCallsignOnce(t *testing.T) {
	r := newRouteOnSight()

	if !r.claim("SAS1456") {
		t.Fatal("first claim was refused, want it granted")
	}
	if r.claim("SAS1456") {
		t.Fatal("second claim was granted while the first lookup is still in flight")
	}
}

func TestClaimGrantedAgainAfterRouteFound(t *testing.T) {
	r := newRouteOnSight()
	r.claim("SAS1456")
	r.release("SAS1456", true, r.unknownCooldown)

	if !r.claim("SAS1456") {
		t.Fatal("claim refused after a successful lookup released the callsign")
	}
}

func TestClaimRefusedDuringCooldown(t *testing.T) {
	r := newRouteOnSight()
	r.claim("CAT250")
	r.release("CAT250", false, r.unknownCooldown)

	if r.claim("CAT250") {
		t.Fatal("claim granted while the callsign is in cooldown")
	}
}

func TestClaimGrantedAfterCooldownExpires(t *testing.T) {
	r := newRouteOnSight()
	r.unknownCooldown = -time.Second // expired the moment it is stored

	r.claim("CAT250")
	r.release("CAT250", false, r.unknownCooldown)

	if !r.claim("CAT250") {
		t.Fatal("claim refused after the cooldown had expired")
	}
}

func TestReleaseUsesTheCooldownItIsGiven(t *testing.T) {
	r := newRouteOnSight()
	r.claim("SAS1456")
	r.release("SAS1456", false, r.errorCooldown)

	until, ok := r.cooldown["SAS1456"]
	if !ok {
		t.Fatal("no cooldown recorded for an unmatched callsign")
	}

	if until.After(time.Now().Add(routeUnknownCooldown)) {
		t.Fatalf("cooldown runs to %v, want the shorter error cooldown", until)
	}
}

func TestPruneDropsExpiredCooldownsOnly(t *testing.T) {
	r := newRouteOnSight()
	for i := 0; i < routeCooldownPruneAt; i++ {
		r.cooldown[fmt.Sprintf("OLD%d", i)] = time.Now().Add(-time.Minute)
	}
	r.cooldown["LIVE1"] = time.Now().Add(time.Hour)

	r.claim("TRIGGER") // claim sweeps before it reserves

	if _, ok := r.cooldown["OLD0"]; ok {
		t.Error("an expired cooldown survived the sweep")
	}
	if _, ok := r.cooldown["LIVE1"]; !ok {
		t.Error("a live cooldown was swept away")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$HOME/.local/go/bin:$PATH
cd /mnt/c/temp/github/claude/skystats-route-on-sight
go test ./core/ -run 'TestClaim|TestRelease|TestPrune' -v
```

Expected: FAIL — `undefined: newRouteOnSight`, `undefined: routeCooldownPruneAt`.

- [ ] **Step 3: Write the implementation**

Create `core/routes-onsight.go`:

```go
package main

import (
	"sync"
	"time"
)

// How long the on-sight path waits before asking about a callsign again.
//
// "Unknown" is an answer about the callsign, and keeps for a while: an aircraft
// is typically visible for under five minutes, so one attempt per visit is
// enough, and the 300s ladder in routes.go still retries in the background.
// "Error" is an answer about the network that says nothing about the callsign,
// so a transient blip must not cost us the flight.
const (
	routeUnknownCooldown = 30 * time.Minute
	routeErrorCooldown   = 2 * time.Minute

	// Expired entries are swept once the map grows past this, which bounds it
	// without a timer or a background goroutine.
	routeCooldownPruneAt = 500
)

// routeFetcher is assigned once at startup, alongside the other singletons in
// core.go. It stays nil in tests, where nothing drives the tick.
var routeFetcher *routeOnSight

// routeOnSight tracks which callsigns have a route lookup in flight and which
// were asked about too recently to ask again.
//
// It owns no route data itself: a successful lookup lands in route_data via
// insertRoutes, and the 300s ladder keeps sole ownership of aircraft_data's
// route_processed and route_attempts columns. Nothing is shared between the two
// paths, so they cannot race.
type routeOnSight struct {
	unknownCooldown time.Duration
	errorCooldown   time.Duration

	mu       sync.Mutex
	pending  map[string]bool
	cooldown map[string]time.Time
}

func newRouteOnSight() *routeOnSight {
	return &routeOnSight{
		unknownCooldown: routeUnknownCooldown,
		errorCooldown:   routeErrorCooldown,
		pending:         map[string]bool{},
		cooldown:        map[string]time.Time{},
	}
}

// claim reserves callsign for a lookup, reporting whether the caller now owns
// it.
//
// It must be called synchronously from the tick. A lookup can outlive the 2s
// tick interval, and only an already-updated pending set stops the next tick
// from asking about the same callsign all over again.
func (r *routeOnSight) claim(callsign string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.pending[callsign] {
		return false
	}
	if until, ok := r.cooldown[callsign]; ok && time.Now().Before(until) {
		return false
	}

	r.pruneLocked()
	r.pending[callsign] = true
	return true
}

// release ends the lookup for callsign. found reports whether a route was
// actually stored; when it was not, the callsign goes on the given cooldown so
// the fast path stops asking about it for a while.
func (r *routeOnSight) release(callsign string, found bool, cooldown time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.pending, callsign)

	if found {
		delete(r.cooldown, callsign)
		return
	}
	r.cooldown[callsign] = time.Now().Add(cooldown)
}

// pruneLocked drops expired cooldown entries once the map has grown enough to
// be worth walking. Callers must hold the mutex.
func (r *routeOnSight) pruneLocked() {
	if len(r.cooldown) < routeCooldownPruneAt {
		return
	}

	now := time.Now()
	for callsign, until := range r.cooldown {
		if now.After(until) {
			delete(r.cooldown, callsign)
		}
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$HOME/.local/go/bin:$PATH
cd /mnt/c/temp/github/claude/skystats-route-on-sight
go test ./core/ -run 'TestClaim|TestRelease|TestPrune' -v
```

Expected: PASS, six tests.

- [ ] **Step 5: Commit**

```bash
cd /mnt/c/temp/github/claude/skystats-route-on-sight
git add core/routes-onsight.go core/routes-onsight_test.go
git commit -m "feat: add claim-and-cooldown guard for on-sight route lookups"
```

---

### Task 2: Candidate selection and request adapter

Two pure functions: picking the aircraft that need a route, and reshaping them into what the existing request builder reads.

**Files:**
- Modify: `core/routes-onsight.go`
- Test: `core/routes-onsight_test.go`

**Interfaces:**
- Consumes: `routeOnSight` from Task 1.
- Produces:
  - `func routeCandidates(snapshot []Aircraft, enrichment map[string]aircraftEnrichment) []Aircraft`
  - `func routeLookupSubjects(aircrafts []Aircraft) []Aircraft`

**Background the implementer needs:** `aircraftEnrichment` is declared at `core/current-sightings.go:44`; its `OriginIcao` and `DestinationIcao` fields are `*string`, nil when no route row matched. `Aircraft` is declared in `core/models.go`: `Lat`/`Lon` are `float64` from the live feed, while `LastSeenLat`/`LastSeenLon` are `sql.NullFloat64` and are populated only by the database path. `buildRouteApiRequestBody` (`core/routes.go:383`) silently skips any aircraft whose `LastSeenLat`/`LastSeenLon` are not `Valid` — which is why the adapter exists. Without it the feature would build an empty request body and quietly do nothing.

- [ ] **Step 1: Write the failing tests**

Append to `core/routes-onsight_test.go`:

```go
func TestRouteCandidatesSelectsAircraftWithoutRoute(t *testing.T) {
	snapshot := []Aircraft{{Hex: "abc123", Flight: "SAS1456"}}

	got := routeCandidates(snapshot, map[string]aircraftEnrichment{"abc123": {}})

	if len(got) != 1 || got[0].Flight != "SAS1456" {
		t.Fatalf("got %+v, want the single aircraft that has no route", got)
	}
}

func TestRouteCandidatesSkipsAircraftThatAlreadyHaveARoute(t *testing.T) {
	destination := "ESGG"
	snapshot := []Aircraft{{Hex: "abc123", Flight: "RYR4TR"}}
	enrichment := map[string]aircraftEnrichment{
		"abc123": {DestinationIcao: &destination},
	}

	if got := routeCandidates(snapshot, enrichment); len(got) != 0 {
		t.Fatalf("selected %d candidates, want 0 — the route is already known", len(got))
	}
}

func TestRouteCandidatesSkipsEmptyCallsigns(t *testing.T) {
	snapshot := []Aircraft{{Hex: "abc123", Flight: ""}}

	if got := routeCandidates(snapshot, map[string]aircraftEnrichment{}); len(got) != 0 {
		t.Fatalf("selected %d candidates, want 0 — there is no callsign to look up", len(got))
	}
}

func TestRouteCandidatesDeduplicatesCallsigns(t *testing.T) {
	snapshot := []Aircraft{
		{Hex: "abc123", Flight: "SAS1456"},
		{Hex: "def456", Flight: "SAS1456"},
	}

	got := routeCandidates(snapshot, map[string]aircraftEnrichment{})

	if len(got) != 1 {
		t.Fatalf("selected %d candidates, want 1 — one callsign is asked about once", len(got))
	}
}

func TestRouteLookupSubjectsCopiesLivePositionIntoLastSeen(t *testing.T) {
	subjects := routeLookupSubjects([]Aircraft{{Flight: "SAS1456", Lat: 57.1, Lon: 12.28}})

	if len(subjects) != 1 {
		t.Fatalf("got %d subjects, want 1", len(subjects))
	}
	if !subjects[0].LastSeenLat.Valid || subjects[0].LastSeenLat.Float64 != 57.1 {
		t.Errorf("LastSeenLat = %+v, want 57.1 marked valid — buildRouteApiRequestBody reads it", subjects[0].LastSeenLat)
	}
	if !subjects[0].LastSeenLon.Valid || subjects[0].LastSeenLon.Float64 != 12.28 {
		t.Errorf("LastSeenLon = %+v, want 12.28 marked valid", subjects[0].LastSeenLon)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$HOME/.local/go/bin:$PATH
cd /mnt/c/temp/github/claude/skystats-route-on-sight
go test ./core/ -run 'TestRouteCandidates|TestRouteLookupSubjects' -v
```

Expected: FAIL — `undefined: routeCandidates`, `undefined: routeLookupSubjects`.

- [ ] **Step 3: Write the implementation**

Add `"database/sql"` to the import block in `core/routes-onsight.go`, then append:

```go
// routeCandidates returns the aircraft in a snapshot whose callsign has no
// route yet, one entry per callsign.
//
// The enrichment map is the one Current Sightings already fetched for this
// tick, so spotting a missing route costs no extra query. A route counts as
// missing only when both airport ICAO codes are absent: insertRoutes never
// stores a row without a resolved pair of airports, but an individual IATA code
// can be blank for a minor field.
func routeCandidates(snapshot []Aircraft, enrichment map[string]aircraftEnrichment) []Aircraft {
	var candidates []Aircraft
	seen := map[string]bool{}

	for _, a := range snapshot {
		if a.Flight == "" || seen[a.Flight] {
			continue
		}

		e := enrichment[a.Hex]
		if e.OriginIcao != nil || e.DestinationIcao != nil {
			continue
		}

		seen[a.Flight] = true
		candidates = append(candidates, a)
	}

	return candidates
}

// routeLookupSubjects copies each aircraft's live position into the fields the
// request builder reads.
//
// buildRouteApiRequestBody takes its position from LastSeenLat/LastSeenLon and
// skips any aircraft where they are not valid. Only the database path fills
// those in, so a snapshot straight off the feed would produce an empty request.
func routeLookupSubjects(aircrafts []Aircraft) []Aircraft {
	subjects := make([]Aircraft, 0, len(aircrafts))

	for _, a := range aircrafts {
		a.LastSeenLat = sql.NullFloat64{Float64: a.Lat, Valid: true}
		a.LastSeenLon = sql.NullFloat64{Float64: a.Lon, Valid: true}
		subjects = append(subjects, a)
	}

	return subjects
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$HOME/.local/go/bin:$PATH
cd /mnt/c/temp/github/claude/skystats-route-on-sight
go test ./core/ -run 'TestRouteCandidates|TestRouteLookupSubjects' -v
```

Expected: PASS, five tests.

- [ ] **Step 5: Commit**

```bash
cd /mnt/c/temp/github/claude/skystats-route-on-sight
git add core/routes-onsight.go core/routes-onsight_test.go
git commit -m "feat: select callsigns needing a route and adapt them for lookup"
```

---

### Task 3: Wire the lookup into the 2s tick

Joins the pieces to the live path: select and claim synchronously, fetch on a goroutine, release with the right cooldown.

**Files:**
- Modify: `core/routes-onsight.go`
- Modify: `core/aircraft.go:58`
- Modify: `core/core.go:98`

**Interfaces:**
- Consumes: `routeOnSight`, `claim`, `release`, `routeFetcher`, `routeCandidates`, `routeLookupSubjects` from Tasks 1–2. From existing code: `getRoutes(aircrafts []Aircraft) ([]RouteInfo, error)` (`core/routes.go:192`) and `insertRoutes(pg *postgres, routes []RouteInfo) map[string]bool` (`core/routes.go:236`), which returns the set of callsigns it actually stored.
- Produces: `func requestMissingRoutes(pg *postgres, snapshot []Aircraft, enrichment map[string]aircraftEnrichment)`, called once per tick.

**This task has no new unit tests.** It is the I/O seam — HTTP and database — which this repo has no harness for. It is verified by the build, by the existing suite staying green, and by the runtime check in Task 4.

- [ ] **Step 1: Add the wiring functions**

Add `"github.com/rs/zerolog/log"` to the import block in `core/routes-onsight.go`, then append:

```go
// requestMissingRoutes starts a lookup for every callsign in the snapshot that
// still has no route.
//
// Selection and claiming happen on the caller's goroutine so the pending set is
// up to date before the tick returns. The network call and the database write
// happen on a goroutine of their own, because the lookup's 5s timeout is longer
// than the 2s tick it would otherwise block.
func requestMissingRoutes(pg *postgres, snapshot []Aircraft, enrichment map[string]aircraftEnrichment) {
	if routeFetcher == nil {
		return
	}

	var claimed []Aircraft
	for _, a := range routeCandidates(snapshot, enrichment) {
		if routeFetcher.claim(a.Flight) {
			claimed = append(claimed, a)
		}
	}

	if len(claimed) == 0 {
		return
	}

	go fetchRoutesOnSight(pg, claimed)
}

// fetchRoutesOnSight asks the route API about the claimed aircraft in one
// request and stores whatever comes back.
//
// Every claim is released on the way out, whichever path is taken, so a failure
// can never leave a callsign reserved for good.
func fetchRoutesOnSight(pg *postgres, claimed []Aircraft) {
	matched := map[string]bool{}
	cooldown := routeFetcher.unknownCooldown

	defer func() {
		for _, a := range claimed {
			routeFetcher.release(a.Flight, matched[a.Flight], cooldown)
		}
	}()

	routes, err := getRoutes(routeLookupSubjects(claimed))
	if err != nil {
		// The network failed, which says nothing about these callsigns, so they
		// go on the short cooldown rather than the long one.
		cooldown = routeFetcher.errorCooldown
		log.Warn().Err(err).Int("callsigns", len(claimed)).Msg("fetchRoutesOnSight() - route lookup failed")
		return
	}

	matched = insertRoutes(pg, routes)
}
```

- [ ] **Step 2: Call it from the tick**

In `core/aircraft.go`, inside `updateAircraftDatabase`, the enrichment block currently reads:

```go
	// One enrichment round trip per tick, shared by every consumer of the
	// snapshot.
	enrichment := enrichAircraftSnapshot(pg, aircraftsInRange)
	refreshCurrentSightings(response.Now, aircraftsInRange, enrichment)
	evaluateWatches(pg, aircraftsInRange, enrichment)
```

Replace it with:

```go
	// One enrichment round trip per tick, shared by every consumer of the
	// snapshot.
	enrichment := enrichAircraftSnapshot(pg, aircraftsInRange)
	requestMissingRoutes(pg, aircraftsInRange, enrichment)
	refreshCurrentSightings(response.Now, aircraftsInRange, enrichment)
	evaluateWatches(pg, aircraftsInRange, enrichment)
```

- [ ] **Step 3: Create the singleton at startup**

In `core/core.go`, line 98 currently reads:

```go
	notifier = NewNotificationService(pg)
```

Make it:

```go
	notifier = NewNotificationService(pg)
	routeFetcher = newRouteOnSight()
```

- [ ] **Step 4: Build and run the whole suite**

```bash
export PATH=$HOME/.local/go/bin:$PATH
cd /mnt/c/temp/github/claude/skystats-route-on-sight
go build ./... && go test ./...
```

Expected: build succeeds; `ok github.com/tomcarman/skystats/core`. No test may regress.

- [ ] **Step 5: Vet the race-sensitive code**

```bash
export PATH=$HOME/.local/go/bin:$PATH
cd /mnt/c/temp/github/claude/skystats-route-on-sight
go vet ./... && go test ./core/ -race -run 'TestClaim|TestRelease|TestPrune|TestRouteCandidates|TestRouteLookupSubjects'
```

Expected: `go vet` silent; race detector reports nothing.

- [ ] **Step 6: Commit**

```bash
cd /mnt/c/temp/github/claude/skystats-route-on-sight
git add core/routes-onsight.go core/aircraft.go core/core.go
git commit -m "feat: fetch routes when a callsign first appears in the tick"
```

---

### Task 4: Verify against the live receiver

The measurement that decides whether this worked. The spec's baseline is **66.7%** of flights (50 of 75 over 12 hours) getting their route before the aircraft left view.

**Files:** none — this task changes no code.

**Interfaces:** none.

> **Stop here for sign-off.** Steps 2 onwards touch the live receiver at 192.168.1.251 and rebuild the running stack. Do not deploy without the user explicitly approving this run. Tasks 1–3 are complete and reviewable on their own.

**Deploy notes:** the stack runs at `root@192.168.1.251` in `/opt/skystats`. Disk there is small; clear the build cache if a build fails with `no space left on device`. Deploying replaces the server's checkout with the branch's tracked files — confirm `main` has not moved ahead of this branch before shipping, or the deploy silently reverts newer work.

- [ ] **Step 1: Confirm the branch is not behind main**

Check **local** `main`, not just `origin/main`. This repo merges to local main and pushes later, so `origin/main` can lag far behind what is actually current — on 2026-08-05 local main was 13 commits ahead of origin while `origin/main` still matched this branch's base. Deploying ships a tar of tracked files and would silently revert anything newer.

```bash
cd /mnt/c/temp/github/claude/skystats-route-on-sight
git fetch origin
echo "--- behind local main ---"; git log --oneline feat/route-on-sight..main
echo "--- behind origin/main ---"; git log --oneline feat/route-on-sight..origin/main
```

Expected: both empty. Anything listed must be merged into this branch before deploying.

- [ ] **Step 2: Deploy**

```bash
cd /mnt/c/temp/github/claude/skystats-route-on-sight
git archive --format=tar HEAD | ssh root@192.168.1.251 'tar -x -C /opt/skystats'
ssh root@192.168.1.251 'cd /opt/skystats && docker builder prune -f && docker compose up -d --build'
```

- [ ] **Step 3: Confirm the daemon came up**

```bash
ssh root@192.168.1.251 'docker ps --format "{{.Names}}\t{{.Status}}"; docker logs skystats --since 3m 2>&1 | tail -20'
```

Expected: `skystats` up, no repeated errors, no `fetchRoutesOnSight() - route lookup failed` storm.

- [ ] **Step 4: Watch a real aircraft get its route**

```bash
ssh root@192.168.1.251 "docker exec skystats-db psql -U skystats-user -d skystats_db -c \"
SELECT ad.flight, ad.first_seen, rd.last_updated,
       EXTRACT(EPOCH FROM (rd.last_updated - ad.first_seen))::int AS lag_secs
FROM aircraft_data ad JOIN route_data rd ON rd.route_callsign = ad.flight
WHERE ad.first_seen > NOW() - INTERVAL '30 minutes'
ORDER BY ad.first_seen DESC;\""
```

Expected: `lag_secs` in the single digits, against a pre-change average of 166.

- [ ] **Step 5: Re-measure the headline number after 12 hours**

```bash
ssh root@192.168.1.251 "docker exec skystats-db psql -U skystats-user -d skystats_db -c \"
SELECT count(*) AS flights_with_route,
       count(*) FILTER (WHERE rd.last_updated <= ad.last_seen) AS route_in_time,
       round(100.0*count(*) FILTER (WHERE rd.last_updated <= ad.last_seen)/count(*),1) AS pct_in_time,
       round(avg(EXTRACT(EPOCH FROM (rd.last_updated - ad.first_seen)))::numeric,0) AS avg_lag_secs
FROM aircraft_data ad JOIN route_data rd ON rd.route_callsign = ad.flight
WHERE ad.first_seen > NOW() - INTERVAL '12 hours'
  AND ad.flight IN (SELECT flight FROM aircraft_data
                    WHERE first_seen > NOW() - INTERVAL '12 hours'
                    GROUP BY flight HAVING count(*)=1);\""
```

Expected: `pct_in_time` well above the 66.7% baseline, approaching 100% for callsigns the source knows at all. `avg_lag_secs` should fall from 166 to single digits.

Report the actual numbers. If `pct_in_time` did not improve, do not paper over it — the change did not work and needs investigation.

---

## Notes for the implementer

**What "done" does not mean.** Callsigns the route source has never heard of — ad hoc charter, air ambulance, most general aviation — will still show no route, however fast the lookup runs. That is a coverage limit upstream, not a defect in this work. Verified live on 2026-08-05: `CAT250` returns `unknown` from adsb.im, adsbdb.com and adsb.lol alike. Judge this change on latency for callsigns that resolve at all.

**The modal lags one more step.** Aircraft detail reads a snapshot in `flight_history` written by the distance-stats job on its own 300s ticker, not `route_data` directly. It should improve from roughly ten minutes to a couple of minutes as a side effect, but making it immediate is deliberately out of scope — see the spec.
