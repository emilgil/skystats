package main

import "time"

// recordCategory is the single source of truth for how a leaderboard category
// is named and sorted. Shared by the ingest write path, the read/API path, and
// (later) the custom-range read path.
type recordCategory struct {
	Name       string // canonical category, matches records.category CHECK
	MetricName string // records.metric_name; also the flat JSON field the frontend expects
	KeepMax    bool   // true: larger metric_value is better (ORDER BY DESC); false: smaller (ASC)
}

var recordCategories = map[string]recordCategory{
	"fastest":        {Name: "fastest", MetricName: "ground_speed", KeepMax: true},
	"slowest":        {Name: "slowest", MetricName: "ground_speed", KeepMax: false},
	"highest":        {Name: "highest", MetricName: "barometric_altitude", KeepMax: true},
	"lowest":         {Name: "lowest", MetricName: "barometric_altitude", KeepMax: false},
	"furthest_flown": {Name: "furthest_flown", MetricName: "distance_flown", KeepMax: true},
	"longest_route":  {Name: "longest_route", MetricName: "route_distance", KeepMax: true},
	"most_remaining": {Name: "most_remaining", MetricName: "distance_remaining", KeepMax: true},
}

// bestFirstSQL returns the ORDER BY direction that puts the best record first
// for this category (used for read LIMIT and for trim-to-100).
func (c recordCategory) bestFirstSQL() string {
	if c.KeepMax {
		return "DESC"
	}
	return "ASC"
}

// allPeriodTypes lists every period_type in records.period_type CHECK order.
var allPeriodTypes = []string{"24h", "7d", "30d", "90d", "365d", "all_time"}

// periodWindow returns the sliding window for a windowed period_type. ok is
// false for "all_time", which has no window.
func periodWindow(periodType string) (time.Duration, bool) {
	switch periodType {
	case "24h":
		return 24 * time.Hour, true
	case "7d":
		return 7 * 24 * time.Hour, true
	case "30d":
		return 30 * 24 * time.Hour, true
	case "90d":
		return 90 * 24 * time.Hour, true
	case "365d":
		return 365 * 24 * time.Hour, true
	default:
		return 0, false
	}
}

// isValidPeriodType reports whether p is an accepted period_type.
func isValidPeriodType(p string) bool {
	if p == "all_time" {
		return true
	}
	_, ok := periodWindow(p)
	return ok
}

// periodsForFirstSeen returns the period_types whose window contains firstSeen,
// plus always "all_time". A freshly-evaluated flight normally lands in all of
// them; an old firstSeen (e.g. after downtime) is excluded from windows it has
// already fallen out of.
func periodsForFirstSeen(firstSeen, now time.Time) []string {
	var out []string
	for _, p := range allPeriodTypes {
		window, ok := periodWindow(p)
		if !ok { // all_time
			out = append(out, p)
			continue
		}
		if !firstSeen.Before(now.Add(-window)) {
			out = append(out, p)
		}
	}
	return out
}
