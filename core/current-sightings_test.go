package main

import (
	"sync"
	"testing"
	"time"
)

// A fixed distance function keyed on latitude keeps the tests independent of
// LAT/LON environment variables and of cheap-ruler's exact arithmetic.
func fakeDistance(lat, lon float64) float64 {
	return lat
}

func TestBuildCurrentSightingsSortsNearestFirst(t *testing.T) {
	aircraft := []Aircraft{
		{Hex: "aaa111", Lat: 30},
		{Hex: "bbb222", Lat: 10},
		{Hex: "ccc333", Lat: 20},
	}

	got := buildCurrentSightings(aircraft, nil, 1785399041, fakeDistance)

	want := []string{"bbb222", "ccc333", "aaa111"}
	if len(got) != len(want) {
		t.Fatalf("got %d rows want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Hex != want[i] {
			t.Errorf("position %d: got %s want %s", i, got[i].Hex, want[i])
		}
	}
}

func TestBuildCurrentSightingsPrefersFeedValuesOverDatabase(t *testing.T) {
	dbReg := "SE-DBB"
	dbType := "B737"
	aircraft := []Aircraft{{Hex: "aaa111", R: "SE-RTM", T: "B38M", Lat: 5}}
	enrichment := map[string]aircraftEnrichment{
		"aaa111": {Registration: &dbReg, IcaoType: &dbType},
	}

	got := buildCurrentSightings(aircraft, enrichment, 1785399041, fakeDistance)

	if got[0].Registration != "SE-RTM" {
		t.Errorf("registration: got %s want SE-RTM", got[0].Registration)
	}
	if got[0].Type != "B38M" {
		t.Errorf("type: got %s want B38M", got[0].Type)
	}
}

func TestBuildCurrentSightingsFallsBackToDatabaseValues(t *testing.T) {
	dbReg := "SE-DBB"
	dbType := "B737"
	aircraft := []Aircraft{{Hex: "aaa111", Lat: 5}}
	enrichment := map[string]aircraftEnrichment{
		"aaa111": {Registration: &dbReg, IcaoType: &dbType},
	}

	got := buildCurrentSightings(aircraft, enrichment, 1785399041, fakeDistance)

	if got[0].Registration != "SE-DBB" {
		t.Errorf("registration: got %s want SE-DBB", got[0].Registration)
	}
	if got[0].Type != "B737" {
		t.Errorf("type: got %s want B737", got[0].Type)
	}
}

// Airline and operator stay separate fields: most aircraft here are not
// scheduled airline traffic, and putting a private owner's name in a column
// labelled Airline would misrepresent the data.
func TestBuildCurrentSightingsKeepsAirlineAndOperatorSeparate(t *testing.T) {
	owner := "Some Private Owner"
	aircraft := []Aircraft{{Hex: "aaa111", Lat: 5}}
	enrichment := map[string]aircraftEnrichment{
		"aaa111": {RegisteredOwner: &owner},
	}

	got := buildCurrentSightings(aircraft, enrichment, 1785399041, fakeDistance)

	if got[0].Airline != nil {
		t.Errorf("airline: got %v want nil", *got[0].Airline)
	}
	if got[0].Operator == nil || *got[0].Operator != owner {
		t.Errorf("operator: got %v want %s", got[0].Operator, owner)
	}
}

func TestBuildCurrentSightingsDerivesLastSeenFromSeen(t *testing.T) {
	var nowEpoch float64 = 1785399041
	aircraft := []Aircraft{{Hex: "aaa111", Lat: 5, Seen: 12}}

	got := buildCurrentSightings(aircraft, nil, nowEpoch, fakeDistance)

	want := time.Unix(int64(nowEpoch), 0).Add(-12 * time.Second)
	if !got[0].LastSeen.Equal(want) {
		t.Errorf("last seen: got %v want %v", got[0].LastSeen, want)
	}
}

func TestBuildCurrentSightingsLeavesUnknownDescriptionNil(t *testing.T) {
	aircraft := []Aircraft{{Hex: "aaa111", Lat: 5}}

	got := buildCurrentSightings(aircraft, nil, 1785399041, fakeDistance)

	if got[0].TypeDescription != nil {
		t.Errorf("type description: got %v want nil", *got[0].TypeDescription)
	}
}

func TestCurrentSightingsStoreReturnsWhatWasStored(t *testing.T) {
	store := &currentSightingsStore{}
	generatedAt := time.Unix(1785399041, 0)

	store.replace([]CurrentSighting{{Hex: "aaa111"}}, generatedAt)
	got, at := store.snapshot()

	if len(got) != 1 || got[0].Hex != "aaa111" {
		t.Errorf("aircraft: got %v want one row with hex aaa111", got)
	}
	if !at.Equal(generatedAt) {
		t.Errorf("generatedAt: got %v want %v", at, generatedAt)
	}
}

// snapshot must hand back a copy: the API goroutine ranges over the result
// while the 2s ticker is replacing the store's contents.
func TestCurrentSightingsStoreSnapshotIsACopy(t *testing.T) {
	store := &currentSightingsStore{}
	store.replace([]CurrentSighting{{Hex: "aaa111"}}, time.Now())

	got, _ := store.snapshot()
	got[0].Hex = "mutated"

	again, _ := store.snapshot()
	if again[0].Hex != "aaa111" {
		t.Errorf("store was mutated through the snapshot: got %s", again[0].Hex)
	}
}

func TestCurrentSightingsStoreIsRaceFree(t *testing.T) {
	store := &currentSightingsStore{}
	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				store.replace([]CurrentSighting{{Hex: "aaa111", DistanceKm: float64(n)}}, time.Now())
			}
		}(i)
	}

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				aircraft, _ := store.snapshot()
				for range aircraft {
				}
			}
		}()
	}

	wg.Wait()
}
