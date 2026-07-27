package main

import (
	"context"

	"github.com/rs/zerolog/log"
)

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
	var toProcess []Aircraft
	for _, a := range aircrafts {
		if !a.HighestProcessed {
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
	var toProcess []Aircraft
	for _, a := range aircrafts {
		if !a.FastestProcessed {
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

	query := `SELECT id, hex, flight, r, t, first_seen, last_seen, alt_baro, alt_geom, gs, ias, tas, 
				lowest_aircraft_processed, highest_aircraft_processed, fastest_aircraft_processed, slowest_aircraft_processed
				FROM aircraft_data
				WHERE lowest_aircraft_processed = false OR
					highest_aircraft_processed = false OR
					fastest_aircraft_processed = false OR
					slowest_aircraft_processed = false`

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
