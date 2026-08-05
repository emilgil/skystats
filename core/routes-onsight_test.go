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
