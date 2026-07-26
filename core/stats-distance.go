package main

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

// updateDistanceStatistics scans aircraft_data for rows whose route lookup
// has completed (route_processed = true) but that haven't yet been
// processed for one or more of the three distance leaderboards, computes
// the relevant great-circle distances, and updates the leaderboard tables.
func updateDistanceStatistics(pg *postgres) {

	matched, unmatched := getAircraftsForDistanceStatistics(pg)

	// Aircraft whose callsign never resolved to a route (no route_data row)
	// can never contribute to any of the three leaderboards. Mark them
	// processed now so they're not re-queried on every tick forever.
	if len(unmatched) > 0 {
		MarkProcessed(pg, "furthest_flown_processed", unmatched)
		MarkProcessed(pg, "most_remaining_processed", unmatched)
		MarkProcessed(pg, "longest_route_processed", unmatched)
	}

	if len(matched) == 0 {
		return
	}

	updateFurthestFlownAircraft(pg, matched)
	updateMostRemainingAircraft(pg, matched)
	updateLongestRouteAircraft(pg, matched)
}

// getAircraftsForDistanceStatistics returns aircraft split into two groups:
// matched (has a resolved route_data row, i.e. at least one usable airport
// coordinate) and unmatched (route lookup ran but found nothing).
func getAircraftsForDistanceStatistics(pg *postgres) (matched []Aircraft, unmatched []Aircraft) {

	query := `
		SELECT
			ad.id, ad.hex, ad.flight, ad.r, ad.t, ad.first_seen, ad.last_seen,
			ad.last_seen_lat, ad.last_seen_lon,
			ad.furthest_flown_processed, ad.most_remaining_processed, ad.longest_route_processed,
			rd.origin_icao_code, rd.origin_iata_code, rd.origin_latitude, rd.origin_longitude,
			rd.destination_icao_code, rd.destination_iata_code, rd.destination_latitude, rd.destination_longitude
		FROM aircraft_data ad
		LEFT JOIN route_data rd ON rd.route_callsign = ad.flight
		WHERE ad.route_processed = true
			AND (ad.furthest_flown_processed = false
				OR ad.most_remaining_processed = false
				OR ad.longest_route_processed = false)`

	rows, err := pg.db.Query(context.Background(), query)
	if err != nil {
		log.Error().Err(err).Msg("getAircraftsForDistanceStatistics() - Error querying db")
		return nil, nil
	}
	defer rows.Close()

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
			&aircraft.LastSeenLat,
			&aircraft.LastSeenLon,
			&aircraft.FurthestFlownProcessed,
			&aircraft.MostRemainingProcessed,
			&aircraft.LongestRouteProcessed,
			&aircraft.OriginIcaoCode,
			&aircraft.OriginIataCode,
			&aircraft.OriginLat,
			&aircraft.OriginLon,
			&aircraft.DestinationIcaoCode,
			&aircraft.DestinationIataCode,
			&aircraft.DestinationLat,
			&aircraft.DestinationLon,
		)

		if err != nil {
			log.Error().Err(err).Msg("getAircraftsForDistanceStatistics() - Error scanning rows")
			continue
		}

		if aircraft.OriginIcaoCode.Valid || aircraft.DestinationIcaoCode.Valid {
			matched = append(matched, aircraft)
		} else {
			unmatched = append(unmatched, aircraft)
		}
	}

	log.Debug().Msgf("Distance stats: %d matched, %d unmatched", len(matched), len(unmatched))
	return matched, unmatched
}

func updateFurthestFlownAircraft(pg *postgres, aircrafts []Aircraft) {

	tableName := "furthest_flown_aircraft"
	metricName := "distance_flown"

	var toProcess []Aircraft
	for _, aircraft := range aircrafts {
		if !aircraft.FurthestFlownProcessed {
			toProcess = append(toProcess, aircraft)
		}
	}
	if len(toProcess) == 0 {
		return
	}

	batch := &pgx.Batch{}
	var queued []Aircraft

	for _, aircraft := range toProcess {
		if !aircraft.OriginLat.Valid || !aircraft.OriginLon.Valid ||
			!aircraft.LastSeenLat.Valid || !aircraft.LastSeenLon.Valid {
			continue
		}

		distanceFlown := haversineDistanceKm(
			aircraft.OriginLat.Float64, aircraft.OriginLon.Float64,
			aircraft.LastSeenLat.Float64, aircraft.LastSeenLon.Float64)

		insertStatement := `
			INSERT INTO furthest_flown_aircraft (
				hex, flight, registration, type, first_seen, last_seen,
				origin_icao_code, origin_iata_code,
				destination_icao_code, destination_iata_code, distance_flown)
			VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (hex, first_seen)
			DO UPDATE SET
				distance_flown = EXCLUDED.distance_flown,
				last_seen = EXCLUDED.last_seen`

		batch.Queue(insertStatement,
			aircraft.Hex, aircraft.Flight, aircraft.R, aircraft.T,
			aircraft.FirstSeen, aircraft.LastSeen,
			aircraft.OriginIcaoCode, aircraft.OriginIataCode,
			aircraft.DestinationIcaoCode, aircraft.DestinationIataCode,
			distanceFlown)

		queued = append(queued, aircraft)
	}

	br := pg.db.SendBatch(context.Background(), batch)
	defer br.Close()
	for i := 0; i < len(queued); i++ {
		if _, err := br.Exec(); err != nil {
			log.Error().Err(err).Msg("updateFurthestFlownAircraft() - Unable to insert data")
		}
	}

	// "ASC" here means: delete the LOWEST distance_flown rows first, keeping
	// the highest — because this leaderboard wants to keep the maximum.
	DeleteExcessRows(pg, tableName, metricName, "ASC", 50)
	MarkProcessed(pg, "furthest_flown_processed", toProcess)
}

