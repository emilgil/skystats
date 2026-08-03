package main

import (
	"context"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// markKnownAircraft records every hex in the snapshot in the permanent
// known_aircraft archive and returns the set that had never been seen before.
// known_aircraft is deliberately independent of aircraft_data and
// flight_history so retention on those tables can never resurrect a hex as
// "first ever seen".
func markKnownAircraft(pg *postgres, hexes []string) map[string]bool {

	brandNew := map[string]bool{}

	// Filter out empty/whitespace hexes to avoid junk rows in permanent table.
	filtered := make([]string, 0, len(hexes))
	for _, hex := range hexes {
		if len(strings.TrimSpace(hex)) > 0 {
			filtered = append(filtered, hex)
		}
	}

	if len(filtered) == 0 {
		return brandNew
	}

	rows, err := pg.db.Query(context.Background(), `
		INSERT INTO known_aircraft (hex)
		SELECT DISTINCT unnest($1::text[])
		ON CONFLICT (hex) DO NOTHING
		RETURNING hex`, filtered)
	if err != nil {
		log.Error().Err(err).Msg("markKnownAircraft() - insert failed")
		return brandNew
	}
	defer rows.Close()

	for rows.Next() {
		var hex string
		if err := rows.Scan(&hex); err != nil {
			log.Error().Err(err).Msg("markKnownAircraft() - error scanning rows")
			continue
		}
		brandNew[hex] = true
	}

	if err := rows.Err(); err != nil {
		log.Error().Err(err).Msg("markKnownAircraft() - row iteration failed")
	}

	return brandNew
}

// firstSeenTracker keeps the first_seen_ever flag true for the whole of an
// aircraft's first sighting rather than only the tick it appeared, so the
// condition can be combined with others that become true moments later.
//
// State is in-memory only. A daemon restart during an aircraft's very first
// sighting loses the flag for that sighting; the permanent record in
// known_aircraft is what prevents it from ever being flagged again.
//
// Not safe for concurrent use; must be called only from the ingest tick goroutine.
type firstSeenTracker struct {
	sessions map[string]time.Time
}

func newFirstSeenTracker() *firstSeenTracker {
	return &firstSeenTracker{sessions: map[string]time.Time{}}
}

// update folds this tick's brand-new hexes into the tracked set, refreshes the
// ones still visible, drops the ones absent for longer than grace, and returns
// the hexes currently in their first sighting.
func (t *firstSeenTracker) update(snapshotHexes []string, brandNew map[string]bool, now time.Time, grace time.Duration) map[string]bool {

	for hex := range brandNew {
		t.sessions[hex] = now
	}

	for _, hex := range snapshotHexes {
		if _, tracked := t.sessions[hex]; tracked {
			t.sessions[hex] = now
		}
	}

	current := make(map[string]bool, len(t.sessions))
	for hex, lastSeen := range t.sessions {
		if now.Sub(lastSeen) > grace {
			delete(t.sessions, hex)
			continue
		}
		current[hex] = true
	}

	return current
}
