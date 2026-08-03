package main

import (
	"testing"
	"time"
)

func TestActiveMatchStoreApplyRefreshesAndRemoves(t *testing.T) {
	store := newActiveMatchStore()
	now := time.Unix(1785399041, 0)

	store.apply(map[watchKey]bool{
		{WatchID: 1, Hex: "aaa111"}: true,
		{WatchID: 1, Hex: "bbb222"}: true,
	}, nil, now)

	later := now.Add(time.Minute)
	store.apply(map[watchKey]bool{{WatchID: 1, Hex: "aaa111"}: true},
		[]watchKey{{WatchID: 1, Hex: "bbb222"}}, later)

	got := store.snapshot()
	if len(got) != 1 {
		t.Fatalf("got %d matches want 1", len(got))
	}
	if ts, ok := got[watchKey{WatchID: 1, Hex: "aaa111"}]; !ok || !ts.Equal(later) {
		t.Errorf("aaa111 should be present with the refreshed timestamp, got %v (present=%v)", ts, ok)
	}
}

func TestActiveMatchStoreSnapshotIsACopy(t *testing.T) {
	store := newActiveMatchStore()
	now := time.Unix(1785399041, 0)
	store.apply(map[watchKey]bool{{WatchID: 1, Hex: "aaa111"}: true}, nil, now)

	got := store.snapshot()
	delete(got, watchKey{WatchID: 1, Hex: "aaa111"})

	if len(store.snapshot()) != 1 {
		t.Error("mutating the snapshot must not affect the store")
	}
}

func TestActiveMatchStoreForgetDropsOnlyThatWatch(t *testing.T) {
	store := newActiveMatchStore()
	now := time.Unix(1785399041, 0)
	store.apply(map[watchKey]bool{
		{WatchID: 1, Hex: "aaa111"}: true,
		{WatchID: 2, Hex: "aaa111"}: true,
		{WatchID: 2, Hex: "bbb222"}: true,
	}, nil, now)

	store.forget(2)

	got := store.snapshot()
	if len(got) != 1 {
		t.Fatalf("got %d matches want 1", len(got))
	}
	if _, ok := got[watchKey{WatchID: 1, Hex: "aaa111"}]; !ok {
		t.Error("watch 1's match should have survived forget(2)")
	}
}

func TestWatchStorePublishDiscardsStaleGeneration(t *testing.T) {
	store := &watchStore{}

	// Capture gen the way enabled() does before starting its fetch.
	gen := store.gen

	// Simulate a write landing while that fetch was in flight.
	store.invalidate()

	// The fetch (started before the invalidate) now tries to publish its
	// now-stale snapshot.
	store.publish([]Watch{{ID: 1}}, gen)

	if store.loaded {
		t.Error("publish with a stale generation must not mark the cache loaded")
	}
	if len(store.watches) != 0 {
		t.Errorf("publish with a stale generation must not store watches, got %d", len(store.watches))
	}
}

func TestWatchStorePublishCommitsCurrentGeneration(t *testing.T) {
	store := &watchStore{}

	gen := store.gen
	watches := []Watch{{ID: 1}, {ID: 2}}

	store.publish(watches, gen)

	if !store.loaded {
		t.Error("publish with an unchanged generation should mark the cache loaded")
	}
	if len(store.watches) != 2 {
		t.Fatalf("got %d watches want 2", len(store.watches))
	}
}
