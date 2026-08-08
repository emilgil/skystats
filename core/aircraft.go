package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	cheapruler "github.com/JamesLMilner/cheap-ruler-go"
	"github.com/jackc/pgx/v5"
)

func updateAircraftDatabase(pg *postgres) {

	responseData, err := Fetch()

	if err != nil {
		log.Error().Err(err).Msg("Unable to fetch data from feeder aircraft.json")
		return
	}

	var response Response
	json.Unmarshal(responseData, &response)

	response.TrimFlightStrings()

	loc := []float64{getLon(), getLat()}

	var aircraftsInRange []Aircraft

	for _, aircraft := range response.Aircraft {

		// Filter out non-aircraft
		if isNonAircraft(aircraft) {
			continue
		}

		// Filter out aircraft not within radius
		planeLoc := []float64{aircraft.Lon, aircraft.Lat}
		distance := getRuler().Distance(loc, planeLoc)

		if distance < getRadius() {
			aircraftsInRange = append(aircraftsInRange, aircraft)
		}

	}
	pg.updateDatabase(response.Now, aircraftsInRange)

	// One enrichment round trip per tick, shared by every consumer of the
	// snapshot.
	enrichment := enrichAircraftSnapshot(pg, aircraftsInRange)
	requestMissingRoutes(pg, aircraftsInRange, enrichment)
	refreshCurrentSightings(response.Now, aircraftsInRange, enrichment)
	evaluateWatches(pg, aircraftsInRange, enrichment)
}

func isNonAircraft(aircraft Aircraft) bool {
	return aircraft.T == "TWR" ||
		aircraft.R == "TWR" ||
		strings.HasPrefix(aircraft.Category, "C") ||
		aircraft.Squawk == "7777"
}

func getRuler() *cheapruler.CheapRuler {
	ruler, err := cheapruler.NewCheapruler(getLat(), "kilometers")
	if err != nil {
		fmt.Println("Error creating ruler: ", err)
		return nil
	}

	return &ruler
}

func getDistance(aircraft []float64) *float64 {
	loc := []float64{getLon(), getLat()}
	distance := getRuler().Distance(loc, aircraft)
	return &distance
}

func getBearing(aircraft []float64) float64 {
	loc := []float64{getLon(), getLat()}
	return normalizeBearing(getRuler().Bearing(loc, aircraft))
}

// normalizeBearing converts cheap-ruler's Bearing() output, which is in
// (-180, 180], to a compass bearing in [0, 360).
func normalizeBearing(bearing float64) float64 {
	if bearing < 0 {
		return bearing + 360
	}
	return bearing
}

func (pg *postgres) updateDatabase(nowEpoch float64, aircrafts []Aircraft) {

	existingAircrafts := getAircraftsRecentlySeen(pg, nowEpoch, aircrafts)

	if len(existingAircrafts) > 0 {
		updateExistingAircrafts(pg, nowEpoch, aircrafts, existingAircrafts)
	}

	insertNewAircrafts(pg, nowEpoch, existingAircrafts, aircrafts)

}

