package main

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

// watchMatchGrace is how long a match survives without being re-confirmed
// before it is considered over. It mirrors the 10-minute window
// getAircraftsRecentlySeen uses to decide that a new aircraft_data row is a new
// sighting, so "new sighting" and "new notification" agree, and it absorbs the
// occasional tick where readsb drops an aircraft from the feed.
const watchMatchGrace = 10 * time.Minute

// diffMatches compares this tick's match set against the previous state.
// started is everything newly matching (one notification each); ended is
// everything that has not been re-confirmed within grace.
func diffMatches(current map[watchKey]bool, previous map[watchKey]time.Time, now time.Time, grace time.Duration) (started, ended []watchKey) {

	for key := range current {
		if _, active := previous[key]; !active {
			started = append(started, key)
		}
	}

	for key, lastMatched := range previous {
		if current[key] {
			continue
		}
		if now.Sub(lastMatched) > grace {
			ended = append(ended, key)
		}
	}

	return started, ended
}

// buildWatchSubject flattens one aircraft into the value set conditions are
// evaluated against. Live readsb values win over database enrichment, which is
// only a fallback for what the feed does not carry.
func buildWatchSubject(a Aircraft, e aircraftEnrichment, distanceKm float64, hasPosition, firstSeenEver bool) watchSubject {

	s := watchSubject{
		Hex:             a.Hex,
		Callsign:        a.Flight,
		Registration:    firstNonEmpty(a.R, stringValue(e.Registration)),
		TypeCode:        firstNonEmpty(a.T, stringValue(e.IcaoType)),
		Model:           firstNonEmpty(a.Desc, stringValue(e.AircraftType)),
		Manufacturer:    stringValue(e.Manufacturer),
		Country:         stringValue(e.CountryName),
		Airline:         stringValue(e.AirlineName),
		Squawk:          a.Squawk,
		DistanceKm:      distanceKm,
		HasPosition:     hasPosition,
		AltitudeFt:      float64(a.AltBaro),
		HasAltitude:     a.AltBaro != 0,
		SpeedKt:         a.Gs,
		HasSpeed:        a.Gs != 0,
		VerticalRateFpm: float64(a.BaroRate),
		FirstSeenEver:   firstSeenEver,
	}

	s.AirlineCodes = nonEmptyValues(stringValue(e.AirlineIcao), stringValue(e.AirlineIata))
	s.Origin = nonEmptyValues(stringValue(e.OriginIcao), stringValue(e.OriginIata))
	s.Destination = nonEmptyValues(stringValue(e.DestinationIcao), stringValue(e.DestinationIata))

	return s
}

func nonEmptyValues(values ...string) []string {
	var out []string
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// initWatchEngine primes the in-memory match state from Postgres so a restart
// does not re-notify for aircraft that were already matching.
func initWatchEngine(pg *postgres) {
	activeMatchCache.load(pg, time.Now())
}

// firstSeen is the process-wide first-sighting tracker, driven by the ingest
// tick.
var firstSeen = newFirstSeenTracker()

// evaluateWatches matches the current readsb snapshot against every enabled
// watch and fires one notification per match that starts. Called from the 2s
// ingest tick with the snapshot and enrichment it has already fetched.
func evaluateWatches(pg *postgres, aircraft []Aircraft, enrichment map[string]aircraftEnrichment) {

	watches := watchCache.enabled(pg)
	now := time.Now()

	hexes := make([]string, 0, len(aircraft))
	for _, a := range aircraft {
		hexes = append(hexes, a.Hex)
	}

	// known_aircraft must be maintained even with no watches configured, so
	// first_seen_ever is correct for whatever the user creates later.
	brandNew := markKnownAircraft(pg, hexes)
	firstSeenNow := firstSeen.update(hexes, brandNew, now, watchMatchGrace)

	if len(watches) == 0 {
		return
	}

	subjects := make(map[string]watchSubject, len(aircraft))
	for _, a := range aircraft {
		hasPosition := a.Lat != 0 || a.Lon != 0
		distance := 0.0
		if hasPosition {
			distance = *getDistance([]float64{a.Lon, a.Lat})
		}
		subjects[a.Hex] = buildWatchSubject(a, enrichment[a.Hex], distance, hasPosition, firstSeenNow[a.Hex])
	}

	current := map[watchKey]bool{}
	for _, w := range watches {
		for hex, subject := range subjects {
			if matchWatch(w, subject) {
				current[watchKey{WatchID: w.ID, Hex: hex}] = true
			}
		}
	}

	previous := activeMatchCache.snapshot()
	started, ended := diffMatches(current, previous, now, watchMatchGrace)

	startedSet := make(map[watchKey]bool, len(started))
	for _, key := range started {
		startedSet[key] = true
	}

	// The cache must only ever reflect what Postgres actually holds, so it is
	// built up from confirmed writes below rather than from `current`
	// directly. Continuing matches (in current but not newly started) already
	// have their row in Postgres from an earlier tick, so they are seeded in
	// unconditionally; their timestamp is simply refreshed by apply().
	//
	// A started match is added only once its INSERT has succeeded, and only
	// then is the notification fired. If the INSERT fails, the key is left
	// out of both the cache and the notification for this tick: the next tick
	// still finds it missing from the cache, so diffMatches reports it as
	// started again and retries the INSERT. That is the only way to avoid
	// notifying for a match Postgres never durably recorded, while never
	// leaving a real match un-notified for good.
	//
	// Symmetrically, an ended match is only dropped from the cache once its
	// DELETE has succeeded; a failed DELETE leaves the key exactly where it
	// was, so the row (and the cache) still agree and the next tick retries.
	persisted := make(map[watchKey]bool, len(current))
	for key := range current {
		if !startedSet[key] {
			persisted[key] = true
		}
	}

	watchByID := make(map[int]Watch, len(watches))
	for _, w := range watches {
		watchByID[w.ID] = w
	}

	for _, key := range started {
		w, ok := watchByID[key.WatchID]
		if !ok {
			continue
		}
		subject := subjects[key.Hex]

		_, err := pg.db.Exec(context.Background(), `
			INSERT INTO watch_active_matches (watch_id, hex, matched_at)
			VALUES ($1, $2, $3)
			ON CONFLICT (watch_id, hex) DO NOTHING`, key.WatchID, key.Hex, now)
		if err != nil {
			log.Error().Err(err).Msgf("evaluateWatches() - unable to record match for watch %d / %s", key.WatchID, key.Hex)
			continue
		}

		persisted[key] = true
		log.Info().Msgf("Watch %q matched %s", w.Name, key.Hex)

		if notifier != nil {
			go notifier.NotifyWatch(w, subject)
		}
	}

	removed := make([]watchKey, 0, len(ended))
	for _, key := range ended {
		_, err := pg.db.Exec(context.Background(), `
			DELETE FROM watch_active_matches WHERE watch_id = $1 AND hex = $2`, key.WatchID, key.Hex)
		if err != nil {
			log.Error().Err(err).Msgf("evaluateWatches() - unable to clear match for watch %d / %s", key.WatchID, key.Hex)
			continue
		}
		removed = append(removed, key)
	}

	activeMatchCache.apply(persisted, removed, now)
}
