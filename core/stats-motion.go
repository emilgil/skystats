package main

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
)

// runningMaxActiveWindow is how long after its last reception a flight is
// still treated as in progress. updateStatisticsTicker fires every 120s, so
// this leaves room for a tick or two of slack without holding flights that
// have genuinely ended open.
const runningMaxActiveWindow = 5 * time.Minute

// shouldRefreshRunningMax reports whether a fastest/highest pass should write
// this aircraft.
//
// aircraft_data.gs and alt_baro only ever grow within a session (see
// updateExistingAircrafts), so they already track the true session maximum.
// Writing a flight once and marking it processed froze the record at whatever
// the first tick after it appeared happened to catch. A flight that is still
// being received is therefore rewritten every tick; once it goes quiet its
// maximum has settled and the processed flag is left to do its job.
func shouldRefreshRunningMax(processed bool, lastSeen, now time.Time) bool {
	return !processed || now.Sub(lastSeen) <= runningMaxActiveWindow
}

func updateMeasurementStatistics(pg *postgres) {

	aircrafts := getAircraftsForMeasurementStatistics(pg)

	updateLowestAircraft(pg, aircrafts)
	updateFastestAircraft(pg, aircrafts)
	updateHighestAircraft(pg, aircrafts)
	updateSlowestAircraft(pg, aircrafts)

}

func updateLowestAircraft(pg *postgres, aircrafts []Aircraft) {
	var toProcess []Aircraft
	for _, a := range aircrafts {
		if !a.LowestProcessed {
			toProcess = append(toProcess, a)
		}
	}
	if len(toProcess) == 0 {
		return
	}

	var candidates []recordCandidate
	for _, a := range toProcess {
		if a.AltBaro < 1 { // validity: lowest needs a real altitude
			continue
		}
		upsertFlightHistory(pg, a.Hex, a.Flight, a.R, a.T, a.FirstSeen, a.LastSeen,
			map[string]any{"barometric_altitude": a.AltBaro, "geometric_altitude": a.AltGeom})
		candidates = append(candidates, recordCandidate{
			Hex: a.Hex, Flight: a.Flight, Registration: a.R, Type: a.T,
			FirstSeen: a.FirstSeen, LastSeen: a.LastSeen,
			MetricValue: float64(a.AltBaro),
			Details:     map[string]any{"geometric_altitude": a.AltGeom},
		})
	}
	writeRecords(pg, "lowest", candidates)
	MarkProcessed(pg, "lowest_aircraft_processed", toProcess)
}

func updateHighestAircraft(pg *postgres, aircrafts []Aircraft) {
	now := time.Now()
	var toProcess []Aircraft
	for _, a := range aircrafts {
		if shouldRefreshRunningMax(a.HighestProcessed, a.LastSeen, now) {
			toProcess = append(toProcess, a)
		}
	}
	if len(toProcess) == 0 {
		return
	}

	var candidates []recordCandidate
	for _, a := range toProcess {
		if a.AltBaro < 1 {
			continue
		}
		upsertFlightHistory(pg, a.Hex, a.Flight, a.R, a.T, a.FirstSeen, a.LastSeen,
			map[string]any{"barometric_altitude": a.AltBaro, "geometric_altitude": a.AltGeom})
		candidates = append(candidates, recordCandidate{
			Hex: a.Hex, Flight: a.Flight, Registration: a.R, Type: a.T,
			FirstSeen: a.FirstSeen, LastSeen: a.LastSeen,
			MetricValue: float64(a.AltBaro),
			Details:     map[string]any{"geometric_altitude": a.AltGeom},
		})
	}
	writeRecords(pg, "highest", candidates)
	MarkProcessed(pg, "highest_aircraft_processed", toProcess)
}