func getAircraftsRecentlySeen(pg *postgres, nowEpoch float64, aircrafts []Aircraft) map[string]*Aircraft {

	existingAircrafts := make(map[string]*Aircraft)
	now := time.Now()
	newExpiry := now.Add(10 * time.Minute)

	var uncachedHexes []string

	for _, a := range aircrafts {
		entry, ok := pg.recentAircraftCache[a.Hex]
		if ok && now.Before(entry.expiresAt) {
			entry.expiresAt = newExpiry
			existingAircrafts[a.Hex] = &entry.aircraft
		} else {
			uncachedHexes = append(uncachedHexes, a.Hex)
		}
	}

	if len(uncachedHexes) == 0 {
		return existingAircrafts
	}

	recentThreshold := int(nowEpoch) - 600

	query := `
		SELECT DISTINCT ON (hex)
			id,
			hex,
			last_seen_epoch,
			last_seen_lat,
			last_seen_lon,
			last_seen_distance,
			alt_baro,
			alt_geom,
			gs,
			ias,
			tas,
			min_distance_receiver,
			min_distance_receiver_altitude,
			min_distance_receiver_bearing,
			max_distance_receiver,
			max_distance_receiver_altitude,
			max_distance_receiver_bearing
		FROM aircraft_data
		WHERE hex = ANY($1::text[])
			AND last_seen_epoch > $2
		ORDER BY hex, last_seen DESC;
	`

	rows, err := pg.db.Query(context.Background(), query, uncachedHexes, recentThreshold)
	if err != nil {
		fmt.Println("getAircraftsRecentlySeen() - Error querying db: ", err)
		return nil
	}
	defer rows.Close()

	for rows.Next() {
		var a Aircraft
		err := rows.Scan(
			&a.Id,
			&a.Hex,
			&a.LastSeenEpoch,
			&a.LastSeenLat,
			&a.LastSeenLon,
			&a.LastSeenDistance,
			&a.AltBaro,
			&a.AltGeom,
			&a.Gs,
			&a.Ias,
			&a.Tas,
			&a.MinDistanceReceiver,
			&a.MinDistanceReceiverAltitude,
			&a.MinDistanceReceiverBearing,
			&a.MaxDistanceReceiver,
			&a.MaxDistanceReceiverAltitude,
			&a.MaxDistanceReceiverBearing)

		if err != nil {
			log.Error().Err(err).Msg("getAircraftsRecentlySeen() - error scanning rows")
			continue
		}

		pg.recentAircraftCache[a.Hex] = &aircraftCacheEntry{
			aircraft:  a,
			expiresAt: newExpiry,
		}
		existingAircrafts[a.Hex] = &pg.recentAircraftCache[a.Hex].aircraft
	}

	return existingAircrafts

}

func insertNewAircrafts(pg *postgres, nowEpoch float64, existingAircrafts map[string]*Aircraft, aircrafts []Aircraft) {

	batch := &pgx.Batch{}

	nowAsTime := time.Unix(int64(nowEpoch), 0)
	nowAsEpoch := int64(nowEpoch)

	var aircraftsToInsert []Aircraft

	for _, aircraft := range aircrafts {
		_, exists := existingAircrafts[aircraft.Hex]
		if !exists {
			lastSeenDistance := getDistance([]float64{aircraft.Lon, aircraft.Lat})
			bearing := getBearing([]float64{aircraft.Lon, aircraft.Lat})
			aircraftsToInsert = append(aircraftsToInsert, aircraft)
			insertStatement := `
				INSERT INTO aircraft_data (
					hex,
					flight,
					first_seen,
					first_seen_epoch,
					last_seen,
					last_seen_epoch,
					last_seen_lat,
					last_seen_lon,
					last_seen_distance,
					type,
					r,
					t,
					alt_baro,
					alt_geom,
					gs,
					ias,
					tas,
					track,
					baro_rate,
					nav_qnh,
					nav_altitude_mcp,
					nav_heading,
					lat,
					lon,
					nic,
					rc,
					seen_pos,
					r_dst,
					r_dir,
					version,
					nic_baro,
					nac_p,
					nac_v,
					sil,
					sil_type,
					alert,
					spi,
					mlat,
					tisb,
					messages,
					seen,
					rssi,
					db_flags,
					min_distance_receiver,
					min_distance_receiver_altitude,
					min_distance_receiver_bearing,
					max_distance_receiver,
					max_distance_receiver_altitude,
					max_distance_receiver_bearing
				) VALUES (
					$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
					$16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28,
					$29, $30, $31, $32, $33, $34, $35, $36, $37, $38, $39, $40, $41, $42, $43,
					$44, $45, $46, $47, $48, $49
				)`

			batch.Queue(insertStatement,
				aircraft.Hex,
				aircraft.Flight,
				nowAsTime,
				nowAsEpoch,
				nowAsTime,
				nowAsEpoch,
				aircraft.Lat,
				aircraft.Lon,
				lastSeenDistance,
				aircraft.Type,
				aircraft.R,
				aircraft.T,
				aircraft.AltBaro,
				aircraft.AltGeom,
				aircraft.Gs,
				aircraft.Ias,
				aircraft.Tas,
				aircraft.Track,
				aircraft.BaroRate,
				aircraft.NavQnh,
				aircraft.NavAltitudeMcp,
				aircraft.NavHeading,
				aircraft.Lat,
				aircraft.Lon,
				aircraft.Nic,
				aircraft.Rc,
				aircraft.SeenPos,
				aircraft.RDst,
				aircraft.RDir,
				aircraft.Version,
				aircraft.NicBaro,
				aircraft.NacP,
				aircraft.NacV,
				aircraft.Sil,
				aircraft.SilType,
				aircraft.Alert,
				aircraft.Spi,
				aircraft.Mlat,
				aircraft.Tisb,
				aircraft.Messages,
				aircraft.Seen,
				aircraft.Rssi,
				aircraft.DbFlags,
				*lastSeenDistance,
				aircraft.AltBaro,
				bearing,
				*lastSeenDistance,
				aircraft.AltBaro,
				bearing)
		}
	}

	br := pg.db.SendBatch(context.Background(), batch)
	defer br.Close()

	for i := 0; i < len(aircraftsToInsert); i++ {
		_, err := br.Exec()
		if err != nil {
			log.Error().Err(err).Msg("insertNewAircrafts() - unable to insert data")
		}
	}
}

