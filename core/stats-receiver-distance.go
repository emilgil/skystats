package main

import (
	"context"

	"github.com/rs/zerolog/log"
)

// updateReceiverDistanceStatistics finalizes Nearest/Furthest for flights
// that are both unprocessed and truly over. Unlike every other category's
// ticker (stats-motion.go, stats-distance.go), which processes a row the
// moment it sees processed=false regardless of whether the aircraft is
// still being tracked, this one additionally requires last_seen to be 10+
// minutes old — the same boundary aircraft.go already uses elsewhere to
// decide a hex's next sighting starts a new flight. Without that gate, the
// running min/max accumulated so far in aircraft_data (updated every 2s in
// updateExistingAircrafts) could be finalized before the flight is actually
// done, silently discarding a more extreme value the flight would still go
// on to set.
func updateReceiverDistanceStatistics(pg *postgres) {
	aircrafts := getAircraftsForReceiverDistanceStatistics(pg)
	if len(aircrafts) == 0 {
		return
	}
	updateNearestAircraft(pg, aircrafts)
	updateFurthestRangeAircraft(pg, aircrafts)
}

func getAircraftsForReceiverDistanceStatistics(pg *postgres) []Aircraft {
	query := `SELECT id, hex, flight, r, t, first_seen, last_seen,
				min_distance_receiver, min_distance_receiver_altitude, min_distance_receiver_bearing,
				max_distance_receiver, max_distance_receiver_altitude, max_distance_receiver_bearing,
				nearest_processed, furthest_processed
				FROM aircraft_data
				WHERE last_seen < now() - interval '10 minutes'
					AND (nearest_processed = false OR furthest_processed = false)`

	rows, err := pg.db.Query(context.Background(), query)
	if err != nil {
		log.Error().Err(err).Msg("getAircraftsForReceiverDistanceStatistics() - Error querying db")
		return nil
	}
	defer rows.Close()

	var aircrafts []Aircraft
	for rows.Next() {
		var aircraft Aircraft
		err := rows.Scan(
			&aircraft.Id,
			&aircraft.Hex,
			&aircraft.Flight,
			&aircraft.R,
			&aircraft.T,
			&aircraft.FirstSeen,
			&aircraft.LastSeen,
			&aircraft.MinDistanceReceiver,
			&aircraft.MinDistanceReceiverAltitude,
			&aircraft.MinDistanceReceiverBearing,
			&aircraft.MaxDistanceReceiver,
			&aircraft.MaxDistanceReceiverAltitude,
			&aircraft.MaxDistanceReceiverBearing,
			&aircraft.NearestProcessed,
			&aircraft.FurthestProcessed)
		if err != nil {
			log.Error().Err(err).Msg("getAircraftsForReceiverDistanceStatistics() - Error scanning rows")
			continue
		}
		aircrafts = append(aircrafts, aircraft)
	}

	log.Debug().Msgf("Receiver-distance stats: %d stale unprocessed flights", len(aircrafts))
	return aircrafts
}

func updateNearestAircraft(pg *postgres, aircrafts []Aircraft) {
	var toProcess []Aircraft
	for _, a := range aircrafts {
		if !a.NearestProcessed {
			toProcess = append(toProcess, a)
		}
	}
	if len(toProcess) == 0 {
		return
	}

	var candidates []recordCandidate
	for _, a := range toProcess {
		if !a.MinDistanceReceiver.Valid { // validity: never actually got a position tick
			continue
		}
		upsertFlightHistory(pg, a.Hex, a.Flight, a.R, a.T, a.FirstSeen, a.LastSeen, map[string]any{
			"min_distance_receiver":          a.MinDistanceReceiver.Float64,
			"min_distance_receiver_altitude": a.MinDistanceReceiverAltitude,
			"min_distance_receiver_bearing":  a.MinDistanceReceiverBearing,
		})
		candidates = append(candidates, recordCandidate{
			Hex: a.Hex, Flight: a.Flight, Registration: a.R, Type: a.T,
			FirstSeen: a.FirstSeen, LastSeen: a.LastSeen,
			MetricValue: a.MinDistanceReceiver.Float64,
			Details: map[string]any{
				"min_distance_receiver_altitude": a.MinDistanceReceiverAltitude,
				"min_distance_receiver_bearing":  a.MinDistanceReceiverBearing,
			},
		})
	}
	writeRecords(pg, "nearest", candidates)
	MarkProcessed(pg, "nearest_processed", toProcess)
}

func updateFurthestRangeAircraft(pg *postgres, aircrafts []Aircraft) {
	var toProcess []Aircraft
	for _, a := range aircrafts {
		if !a.FurthestProcessed {
			toProcess = append(toProcess, a)
		}
	}
	if len(toProcess) == 0 {
		return
	}

	var candidates []recordCandidate
	for _, a := range toProcess {
		if !a.MaxDistanceReceiver.Valid {
			continue
		}
		upsertFlightHistory(pg, a.Hex, a.Flight, a.R, a.T, a.FirstSeen, a.LastSeen, map[string]any{
			"max_distance_receiver":          a.MaxDistanceReceiver.Float64,
			"max_distance_receiver_altitude": a.MaxDistanceReceiverAltitude,
			"max_distance_receiver_bearing":  a.MaxDistanceReceiverBearing,
		})
		candidates = append(candidates, recordCandidate{
			Hex: a.Hex, Flight: a.Flight, Registration: a.R, Type: a.T,
			FirstSeen: a.FirstSeen, LastSeen: a.LastSeen,
			MetricValue: a.MaxDistanceReceiver.Float64,
			Details: map[string]any{
				"max_distance_receiver_altitude": a.MaxDistanceReceiverAltitude,
				"max_distance_receiver_bearing":  a.MaxDistanceReceiverBearing,
			},
		})
	}
	writeRecords(pg, "furthest_range", candidates)
	MarkProcessed(pg, "furthest_processed", toProcess)
}
