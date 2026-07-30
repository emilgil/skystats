package main

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

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
		"operator":     nil,
		"live":         nil,
		"history":      gin.H{"times_seen": 0, "last_seen": nil},
		"photo":        nil,
		"interesting":  nil,
	}

	// 1) Identity + photo from registration_data (adsbdb-enriched).
	var regType, registration, registeredOwner, urlPhoto, urlPhotoThumb *string
	err := s.pg.db.QueryRow(context.Background(), `
		SELECT type, registration, registered_owner, url_photo, url_photo_thumbnail
		FROM registration_data
		WHERE mode_s = $1`, hex).
		Scan(&regType, &registration, &registeredOwner, &urlPhoto, &urlPhotoThumb)
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
	if registeredOwner != nil {
		resp["operator"] = registeredOwner
	}
	if urlPhoto != nil || urlPhotoThumb != nil {
		resp["photo"] = gin.H{"url": urlPhoto, "thumbnail": urlPhotoThumb, "source": "adsbdb"}
	}

	// 2) Live status: newest aircraft_data row for this hex, airline via route_data.
	var flight, airlineName *string
	var altBaro *int
	var gs, track, distance, bearing, lat, lon *float64
	var lastSeen *time.Time
	err = s.pg.db.QueryRow(context.Background(), `
		SELECT ad.flight, ad.alt_baro,
		       ad.gs::float8, ad.track::float8,
		       ad.last_seen_distance::float8, ad.last_seen_bearing::float8,
		       ad.last_seen_lat::float8, ad.last_seen_lon::float8,
		       ad.last_seen, rt.airline_name
		FROM aircraft_data ad
		LEFT JOIN route_data rt ON ad.flight = rt.route_callsign
		WHERE ad.hex = $1
		ORDER BY ad.last_seen DESC
		LIMIT 1`, hex).
		Scan(&flight, &altBaro, &gs, &track, &distance, &bearing, &lat, &lon, &lastSeen, &airlineName)
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

	c.JSON(http.StatusOK, resp)
}
