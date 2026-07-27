package main

import (
	"context"
	"database/sql"

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
	var toProcess []Aircraft
	for _, a := range aircrafts {
		if !a.FurthestFlownProcessed {
			toProcess = append(toProcess, a)
		}
	}
	if len(toProcess) == 0 {
		return
	}

	var candidates []recordCandidate
	for _, a := range toProcess {
		if !a.OriginLat.Valid || !a.OriginLon.Valid || !a.LastSeenLat.Valid || !a.LastSeenLon.Valid {
			continue
		}
		distanceFlown := haversineDistanceKm(
			a.OriginLat.Float64, a.OriginLon.Float64, a.LastSeenLat.Float64, a.LastSeenLon.Float64)

		upsertFlightHistory(pg, a.Hex, a.Flight, a.R, a.T, a.FirstSeen, a.LastSeen, map[string]any{
			"distance_flown":        distanceFlown,
			"origin_icao_code":      nullStr(a.OriginIcaoCode),
			"origin_iata_code":      nullStr(a.OriginIataCode),
			"destination_icao_code": nullStr(a.DestinationIcaoCode),
			"destination_iata_code": nullStr(a.DestinationIataCode),
		})
		candidates = append(candidates, recordCandidate{
			Hex: a.Hex, Flight: a.Flight, Registration: a.R, Type: a.T,
			FirstSeen: a.FirstSeen, LastSeen: a.LastSeen,
			MetricValue: distanceFlown,
			Details: map[string]any{
				"origin_icao_code":      nullStr(a.OriginIcaoCode),
				"origin_iata_code":      nullStr(a.OriginIataCode),
				"destination_icao_code": nullStr(a.DestinationIcaoCode),
				"destination_iata_code": nullStr(a.DestinationIataCode),
			},
		})
	}
	writeRecords(pg, "furthest_flown", candidates)
	MarkProcessed(pg, "furthest_flown_processed", toProcess)
}

func updateMostRemainingAircraft(pg *postgres, aircrafts []Aircraft) {
	var toProcess []Aircraft
	for _, a := range aircrafts {
		if !a.MostRemainingProcessed {
			toProcess = append(toProcess, a)
		}
	}
	if len(toProcess) == 0 {
		return
	}

	var candidates []recordCandidate
	for _, a := range toProcess {
		if !a.DestinationLat.Valid || !a.DestinationLon.Valid || !a.LastSeenLat.Valid || !a.LastSeenLon.Valid {
			continue
		}
		distanceRemaining := haversineDistanceKm(
			a.LastSeenLat.Float64, a.LastSeenLon.Float64, a.DestinationLat.Float64, a.DestinationLon.Float64)

		upsertFlightHistory(pg, a.Hex, a.Flight, a.R, a.T, a.FirstSeen, a.LastSeen, map[string]any{
			"distance_remaining":    distanceRemaining,
			"destination_icao_code": nullStr(a.DestinationIcaoCode),
			"destination_iata_code": nullStr(a.DestinationIataCode),
		})
		candidates = append(candidates, recordCandidate{
			Hex: a.Hex, Flight: a.Flight, Registration: a.R, Type: a.T,
			FirstSeen: a.FirstSeen, LastSeen: a.LastSeen,
			MetricValue: distanceRemaining,
			Details: map[string]any{
				"destination_icao_code": nullStr(a.DestinationIcaoCode),
				"destination_iata_code": nullStr(a.DestinationIataCode),
			},
		})
	}
	writeRecords(pg, "most_remaining", candidates)
	MarkProcessed(pg, "most_remaining_processed", toProcess)
}

func updateLongestRouteAircraft(pg *postgres, aircrafts []Aircraft) {
	var toProcess []Aircraft
	for _, a := range aircrafts {
		if !a.LongestRouteProcessed {
			toProcess = append(toProcess, a)
		}
	}
	if len(toProcess) == 0 {
		return
	}

	var candidates []recordCandidate
	for _, a := range toProcess {
		if !a.OriginLat.Valid || !a.OriginLon.Valid || !a.DestinationLat.Valid || !a.DestinationLon.Valid {
			continue
		}
		routeDistance := haversineDistanceKm(
			a.OriginLat.Float64, a.OriginLon.Float64, a.DestinationLat.Float64, a.DestinationLon.Float64)

		upsertFlightHistory(pg, a.Hex, a.Flight, a.R, a.T, a.FirstSeen, a.LastSeen, map[string]any{
			"route_distance":        routeDistance,
			"origin_icao_code":      nullStr(a.OriginIcaoCode),
			"origin_iata_code":      nullStr(a.OriginIataCode),
			"destination_icao_code": nullStr(a.DestinationIcaoCode),
			"destination_iata_code": nullStr(a.DestinationIataCode),
		})
		candidates = append(candidates, recordCandidate{
			Hex: a.Hex, Flight: a.Flight, Registration: a.R, Type: a.T,
			FirstSeen: a.FirstSeen, LastSeen: a.LastSeen,
			MetricValue: routeDistance,
			Details: map[string]any{
				"origin_icao_code":      nullStr(a.OriginIcaoCode),
				"origin_iata_code":      nullStr(a.OriginIataCode),
				"destination_icao_code": nullStr(a.DestinationIcaoCode),
				"destination_iata_code": nullStr(a.DestinationIataCode),
			},
		})
	}
	writeRecords(pg, "longest_route", candidates)
	MarkProcessed(pg, "longest_route_processed", toProcess)
}

// nullStr unwraps a sql.NullString to a *string so it JSON-marshals as null
// (not "") when absent, matching migration 000012's bootstrap details.
func nullStr(ns sql.NullString) any {
	if ns.Valid {
		return ns.String
	}
	return nil
}
