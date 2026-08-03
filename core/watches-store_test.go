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
