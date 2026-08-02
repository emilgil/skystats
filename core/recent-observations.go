package main

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// recentObservationsCutoff returns the earliest first_seen a "recent
// observations" query should include for the given period, relative to now.
// The zero time.Time is returned for "all_time" (and any unrecognized
// period), which is always before any real flight_history.first_seen and so
// imposes no lower bound.
func recentObservationsCutoff(period string, now time.Time) time.Time {
	window, ok := periodWindow(period)
	if !ok {
		return time.Time{}
	}
	return now.Add(-window)
}

// getRecentObservations returns the N most recently observed flights
// (flight_history rows), newest first, optionally windowed by period. Unlike
// getRecords, this is not ranked on a single metric — it is the raw
// chronological feed of every evaluated flight, records or not.
func (s *APIServer) getRecentObservations(c *gin.Context) {
	period := c.DefaultQuery("period", "all_time")
	if !isValidPeriodType(period) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid period"})
		return
	}

	limit := s.getLimit("record_holder_table_limit")
	if limit > 100 {
		limit = 100
	}

	cutoff := recentObservationsCutoff(period, time.Now())

	query := `
		SELECT fh.hex, fh.flight, fh.registration, fh.type, fh.first_seen, fh.last_seen,
		       fh.origin_iata_code, fh.destination_iata_code, rt.airline_name
		FROM flight_history fh
		LEFT JOIN route_data rt ON fh.flight = rt.route_callsign
		WHERE fh.first_seen >= $1
		ORDER BY fh.first_seen DESC
		LIMIT $2`

	rows, err := s.pg.db.Query(context.Background(), query, cutoff, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	out := []gin.H{}
	for rows.Next() {
		var hex string
		var flight, registration, aircraftType *string
		var firstSeen time.Time
		var lastSeen *time.Time
		var originIata, destinationIata, airlineName *string

		if err := rows.Scan(&hex, &flight, &registration, &aircraftType,
			&firstSeen, &lastSeen, &originIata, &destinationIata, &airlineName); err != nil {
			continue
		}

		out = append(out, gin.H{
			"hex":                   hex,
			"flight":                flight,
			"registration":          registration,
			"type":                  aircraftType,
			"first_seen":            firstSeen,
			"last_seen":             lastSeen,
			"origin_iata_code":      originIata,
			"destination_iata_code": destinationIata,
			"airline_name":          airlineName,
		})
	}

	c.JSON(http.StatusOK, out)
}
