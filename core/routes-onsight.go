package main

import (
	"database/sql"
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
