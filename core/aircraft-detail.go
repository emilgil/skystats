package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// personalBestRecord is one badge on the aircraft detail card: this
// aircraft's own best/worst reading for one metric, and whether that same
// category also currently places in the fleet-wide top-100 records table.
type personalBestRecord struct {
	Category       string
	MetricName     string
	Value          float64
	IsGlobalRecord bool
}

// buildPersonalBestRecords assembles the badge list for one aircraft from its
// freshly-aggregated personal-best values (nil = no qualifying observation
// for that metric, so the category is omitted entirely) and the set of
// categories where this hex currently holds a fleet-wide record. Order is
// fixed (fastest, slowest, highest, lowest, longest_route, furthest_flown,
// most_remaining) to match the Speed/Altitude/Distance grouping the
// frontend renders.
func buildPersonalBestRecords(maxGs, minGs, maxAlt, minAlt, maxRouteDist, maxDistFlown, maxDistRemaining *float64, globalRecordCategories map[string]bool) []personalBestRecord {
	candidates := []struct {
		category string
		value    *float64
	}{
		{"fastest", maxGs},
		{"slowest", minGs},
		{"highest", maxAlt},
		{"lowest", minAlt},
		{"longest_route", maxRouteDist},
		{"furthest_flown", maxDistFlown},
		{"most_remaining", maxDistRemaining},
	}
	out := []personalBestRecord{}
	for _, cand := range candidates {
		if cand.value == nil {
			continue
		}
		out = append(out, personalBestRecord{
			Category:       cand.category,
			MetricName:     recordCategories[cand.category].MetricName,
			Value:          *cand.value,
			IsGlobalRecord: globalRecordCategories[cand.category],
		})
	}
	return out
}

