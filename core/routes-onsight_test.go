package main

import (
	"fmt"
	"testing"
	"time"
)

func TestClaimReservesCallsignOnce(t *testing.T) {
	r := newRouteOnSight()

	if !r.claim("SAS1456") {
		t.Fatal("first claim was refused, want it granted")
	}
	if r.claim("SAS1456") {
		t.Fatal("second claim was granted while the first lookup is still in flight")
	}
}

func TestClaimGrantedAgainAfterRouteFound(t *testing.T) {
	r := newRouteOnSight()
	r.claim("SAS1456")
	r.release("SAS1456", true, r.unknownCooldown)

	if !r.claim("SAS1456") {
		t.Fatal("claim refused after a successful lookup released the callsign")
	}
}

func TestClaimRefusedDuringCooldown(t *testing.T) {
	r := newRouteOnSight()
	r.claim("CAT250")
	r.release("CAT250", false, r.unknownCooldown)

	if r.claim("CAT250") {
		t.Fatal("claim granted while the callsign is in cooldown")
	}
}

func TestClaimGrantedAfterCooldownExpires(t *testing.T) {
	r := newRouteOnSight()
	r.unknownCooldown = -time.Second // expired the moment it is stored

	r.claim("CAT250")
	r.release("CAT250", false, r.unknownCooldown)

	if !r.claim("CAT250") {
		t.Fatal("claim refused after the cooldown had expired")
	}
}

func TestReleaseUsesTheCooldownItIsGiven(t *testing.T) {
	r := newRouteOnSight()
	r.claim("SAS1456")
	now := time.Now()
	r.release("SAS1456", false, r.errorCooldown)

	until, ok := r.cooldown["SAS1456"]
	if !ok {
		t.Fatal("no cooldown recorded for an unmatched callsign")
	}

	// Verify the cooldown is approximately errorCooldown (2 min), not unknownCooldown (30 min).
	// If release() ignored its parameter and used unknownCooldown, until would be ~30 min away, not ~2 min.
	expected := now.Add(r.errorCooldown)
	tolerance := 100 * time.Millisecond

	if until.Before(expected.Add(-tolerance)) || until.After(expected.Add(tolerance)) {
		t.Fatalf("cooldown runs to %v, want around %v (±%v ms)", until, expected, tolerance.Milliseconds())
	}
}

func TestPruneDropsExpiredCooldownsOnly(t *testing.T) {
	r := newRouteOnSight()
	for i := 0; i < routeCooldownPruneAt; i++ {
		r.cooldown[fmt.Sprintf("OLD%d", i)] = time.Now().Add(-time.Minute)
	}
	r.cooldown["LIVE1"] = time.Now().Add(time.Hour)

	r.claim("TRIGGER") // claim sweeps before it reserves

	if _, ok := r.cooldown["OLD0"]; ok {
		t.Error("an expired cooldown survived the sweep")
	}
	if _, ok := r.cooldown["LIVE1"]; !ok {
		t.Error("a live cooldown was swept away")
	}
}

func TestRouteCandidatesSelectsAircraftWithoutRoute(t *testing.T) {
	snapshot := []Aircraft{{Hex: "abc123", Flight: "SAS1456"}}

	got := routeCandidates(snapshot, map[string]aircraftEnrichment{"abc123": {}})

	if len(got) != 1 || got[0].Flight != "SAS1456" {
		t.Fatalf("got %+v, want the single aircraft that has no route", got)
	}
}

func TestRouteCandidatesSkipsAircraftThatAlreadyHaveARoute(t *testing.T) {
	destination := "ESGG"
	snapshot := []Aircraft{{Hex: "abc123", Flight: "RYR4TR"}}
	enrichment := map[string]aircraftEnrichment{
		"abc123": {DestinationIcao: &destination},
	}

	if got := routeCandidates(snapshot, enrichment); len(got) != 0 {
		t.Fatalf("selected %d candidates, want 0 — the route is already known", len(got))
	}
}

