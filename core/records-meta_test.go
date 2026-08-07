package main

import (
	"strings"
	"testing"
	"time"
)

func TestBestFirstSQL(t *testing.T) {
	cases := map[string]string{
		"fastest":        "DESC",
		"slowest":        "ASC",
		"highest":        "DESC",
		"lowest":         "ASC",
		"nearest":        "ASC",
		"furthest_range": "DESC",
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

func TestValidateRecordCategories_AllKnown(t *testing.T) {
	all := []string{"fastest", "slowest", "highest", "lowest",
		"furthest_flown", "longest_route", "most_remaining"}
	if err := validateRecordCategories(all); err != nil {
		t.Errorf("all seven categories should validate, got %v", err)
	}
}

func TestValidateRecordCategories_Empty(t *testing.T) {
	if err := validateRecordCategories(nil); err == nil {
		t.Error("empty category list should be rejected")
	}
}

func TestValidateRecordCategories_Unknown(t *testing.T) {
	err := validateRecordCategories([]string{"fastest", "not_a_category"})
	if err == nil {
		t.Fatal("unknown category should be rejected")
	}
	if !strings.Contains(err.Error(), "not_a_category") {
		t.Errorf("error should name the offending category, got %q", err)
	}
}

func TestValidateRecordCategories_RejectsBeforeAnyValidOnes(t *testing.T) {
	// The unknown key is last, so a caller that validates the whole list up
	// front cannot have deleted "fastest" by the time it finds out.
	if err := validateRecordCategories([]string{"fastest", "highest", "bogus"}); err == nil {
		t.Error("a trailing unknown category should still fail the whole list")
	}
}

func TestValidateRecordCategories_Duplicates(t *testing.T) {
	if err := validateRecordCategories([]string{"fastest", "fastest"}); err != nil {
		t.Errorf("duplicates are harmless for a delete, got %v", err)
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