func updateExistingAircrafts(pg *postgres, nowEpoch float64, aircrafts []Aircraft, existingAircrafts map[string]*Aircraft) {

	batch := &pgx.Batch{}

	for _, aircraft := range aircrafts {
		existingAircraft, exists := existingAircrafts[aircraft.Hex]
		if !exists {
			continue
		}

		// Update flight if null and found in subsequent messages
		if existingAircraft.Flight == "" {
			if aircraft.Flight != "" {
				existingAircraft.Flight = aircraft.Flight
			}
		}

		// Update last seen time
		existingAircraft.LastSeenEpoch = nowEpoch
		existingAircraft.LastSeen = time.Unix(int64(nowEpoch), 0)

		// Update last_seen_lat, last_seen_lon, last_seen_distance with the latest lat/lon
		existingAircraft.LastSeenLat = sql.NullFloat64{Float64: aircraft.Lat, Valid: true}
		existingAircraft.LastSeenLon = sql.NullFloat64{Float64: aircraft.Lon, Valid: true}
		lastSeenDistance := getDistance([]float64{aircraft.Lon, aircraft.Lat})
		existingAircraft.LastSeenDistance = sql.NullFloat64{Float64: *lastSeenDistance, Valid: true}

		// Update running min/max distance-to-receiver for Nearest/Furthest —
		// tracked across the whole flight, not snapshotted once. See
		// updateReceiverDistanceStatistics (stats-receiver-distance.go) for
		// where the final value gets written out.
		bearing := getBearing([]float64{aircraft.Lon, aircraft.Lat})
		if !existingAircraft.MinDistanceReceiver.Valid || *lastSeenDistance < existingAircraft.MinDistanceReceiver.Float64 {
			existingAircraft.MinDistanceReceiver = sql.NullFloat64{Float64: *lastSeenDistance, Valid: true}
			existingAircraft.MinDistanceReceiverAltitude = sql.NullInt64{Int64: int64(aircraft.AltBaro), Valid: true}
			existingAircraft.MinDistanceReceiverBearing = sql.NullFloat64{Float64: bearing, Valid: true}
		}
		if !existingAircraft.MaxDistanceReceiver.Valid || *lastSeenDistance > existingAircraft.MaxDistanceReceiver.Float64 {
			existingAircraft.MaxDistanceReceiver = sql.NullFloat64{Float64: *lastSeenDistance, Valid: true}
			existingAircraft.MaxDistanceReceiverAltitude = sql.NullInt64{Int64: int64(aircraft.AltBaro), Valid: true}
			existingAircraft.MaxDistanceReceiverBearing = sql.NullFloat64{Float64: bearing, Valid: true}
		}

		// Update destination distance
		if aircraft.Flight != "" {
			routeData, err := getRouteData(pg, aircraft.Flight)
			if err != nil {
			} else if routeData != nil && routeData.DestinationLatitude.Valid && routeData.DestinationLongitude.Valid {
				destinationDistance := getDestinationDistance(
					aircraft.Lat,
					aircraft.Lon,
					routeData.DestinationLatitude.Float64,
					routeData.DestinationLongitude.Float64)
				existingAircraft.DestinationDistance = sql.NullFloat64{Float64: destinationDistance, Valid: true}
			}
		}

		// Update track
		existingAircraft.Track = aircraft.Track

		// Update barometric altitude & geometric altitudes if higher than already stored
		if existingAircraft.AltBaro < aircraft.AltBaro {
			existingAircraft.AltBaro = aircraft.AltBaro
		}
		if existingAircraft.AltGeom < aircraft.AltGeom {
			existingAircraft.AltGeom = aircraft.AltGeom
		}

		// Update ground speed, indicated air speed, and true air speed if higher than already stored
		if existingAircraft.Gs < aircraft.Gs {
			existingAircraft.Gs = aircraft.Gs
		}
		if existingAircraft.Ias < aircraft.Ias {
			existingAircraft.Ias = aircraft.Ias
		}
		if existingAircraft.Tas < aircraft.Tas {
			existingAircraft.Tas = aircraft.Tas
		}

		updateStatement := `UPDATE aircraft_data
							SET last_seen = $1,
								last_seen_epoch = $2,
								last_seen_lat = $3,
								last_seen_lon = $4,
								last_seen_distance = $5,
								destination_distance = $6,
								track = $7,
								alt_baro = $8,
								alt_geom = $9,
								gs = $10,
								ias = $11,
								tas = $12,
								flight = $13,
								min_distance_receiver = $14,
								min_distance_receiver_altitude = $15,
								min_distance_receiver_bearing = $16,
								max_distance_receiver = $17,
								max_distance_receiver_altitude = $18,
								max_distance_receiver_bearing = $19
							WHERE id = $20`

		batch.Queue(
			updateStatement,
			existingAircraft.LastSeen,
			existingAircraft.LastSeenEpoch,
			existingAircraft.LastSeenLat,
			existingAircraft.LastSeenLon,
			existingAircraft.LastSeenDistance,
			existingAircraft.DestinationDistance,
			existingAircraft.Track,
			existingAircraft.AltBaro,
			existingAircraft.AltGeom,
			existingAircraft.Gs,
			existingAircraft.Ias,
			existingAircraft.Tas,
			existingAircraft.Flight,
			existingAircraft.MinDistanceReceiver,
			existingAircraft.MinDistanceReceiverAltitude,
			existingAircraft.MinDistanceReceiverBearing,
			existingAircraft.MaxDistanceReceiver,
			existingAircraft.MaxDistanceReceiverAltitude,
			existingAircraft.MaxDistanceReceiverBearing,
			existingAircraft.Id,
		)
	}

	br := pg.db.SendBatch(context.Background(), batch)
	defer br.Close()

	for i := 0; i < len(existingAircrafts); i++ {
		_, err := br.Exec()
		if err != nil {
			log.Error().Err(err).Msg("updateExistingAircrafts() - unable to update data")
		}
	}
}