func TestRouteCandidatesSkipsEmptyCallsigns(t *testing.T) {
	snapshot := []Aircraft{{Hex: "abc123", Flight: ""}}

	if got := routeCandidates(snapshot, map[string]aircraftEnrichment{"abc123": {}}); len(got) != 0 {
		t.Fatalf("selected %d candidates, want 0 — there is no callsign to look up", len(got))
	}
}

func TestRouteCandidatesReturnsNothingWhenEnrichmentQueryFailed(t *testing.T) {
	// enrichAircraftSnapshot returns an empty map, not one entry per hex, when
	// its query fails. If routeCandidates read that the same as "no aircraft
	// here has a route", every visible callsign would become a candidate —
	// including ones whose routes are already known — every single tick for
	// as long as the failure lasts. It must select nothing instead and let
	// the next tick's enrichment try again.
	snapshot := []Aircraft{
		{Hex: "abc123", Flight: "SAS1456"},
		{Hex: "def456", Flight: "RYR4TR"},
	}

	got := routeCandidates(snapshot, map[string]aircraftEnrichment{})

	if len(got) != 0 {
		t.Fatalf("selected %d candidates from a snapshot paired with an empty enrichment map, want 0 — an empty map alongside a non-empty snapshot means the enrichment query failed, not that every aircraft is routeless", len(got))
	}
}

func TestRouteCandidatesCapsAtMaxRouteOnSightBatch(t *testing.T) {
	// A cold start against an empty route_data table makes every aircraft in
	// range a candidate on the very first tick. Without a cap here, that one
	// tick could send a request carrying every aircraft currently in range,
	// far beyond the 100-callsign batch size updateRoutes enforces in
	// routes.go for the same reason.
	snapshot := make([]Aircraft, 0, maxRouteOnSightBatch+10)
	enrichment := make(map[string]aircraftEnrichment, maxRouteOnSightBatch+10)
	for i := 0; i < maxRouteOnSightBatch+10; i++ {
		hex := fmt.Sprintf("hex%d", i)
		snapshot = append(snapshot, Aircraft{Hex: hex, Flight: fmt.Sprintf("FLT%d", i)})
		enrichment[hex] = aircraftEnrichment{} // a real "no route yet" answer for every hex
	}

	got := routeCandidates(snapshot, enrichment)

	if len(got) != maxRouteOnSightBatch {
		t.Fatalf("selected %d candidates, want the batch capped at %d", len(got), maxRouteOnSightBatch)
	}
}

func TestRouteCandidatesDeduplicatesCallsigns(t *testing.T) {
	snapshot := []Aircraft{
		{Hex: "abc123", Flight: "SAS1456"},
		{Hex: "def456", Flight: "SAS1456"},
	}
	enrichment := map[string]aircraftEnrichment{
		"abc123": {},
		"def456": {},
	}

	got := routeCandidates(snapshot, enrichment)

	if len(got) != 1 {
		t.Fatalf("selected %d candidates, want 1 — one callsign is asked about once", len(got))
	}
}

func TestRouteLookupSubjectsCopiesLivePositionIntoLastSeen(t *testing.T) {
	subjects := routeLookupSubjects([]Aircraft{{Flight: "SAS1456", Lat: 57.1, Lon: 12.28}})

	if len(subjects) != 1 {
		t.Fatalf("got %d subjects, want 1", len(subjects))
	}
	if !subjects[0].LastSeenLat.Valid || subjects[0].LastSeenLat.Float64 != 57.1 {
		t.Errorf("LastSeenLat = %+v, want 57.1 marked valid — buildRouteApiRequestBody reads it", subjects[0].LastSeenLat)
	}
	if !subjects[0].LastSeenLon.Valid || subjects[0].LastSeenLon.Float64 != 12.28 {
		t.Errorf("LastSeenLon = %+v, want 12.28 marked valid", subjects[0].LastSeenLon)
	}
}