func updateMostRemainingAircraft(pg *postgres, aircrafts []Aircraft) {

	tableName := "most_remaining_aircraft"
	metricName := "distance_remaining"

	var toProcess []Aircraft
	for _, aircraft := range aircrafts {
		if !aircraft.MostRemainingProcessed {
			toProcess = append(toProcess, aircraft)
		}
	}
	if len(toProcess) == 0 {
		return
	}

	batch := &pgx.Batch{}
	var queued []Aircraft

	for _, aircraft := range toProcess {
		// Only the destination is required for "remaining distance".
		if !aircraft.DestinationLat.Valid || !aircraft.DestinationLon.Valid ||
			!aircraft.LastSeenLat.Valid || !aircraft.LastSeenLon.Valid {
			continue
		}

		distanceRemaining := haversineDistanceKm(
			aircraft.LastSeenLat.Float64, aircraft.LastSeenLon.Float64,
			aircraft.DestinationLat.Float64, aircraft.DestinationLon.Float64)

		insertStatement := `
			INSERT INTO most_remaining_aircraft (
				hex, flight, registration, type, first_seen, last_seen,
				destination_icao_code, destination_iata_code, distance_remaining)
			VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (hex, first_seen)
			DO UPDATE SET
				distance_remaining = EXCLUDED.distance_remaining,
				last_seen = EXCLUDED.last_seen`

		batch.Queue(insertStatement,
			aircraft.Hex, aircraft.Flight, aircraft.R, aircraft.T,
			aircraft.FirstSeen, aircraft.LastSeen,
			aircraft.DestinationIcaoCode, aircraft.DestinationIataCode,
			distanceRemaining)

		queued = append(queued, aircraft)
	}

	br := pg.db.SendBatch(context.Background(), batch)
	defer br.Close()
	for i := 0; i < len(queued); i++ {
		if _, err := br.Exec(); err != nil {
			log.Error().Err(err).Msg("updateMostRemainingAircraft() - Unable to insert data")
		}
	}

	DeleteExcessRows(pg, tableName, metricName, "ASC", 50)
	MarkProcessed(pg, "most_remaining_processed", toProcess)
}

func updateLongestRouteAircraft(pg *postgres, aircrafts []Aircraft) {

	tableName := "longest_route_aircraft"
	metricName := "route_distance"

	var toProcess []Aircraft
	for _, aircraft := range aircrafts {
		if !aircraft.LongestRouteProcessed {
			toProcess = append(toProcess, aircraft)
		}
	}
	if len(toProcess) == 0 {
		return
	}

	batch := &pgx.Batch{}
	var queued []Aircraft

	for _, aircraft := range toProcess {
		if !aircraft.OriginLat.Valid || !aircraft.OriginLon.Valid ||
			!aircraft.DestinationLat.Valid || !aircraft.DestinationLon.Valid {
			continue
		}

		routeDistance := haversineDistanceKm(
			aircraft.OriginLat.Float64, aircraft.OriginLon.Float64,
			aircraft.DestinationLat.Float64, aircraft.DestinationLon.Float64)

		insertStatement := `
			INSERT INTO longest_route_aircraft (
				hex, flight, registration, type, first_seen, last_seen,
				origin_icao_code, origin_iata_code,
				destination_icao_code, destination_iata_code, route_distance)
			VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (hex, first_seen)
			DO UPDATE SET
				route_distance = EXCLUDED.route_distance,
				last_seen = EXCLUDED.last_seen`

		batch.Queue(insertStatement,
			aircraft.Hex, aircraft.Flight, aircraft.R, aircraft.T,
			aircraft.FirstSeen, aircraft.LastSeen,
			aircraft.OriginIcaoCode, aircraft.OriginIataCode,
			aircraft.DestinationIcaoCode, aircraft.DestinationIataCode,
			routeDistance)

		queued = append(queued, aircraft)
	}

	br := pg.db.SendBatch(context.Background(), batch)
	defer br.Close()
	for i := 0; i < len(queued); i++ {
		if _, err := br.Exec(); err != nil {
			log.Error().Err(err).Msg("updateLongestRouteAircraft() - Unable to insert data")
		}
	}

	DeleteExcessRows(pg, tableName, metricName, "ASC", 50)
	MarkProcessed(pg, "longest_route_processed", toProcess)
}