// getAircraftDetail assembles a single aircraft's detail (identity, live status,
// history, photo, interesting metadata) for the info modal. Read-only, on-demand.
func (s *APIServer) getAircraftDetail(c *gin.Context) {
	hex := c.Param("hex")
	if hex == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "hex is required"})
		return
	}

	resp := gin.H{
		"hex":          hex,
		"registration": nil,
		"type":         nil,
		"manufacturer": nil,
		"icao_type":    nil,
		"operator":     nil,
		"live":         nil,
		"history":      gin.H{"times_seen": 0, "last_seen": nil},
		"photo":        nil,
		"records":      []gin.H{},
		"observations": []gin.H{},
		"interesting":  nil,
	}

	// 1) Identity + photo from registration_data (adsbdb-enriched).
	var regType, registration, registeredOwner, manufacturer, icaoType, urlPhoto, urlPhotoThumb *string
	err := s.pg.db.QueryRow(context.Background(), `
		SELECT type, registration, registered_owner, manufacturer, icao_type, url_photo, url_photo_thumbnail
		FROM registration_data
		WHERE mode_s = $1`, hex).
		Scan(&regType, &registration, &registeredOwner, &manufacturer, &icaoType, &urlPhoto, &urlPhotoThumb)
	if err != nil && err != pgx.ErrNoRows {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if registration != nil {
		resp["registration"] = registration
	}
	if regType != nil {
		resp["type"] = regType
	}
	if manufacturer != nil {
		resp["manufacturer"] = manufacturer
	}
	if icaoType != nil {
		resp["icao_type"] = icaoType
	}
	if registeredOwner != nil {
		resp["operator"] = registeredOwner
	}
	if urlPhoto != nil || urlPhotoThumb != nil {
		resp["photo"] = gin.H{"url": urlPhoto, "thumbnail": urlPhotoThumb, "source": "adsbdb"}
	}

	// 2) Live status: newest aircraft_data row for this hex, airline via route_data.
	var flight, airlineName, readsbReg *string
	var altBaro *int
	var gs, track, distance, bearing, lat, lon *float64
	var lastSeen *time.Time
	err = s.pg.db.QueryRow(context.Background(), `
		SELECT ad.flight, ad.alt_baro,
		       ad.gs::float8, ad.track::float8,
		       ad.last_seen_distance::float8, ad.last_seen_bearing::float8,
		       ad.last_seen_lat::float8, ad.last_seen_lon::float8,
		       ad.last_seen, rt.airline_name, ad.r
		FROM aircraft_data ad
		LEFT JOIN route_data rt ON ad.flight = rt.route_callsign
		WHERE ad.hex = $1
		ORDER BY ad.last_seen DESC
		LIMIT 1`, hex).
		Scan(&flight, &altBaro, &gs, &track, &distance, &bearing, &lat, &lon, &lastSeen, &airlineName, &readsbReg)
	if err != nil && err != pgx.ErrNoRows {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err == nil {
		// Airline name (from the route) is a better "operator" than registered owner.
		if airlineName != nil && *airlineName != "" {
			resp["operator"] = airlineName
		}
		if lastSeen != nil && time.Since(*lastSeen) <= 60*time.Second {
			resp["live"] = gin.H{
				"altitude":     altBaro,
				"ground_speed": gs,
				"track":        track,
				"distance_km":  distance,
				"bearing":      bearing,
				"lat":          lat,
				"lon":          lon,
			}
		}
	}

	// Fallback: adsbdb had no registration — use the readsb `r` from the feed
	// (aircraft_data.r), which covers far more aircraft. adsbdb keeps priority.
	if readsbReg != nil {
		if trimmed := strings.TrimSpace(*readsbReg); trimmed != "" {
			if cur, ok := resp["registration"].(*string); !ok || cur == nil || *cur == "" {
				resp["registration"] = trimmed
			}
		}
	}

	// 3) History: count of visits + most recent visit from flight_history.
	var timesSeen int
	var histLastSeen *time.Time
	err = s.pg.db.QueryRow(context.Background(), `
		SELECT COUNT(*), MAX(last_seen)
		FROM flight_history
		WHERE hex = $1`, hex).
		Scan(&timesSeen, &histLastSeen)
	if err != nil && err != pgx.ErrNoRows {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if histLastSeen == nil {
		// Not yet swept into flight_history — fall back to the live-query timestamp.
		histLastSeen = lastSeen
	}
	resp["history"] = gin.H{"times_seen": timesSeen, "last_seen": histLastSeen}

	// 4) Interesting metadata: newest sighting for this hex.
	var iGroup, iOperator, tag1, tag2, tag3, img1, img2, img3 *string
	err = s.pg.db.QueryRow(context.Background(), `
		SELECT "group", operator, tag1, tag2, tag3,
		       image_link_1, image_link_2, image_link_3
		FROM interesting_aircraft_seen
		WHERE hex = $1
		ORDER BY seen DESC
		LIMIT 1`, hex).
		Scan(&iGroup, &iOperator, &tag1, &tag2, &tag3, &img1, &img2, &img3)
	if err != nil && err != pgx.ErrNoRows {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err == nil {
		tags := []string{}
		for _, t := range []*string{tag1, tag2, tag3} {
			if t != nil && *t != "" {
				tags = append(tags, *t)
			}
		}
		images := []string{}
		for _, im := range []*string{img1, img2, img3} {
			if im != nil && *im != "" {
				images = append(images, *im)
			}
		}
		resp["interesting"] = gin.H{
			"group":    iGroup,
			"operator": iOperator,
			"tags":     tags,
			"images":   images,
		}
	}

	// 5) Personal-best per category, computed fresh from this hex's full
	// flight_history rather than the trimmed/windowed records leaderboard
	// snapshot (which freezes at whatever value existed the one time a
	// session row was processed — see
	// docs/superpowers/specs/2026-08-03-unified-record-badges-spec.md).
	var maxGs, minGs, maxAlt, minAlt, maxRouteDist, maxDistFlown, maxDistRemaining *float64
	err = s.pg.db.QueryRow(context.Background(), `
		SELECT MAX(ground_speed)::float8, MIN(ground_speed)::float8,
		       MAX(barometric_altitude)::float8, MIN(barometric_altitude)::float8,
		       MAX(route_distance)::float8, MAX(distance_flown)::float8, MAX(distance_remaining)::float8
		FROM flight_history
		WHERE hex = $1`, hex).
		Scan(&maxGs, &minGs, &maxAlt, &minAlt, &maxRouteDist, &maxDistFlown, &maxDistRemaining)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	globalRecordCategories := map[string]bool{}
	catRows, err := s.pg.db.Query(context.Background(), `SELECT DISTINCT category FROM records WHERE hex = $1`, hex)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for catRows.Next() {
		var category string
		if err := catRows.Scan(&category); err != nil {
			continue
		}
		globalRecordCategories[category] = true
	}
	catRows.Close()

	records := []gin.H{}
	for _, r := range buildPersonalBestRecords(maxGs, minGs, maxAlt, minAlt, maxRouteDist, maxDistFlown, maxDistRemaining, globalRecordCategories) {
		records = append(records, gin.H{
			"category":         r.Category,
			"metric_name":      r.MetricName,
			"value":            r.Value,
			"is_global_record": r.IsGlobalRecord,
		})
	}
	resp["records"] = records

	// 6) Recent observations (visits) from flight_history, newest first, max 10.
	obsRows, err := s.pg.db.Query(context.Background(), `
		SELECT first_seen, last_seen, origin_iata_code, destination_iata_code,
		       ground_speed::float8, barometric_altitude
		FROM flight_history
		WHERE hex = $1
		ORDER BY first_seen DESC
		LIMIT 10`, hex)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	observations := []gin.H{}
	for obsRows.Next() {
		var firstSeen, lastSeen *time.Time
		var origin, destination *string
		var groundSpeed *float64
		var altitude *int
		if err := obsRows.Scan(&firstSeen, &lastSeen, &origin, &destination, &groundSpeed, &altitude); err != nil {
			continue
		}
		observations = append(observations, gin.H{
			"first_seen":   firstSeen,
			"last_seen":    lastSeen,
			"origin":       origin,
			"destination":  destination,
			"ground_speed": groundSpeed,
			"altitude":     altitude,
		})
	}
	obsRows.Close()
	resp["observations"] = observations

	c.JSON(http.StatusOK, resp)
}
