package main

import (
	"testing"
	"time"
)

func TestBestFirstSQL(t *testing.T) {
	cases := map[string]string{
		"fastest": "DESC",
		"slowest": "ASC",
		"highest": "DESC",
		"lowest":  "ASC",
	}
	for cat, want := range cases {
		if got := recordCategories[cat].bestFirstSQL(); got != want {
			t.Errorf("%s: got %s want %s", cat, got, want)
		}
	}
}

func TestPeriodWindow(t *testing.T) {
	if _, ok := periodWindow("all_time"); ok {
		t.Error("all_time should have no window")
	}
	w, ok := periodWindow("7d")
	if !ok || w != 7*24*time.Hour {
		t.Errorf("7d window wrong: %v ok=%v", w, ok)
	}
}

func TestPeriodsForFirstSeen_Fresh(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	got := periodsForFirstSeen(now.Add(-time.Minute), now)
	want := []string{"24h", "7d", "30d", "90d", "365d", "all_time"}
	if !equalStrings(got, want) {
		t.Errorf("fresh flight: got %v want %v", got, want)
	}
}

func TestPeriodsForFirstSeen_TenDaysOld(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	got := periodsForFirstSeen(now.Add(-10*24*time.Hour), now)
	want := []string{"30d", "90d", "365d", "all_time"}
	if !equalStrings(got, want) {
		t.Errorf("10-day-old flight: got %v want %v", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
