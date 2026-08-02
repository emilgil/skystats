package main

import (
	"testing"
	"time"
)

func TestRecentObservationsCutoffAllTime(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	got := recentObservationsCutoff("all_time", now)
	if !got.IsZero() {
		t.Errorf("all_time: got %v want zero time", got)
	}
}

func TestRecentObservationsCutoff7d(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	got := recentObservationsCutoff("7d", now)
	want := now.Add(-7 * 24 * time.Hour)
	if !got.Equal(want) {
		t.Errorf("7d: got %v want %v", got, want)
	}
}

func TestRecentObservationsCutoffInvalidPeriod(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	got := recentObservationsCutoff("not-a-period", now)
	if !got.IsZero() {
		t.Errorf("invalid period: got %v want zero time", got)
	}
}
