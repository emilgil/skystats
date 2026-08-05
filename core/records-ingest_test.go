package main

import (
	"strings"
	"testing"
)

func TestFlightHistoryUpsertSQL_RefreshesIdentityColumnsWhenNonEmpty(t *testing.T) {
	// A flight is written by whichever of the seven category jobs reaches it
	// first, and the motion job (every 120s) usually beats route matching
	// (every 300s) to it. If the conflict clause leaves these columns alone,
	// the first writer's empty callsign is locked in permanently.
	query := flightHistoryUpsertSQL([]string{"ground_speed"})

	for _, col := range []string{"flight", "registration", "type"} {
		want := col + " = COALESCE(NULLIF(EXCLUDED." + col + ", ''), flight_history." + col + ")"
		if !strings.Contains(query, want) {
			t.Errorf("expected conflict clause to refresh %q via %q, got:\n%s", col, want, query)
		}
	}
}

func TestFlightHistoryUpsertSQL_PlacesMetricColumnsAfterIdentityColumns(t *testing.T) {
	query := flightHistoryUpsertSQL([]string{"ground_speed", "true_air_speed"})

	if !strings.Contains(query, "(hex, flight, registration, type, first_seen, last_seen, ground_speed, true_air_speed)") {
		t.Errorf("unexpected column list, got:\n%s", query)
	}
	if !strings.Contains(query, "VALUES ($1, $2, $3, $4, $5, $6, $7, $8)") {
		t.Errorf("expected one placeholder per column, got:\n%s", query)
	}
}

func TestRecordsUpsertSQL_KeepsTheHigherValueForMaxCategory(t *testing.T) {
	// fastest/highest rewrite the same row every tick while the aircraft is
	// still airborne, so the conflict clause decides the record, not the last
	// writer.
	query := recordsUpsertSQL(recordCategories["fastest"])

	if !strings.Contains(query, "metric_value = GREATEST(records.metric_value, EXCLUDED.metric_value)") {
		t.Errorf("expected GREATEST for a KeepMax category, got:\n%s", query)
	}
}

func TestRecordsUpsertSQL_KeepsTheLowerValueForMinCategory(t *testing.T) {
	query := recordsUpsertSQL(recordCategories["slowest"])

	if !strings.Contains(query, "metric_value = LEAST(records.metric_value, EXCLUDED.metric_value)") {
		t.Errorf("expected LEAST for a KeepMin category, got:\n%s", query)
	}
}

func TestRecordsUpsertSQL_DetailsFollowTheWinningMetricValue(t *testing.T) {
	// details holds the supporting readings (ias/tas, geometric altitude) for
	// the recorded moment. Overwriting it unconditionally while metric_value
	// keeps an earlier, better value would describe two different moments in
	// one row.
	max := recordsUpsertSQL(recordCategories["fastest"])
	if !strings.Contains(max, "details = CASE WHEN EXCLUDED.metric_value >= records.metric_value") {
		t.Errorf("expected details to follow the winning value for a KeepMax category, got:\n%s", max)
	}

	min := recordsUpsertSQL(recordCategories["slowest"])
	if !strings.Contains(min, "details = CASE WHEN EXCLUDED.metric_value <= records.metric_value") {
		t.Errorf("expected details to follow the winning value for a KeepMin category, got:\n%s", min)
	}
}

func TestRecordsUpsertSQL_RefreshesIdentityColumnsWhenNonEmpty(t *testing.T) {
	// The Record Holders table reads records.flight directly and joins
	// route_data on it for the airline name, so an empty callsign here costs
	// both the callsign and the airline in the UI.
	query := recordsUpsertSQL(recordCategories["fastest"])

	for _, col := range []string{"flight", "registration", "type"} {
		want := col + " = COALESCE(NULLIF(EXCLUDED." + col + ", ''), records." + col + ")"
		if !strings.Contains(query, want) {
			t.Errorf("expected conflict clause to refresh %q via %q, got:\n%s", col, want, query)
		}
	}
}
