package main

import (
	"testing"
	"time"
)

func TestFirstSeenTrackerKeepsFlagForWholeSighting(t *testing.T) {
	tracker := newFirstSeenTracker()
	now := time.Unix(1785399041, 0)
	grace := 10 * time.Minute

	// Tick 1: the aircraft is brand new.
	got := tracker.update([]string{"aaa111", "bbb222"}, map[string]bool{"aaa111": true}, now, grace)
	if !got["aaa111"] {
		t.Error("a brand new hex should be flagged on the tick it appears")
	}
	if got["bbb222"] {
		t.Error("an already-known hex should not be flagged")
	}

	// Tick 2, four minutes later: still the same sighting, still flagged.
	got = tracker.update([]string{"aaa111", "bbb222"}, nil, now.Add(4*time.Minute), grace)
	if !got["aaa111"] {
		t.Error("the flag should survive for the rest of the sighting")
	}
}

func TestFirstSeenTrackerExpiresAfterGrace(t *testing.T) {
	tracker := newFirstSeenTracker()
	now := time.Unix(1785399041, 0)
	grace := 10 * time.Minute

	tracker.update([]string{"aaa111"}, map[string]bool{"aaa111": true}, now, grace)

	// Gone from the snapshot but still inside the grace window.
	got := tracker.update(nil, nil, now.Add(5*time.Minute), grace)
	if !got["aaa111"] {
		t.Error("a brief dropout should not end the first sighting")
	}

	// Gone past the grace window: the first sighting is over for good.
	got = tracker.update(nil, nil, now.Add(20*time.Minute), grace)
	if got["aaa111"] {
		t.Error("the flag should expire once the grace window has passed")
	}

	// The same hex coming back later is no longer a first sighting.
	got = tracker.update([]string{"aaa111"}, nil, now.Add(30*time.Minute), grace)
	if got["aaa111"] {
		t.Error("a returning hex must not be flagged as first-ever seen again")
	}
}

func TestFirstSeenTrackerReturnsEmptySetWhenNothingIsNew(t *testing.T) {
	tracker := newFirstSeenTracker()
	got := tracker.update([]string{"aaa111", "bbb222"}, nil, time.Unix(1785399041, 0), 10*time.Minute)
	if len(got) != 0 {
		t.Errorf("got %d flagged hexes want 0", len(got))
	}
}

func TestFirstSeenTrackerRefreshKeepsLongSightingFlagged(t *testing.T) {
	tracker := newFirstSeenTracker()
	now := time.Unix(1785399041, 0)
	grace := 10 * time.Minute

	// t0: aircraft appears, brand new.
	tracker.update([]string{"aaa111"}, map[string]bool{"aaa111": true}, now, grace)

	// t0+5min: still visible in snapshot, should refresh timestamp.
	tracker.update([]string{"aaa111"}, nil, now.Add(5*time.Minute), grace)

	// t0+14min: still visible in snapshot. Without the refresh loop, the
	// hex timestamp would still be t0, making it 14 minutes old (> 10min grace),
	// so it would expire. With the refresh loop, it's been refreshed to t0+5min,
	// then t0+14min, staying within grace even as time advances.
	got := tracker.update([]string{"aaa111"}, nil, now.Add(14*time.Minute), grace)
	if !got["aaa111"] {
		t.Error("a long-duration sighting should stay flagged if aircraft keeps appearing in snapshots")
	}
}
