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

func TestPartitionLoadedMatchesKeepsLiveMatchesWithTheirRealTimestamp(t *testing.T) {
	now := time.Unix(1785399041, 0)
	// A short restart: the match was confirmed two minutes before startup, well
	// inside the grace window, so it must load and keep its own timestamp
	// rather than being restamped to now.
	confirmedAt := now.Add(-2 * time.Minute)

	active, expired := partitionLoadedMatches([]loadedMatch{
		{Key: watchKey{WatchID: 1, Hex: "aaa111"}, MatchedAt: confirmedAt},
	}, now, 10*time.Minute)

	if len(expired) != 0 {
		t.Errorf("a match confirmed two minutes ago must not expire, got %v", expired)
	}
	ts, ok := active[watchKey{WatchID: 1, Hex: "aaa111"}]
	if !ok {
		t.Fatal("the live match should have loaded")
	}
	if !ts.Equal(confirmedAt) {
		t.Errorf("got %v want the persisted matched_at %v, not a fresh stamp", ts, confirmedAt)
	}
}

func TestPartitionLoadedMatchesDropsWhatExpiredDuringDowntime(t *testing.T) {
	now := time.Unix(1785399041, 0)
	// Three days of downtime: the match ended long ago and must not seed the
	// cache, or the aircraft's next sighting is silently swallowed.
	active, expired := partitionLoadedMatches([]loadedMatch{
		{Key: watchKey{WatchID: 1, Hex: "aaa111"}, MatchedAt: now.Add(-72 * time.Hour)},
		{Key: watchKey{WatchID: 1, Hex: "bbb222"}, MatchedAt: now.Add(-time.Minute)},
	}, now, 10*time.Minute)

	if len(active) != 1 {
		t.Fatalf("got %d active want only bbb222", len(active))
	}
	if _, ok := active[watchKey{WatchID: 1, Hex: "bbb222"}]; !ok {
		t.Error("the recently confirmed match should have loaded")
	}
	if len(expired) != 1 || expired[0].Hex != "aaa111" {
		t.Errorf("expired: got %v want [aaa111]", expired)
	}
}

func TestPartitionLoadedMatchesKeepsAMatchExactlyOnTheBoundary(t *testing.T) {
	now := time.Unix(1785399041, 0)
	grace := 10 * time.Minute

	// diffMatches ends a match on now.Sub(last) > grace, so loading must use
	// the same strict comparison or the two disagree at the boundary.
	active, expired := partitionLoadedMatches([]loadedMatch{
		{Key: watchKey{WatchID: 1, Hex: "aaa111"}, MatchedAt: now.Add(-grace)},
	}, now, grace)

	if len(active) != 1 || len(expired) != 0 {
		t.Errorf("a match exactly at the grace boundary should load, got %d active %d expired", len(active), len(expired))
	}
}

func TestPartitionLoadedMatchesHandlesAnEmptyTable(t *testing.T) {
	active, expired := partitionLoadedMatches(nil, time.Unix(1785399041, 0), 10*time.Minute)

	if len(active) != 0 || len(expired) != 0 {
		t.Errorf("got %d active %d expired, want neither", len(active), len(expired))
	}
}

func TestActiveMatchStoreApplyDiscardsForgottenWatch(t *testing.T) {
	store := newActiveMatchStore()
	now := time.Unix(1785399041, 0)
	store.apply(map[watchKey]bool{
		{WatchID: 1, Hex: "aaa111"}: true,
		{WatchID: 2, Hex: "bbb222"}: true,
	}, nil, now)

	// A tick opens, taking the state it will diff against.
	store.begin()

	// The user edits watch 2 while that tick is still in flight; the update
	// transaction has already deleted watch 2's rows from Postgres.
	store.forget(2)

	// The tick now folds in its result, which still believes watch 2 matched.
	store.apply(map[watchKey]bool{
		{WatchID: 1, Hex: "aaa111"}: true,
		{WatchID: 2, Hex: "bbb222"}: true,
	}, nil, now.Add(2*time.Second))

	got := store.snapshot()
	if _, ok := got[watchKey{WatchID: 2, Hex: "bbb222"}]; ok {
		t.Error("apply must not resurrect a key whose watch was forgotten mid-tick; the row is gone from Postgres")
	}
	if _, ok := got[watchKey{WatchID: 1, Hex: "aaa111"}]; !ok {
		t.Error("an untouched watch's keys must still be applied")
	}
}

func TestActiveMatchStoreAcceptsForgottenWatchOnTheNextTick(t *testing.T) {
	store := newActiveMatchStore()
	now := time.Unix(1785399041, 0)

	store.begin()
	store.forget(2)
	store.apply(map[watchKey]bool{{WatchID: 2, Hex: "bbb222"}: true}, nil, now)

	// The next tick starts after the edit, so its matches are decided under the
	// new conditions and must be cached normally.
	store.begin()
	store.apply(map[watchKey]bool{{WatchID: 2, Hex: "bbb222"}: true}, nil, now.Add(2*time.Second))

	if _, ok := store.snapshot()[watchKey{WatchID: 2, Hex: "bbb222"}]; !ok {
		t.Error("the generation guard must only skip the one tick that straddled the forget")
	}
}

func TestActiveMatchStoreForgetLeavesOtherWatchesUnguarded(t *testing.T) {
	store := newActiveMatchStore()
	now := time.Unix(1785399041, 0)

	store.begin()
	store.forget(2)
	store.apply(map[watchKey]bool{
		{WatchID: 1, Hex: "aaa111"}: true,
		{WatchID: 3, Hex: "ccc333"}: true,
	}, nil, now)

	if len(store.snapshot()) != 2 {
		t.Errorf("forgetting watch 2 must not block watches 1 and 3, got %v", store.snapshot())
	}
}
