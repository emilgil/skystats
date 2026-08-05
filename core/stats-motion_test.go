package main

import (
	"testing"
	"time"
)

func TestShouldRefreshRunningMax_UnprocessedFlightIsAlwaysWritten(t *testing.T) {
	now := time.Now()
	longGone := now.Add(-3 * time.Hour)

	if !shouldRefreshRunningMax(false, longGone, now) {
		t.Error("a flight that has never been written must be written once, however old")
	}
}

func TestShouldRefreshRunningMax_StillAirborneFlightKeepsBeingWritten(t *testing.T) {
	// The bug: fastest/highest wrote a flight on the first 120s tick after it
	// appeared and then marked it processed forever, freezing the record at
	// whatever the aircraft happened to be doing a couple of minutes in, while
	// aircraft_data kept tracking the real session maximum.
	now := time.Now()
	seenOneTickAgo := now.Add(-2 * time.Minute)

	if !shouldRefreshRunningMax(true, seenOneTickAgo, now) {
		t.Error("a flight still being received must keep being written as its session maximum climbs")
	}
}

func TestShouldRefreshRunningMax_FinishedFlightIsLeftAlone(t *testing.T) {
	now := time.Now()
	goneQuiet := now.Add(-30 * time.Minute)

	if shouldRefreshRunningMax(true, goneQuiet, now) {
		t.Error("a flight that is no longer being received has a settled maximum and must not be rewritten")
	}
}
