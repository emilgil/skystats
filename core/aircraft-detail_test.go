package main

import "testing"

func TestBuildPersonalBestRecordsAllNil(t *testing.T) {
	got := buildPersonalBestRecords(nil, nil, nil, nil, nil, nil, nil, map[string]bool{})
	if len(got) != 0 {
		t.Errorf("all nil: got %d records, want 0", len(got))
	}
}

func TestBuildPersonalBestRecordsPartial(t *testing.T) {
	gs := 390.0
	got := buildPersonalBestRecords(&gs, nil, nil, nil, nil, nil, nil, map[string]bool{})
	if len(got) != 1 {
		t.Fatalf("partial: got %d records, want 1: %+v", len(got), got)
	}
	want := personalBestRecord{Category: "fastest", MetricName: "ground_speed", Value: 390.0, IsGlobalRecord: false}
	if got[0] != want {
		t.Errorf("partial: got %+v want %+v", got[0], want)
	}
}

func TestBuildPersonalBestRecordsFullSetOrderAndMembership(t *testing.T) {
	maxGs, minGs := 479.0, 210.0
	maxAlt, minAlt := 37000.0, 19000.0
	maxRouteDist, maxDistFlown, maxDistRemaining := 3016.0, 2615.0, 409.0
	global := map[string]bool{"fastest": true, "longest_route": true}

	got := buildPersonalBestRecords(&maxGs, &minGs, &maxAlt, &minAlt, &maxRouteDist, &maxDistFlown, &maxDistRemaining, global)

	want := []personalBestRecord{
		{Category: "fastest", MetricName: "ground_speed", Value: 479.0, IsGlobalRecord: true},
		{Category: "slowest", MetricName: "ground_speed", Value: 210.0, IsGlobalRecord: false},
		{Category: "highest", MetricName: "barometric_altitude", Value: 37000.0, IsGlobalRecord: false},
		{Category: "lowest", MetricName: "barometric_altitude", Value: 19000.0, IsGlobalRecord: false},
		{Category: "longest_route", MetricName: "route_distance", Value: 3016.0, IsGlobalRecord: true},
		{Category: "furthest_flown", MetricName: "distance_flown", Value: 2615.0, IsGlobalRecord: false},
		{Category: "most_remaining", MetricName: "distance_remaining", Value: 409.0, IsGlobalRecord: false},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d records, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("record %d: got %+v want %+v", i, got[i], want[i])
		}
	}
}