func getLat() float64 {
	lat, _ := strconv.ParseFloat(os.Getenv("LAT"), 64)
	return lat
}

func getLon() float64 {
	lon, _ := strconv.ParseFloat(os.Getenv("LON"), 64)
	return lon
}

func getRadius() float64 {
	radius, _ := strconv.ParseFloat(os.Getenv("RADIUS"), 64)
	return radius
}

type RouteData struct {
	DestinationLatitude  sql.NullFloat64
	DestinationLongitude sql.NullFloat64
}

func getRouteData(pg *postgres, flight string) (*RouteData, error) {
	if flight == "" {
		return nil, nil
	}

	var route RouteData
	query := `
		SELECT destination_latitude, destination_longitude
		FROM route_data
		WHERE route_callsign = $1
		AND destination_latitude IS NOT NULL
		AND destination_longitude IS NOT NULL
		LIMIT 1
	`

	err := pg.db.QueryRow(context.Background(), query, flight).Scan(
		&route.DestinationLatitude,
		&route.DestinationLongitude,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil // No route data found
		}
		return nil, err
	}

	return &route, nil
}

// getDestinationDistance returns the great-circle distance in kilometers from
// an aircraft's current position to its destination airport.
//
// This shares haversine with getDistanceBetweenAirports on purpose: the Above
// Me progress bar computes distance travelled as route_distance minus this
// value, so the two have to come from the same formula or the bar reports a
// position the aircraft was never at.
func getDestinationDistance(currentLat, currentLon, destLat, destLon float64) float64 {
	distance := haversineDistanceKm(currentLat, currentLon, destLat, destLon)
	return math.Round(distance*100) / 100
}