func updateSlowestAircraft(pg *postgres, aircrafts []Aircraft) {
	var toProcess []Aircraft
	for _, a := range aircrafts {
		if !a.SlowestProcessed {
			toProcess = append(toProcess, a)
		}
	}
	if len(toProcess) == 0 {
		return
	}

	var candidates []recordCandidate
	for _, a := range toProcess {
		if a.Gs < 1 { // validity: slowest needs a real, non-zero groundspeed
			continue
		}
		upsertFlightHistory(pg, a.Hex, a.Flight, a.R, a.T, a.FirstSeen, a.LastSeen,
			map[string]any{"ground_speed": a.Gs, "indicated_air_speed": a.Ias, "true_air_speed": a.Tas})
		candidates = append(candidates, recordCandidate{
			Hex: a.Hex, Flight: a.Flight, Registration: a.R, Type: a.T,
			FirstSeen: a.FirstSeen, LastSeen: a.LastSeen,
			MetricValue: a.Gs,
			Details:     map[string]any{"indicated_air_speed": a.Ias, "true_air_speed": a.Tas},
		})
	}
	writeRecords(pg, "slowest", candidates)
	MarkProcessed(pg, "slowest_aircraft_processed", toProcess)
}

func updateFastestAircraft(pg *postgres, aircrafts []Aircraft) {
	now := time.Now()
	var toProcess []Aircraft
	for _, a := range aircrafts {
		if shouldRefreshRunningMax(a.FastestProcessed, a.LastSeen, now) {
			toProcess = append(toProcess, a)
		}
	}
	if len(toProcess) == 0 {
		return
	}

	var candidates []recordCandidate
	for _, a := range toProcess {
		if a.Gs < 1 {
			continue
		}
		upsertFlightHistory(pg, a.Hex, a.Flight, a.R, a.T, a.FirstSeen, a.LastSeen,
			map[string]any{"ground_speed": a.Gs, "indicated_air_speed": a.Ias, "true_air_speed": a.Tas})
		candidates = append(candidates, recordCandidate{
			Hex: a.Hex, Flight: a.Flight, Registration: a.R, Type: a.T,
			FirstSeen: a.FirstSeen, LastSeen: a.LastSeen,
			MetricValue: a.Gs,
			Details:     map[string]any{"indicated_air_speed": a.Ias, "true_air_speed": a.Tas},
		})
	}
	writeRecords(pg, "fastest", candidates)
	MarkProcessed(pg, "fastest_aircraft_processed", toProcess)
}

func getAircraftsForMeasurementStatistics(pg *postgres) []Aircraft {

	// Still-active flights are fetched even when every flag is set, so
	// fastest/highest can keep following a session maximum that is still
	// climbing (see shouldRefreshRunningMax). slowest/lowest filter on their
	// own processed flag and so ignore the extra rows. The interval is derived
	// from runningMaxActiveWindow so the two cannot drift apart.
	query := fmt.Sprintf(`SELECT id, hex, flight, r, t, first_seen, last_seen, alt_baro, alt_geom, gs, ias, tas,
				lowest_aircraft_processed, highest_aircraft_processed, fastest_aircraft_processed, slowest_aircraft_processed
				FROM aircraft_data
				WHERE lowest_aircraft_processed = false OR
					highest_aircraft_processed = false OR
					fastest_aircraft_processed = false OR
					slowest_aircraft_processed = false OR
					last_seen > NOW() - INTERVAL '%d seconds'`,
		int(runningMaxActiveWindow.Seconds()))

	rows, err := pg.db.Query(context.Background(), query)
	if err != nil {
		log.Error().Err(err).Msg("getAircraftsForMeasurementStatistics() - Error querying db")
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
			&aircraft.AltBaro,
			&aircraft.AltGeom,
			&aircraft.Gs,
			&aircraft.Ias,
			&aircraft.Tas,
			&aircraft.LowestProcessed,
			&aircraft.HighestProcessed,
			&aircraft.FastestProcessed,
			&aircraft.SlowestProcessed)

		if err != nil {
			log.Error().Err(err).Msg("getAircraftsForMeasurementStatistics() - Error scanning rows")
			return nil
		}
		aircrafts = append(aircrafts, aircraft)
	}

	log.Debug().Msgf("Aircrafts that have not have statistics processed: %d", len(aircrafts))
	return aircrafts
}
