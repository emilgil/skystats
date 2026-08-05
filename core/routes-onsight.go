package main

import (
	"database/sql"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
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

	// maxRouteOnSightBatch mirrors the 100-callsign cap updateRoutes applies
	// in routes.go. The claim guard normally damps the fast path to a
	// trickle, but on first start against an empty route_data table every
	// aircraft in range can be a candidate on the very first tick, so the cap
	// must be enforced here too — before claiming, so a dropped callsign
	// never leaks a claim it will now never be released from.
	maxRouteOnSightBatch = 100
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

// routeCandidates returns the aircraft in a snapshot whose callsign has no
// route yet, one entry per callsign, capped at the same 100-callsign batch
// size the 300s ladder enforces in routes.go.
//
// The enrichment map is the one Current Sightings already fetched for this
// tick, so spotting a missing route costs no extra query. A route counts as
// missing only when both airport ICAO codes are absent: insertRoutes never
// stores a row without a resolved pair of airports, but an individual IATA code
// can be blank for a minor field.
//
// enrichAircraftSnapshot returns an empty map on query failure rather than
// its usual per-hex rows, and that is indistinguishable here from "every
// aircraft is routeless" unless we check for it explicitly.
// fetchAircraftEnrichment builds its result with unnest, so a successful
// query always yields exactly one row per hex passed in; an empty map next to
// a non-empty snapshot can therefore only mean the query failed. Selecting
// every callsign as a candidate in that case would turn a single failed
// enrichment query into a full-snapshot route lookup on every following
// tick, since insertRoutes keeps "matching" already-known routes and the
// claim guard's cooldown is deleted on every match. Do nothing this tick and
// let the next tick's enrichment try again instead — do not "simplify" this
// away by trusting an empty map.
func routeCandidates(snapshot []Aircraft, enrichment map[string]aircraftEnrichment) []Aircraft {
	if len(snapshot) > 0 && len(enrichment) == 0 {
		return nil
	}

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

		if len(candidates) == maxRouteOnSightBatch {
			break
		}
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

	log.Info().Int("requested", len(claimed)).Int("matched", len(matched)).Msg("fetchRoutesOnSight() - on-sight route lookup complete")
}
