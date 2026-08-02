package main

import (
	"time"
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
