package main

import (
	"fmt"
	"testing"
	"time"
)

// tick is one 2-second ingest tick as the queue sees it: the moment it happens,
// the subject the snapshot carried for our aircraft, and whether the aircraft
// was in the snapshot at all.
type tick struct {
	at      time.Duration
	subject watchSubject
	visible bool
}

var (
	pendingBare     = watchSubject{Hex: "78026e"}
	pendingCallsign = watchSubject{Hex: "78026e", Callsign: "CAO1160"}
	pendingFull     = watchSubject{Hex: "78026e", Callsign: "CAO1160", Origin: []string{"PIK"}, Destination: []string{"CTU"}}
)

func TestPendingWatchQueueReleaseConditions(t *testing.T) {
	key := watchKey{WatchID: 1, Hex: "78026e"}
	base := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name      string
		enqueued  watchSubject
		delay     time.Duration
		ticks     []tick
		releaseAt int // index into ticks; -1 means never released
		wantLeft  bool
	}{
		{
			name:     "callsign and route together release at once",
			enqueued: pendingBare,
			delay:    30 * time.Second,
			ticks: []tick{
				{at: 2 * time.Second, subject: pendingBare, visible: true},
				{at: 4 * time.Second, subject: pendingFull, visible: true},
			},
			releaseAt: 1,
		},
		{
			name:     "a callsign without a route waits out the grace",
			enqueued: pendingBare,
			delay:    30 * time.Second,
			ticks: []tick{
				{at: 4 * time.Second, subject: pendingCallsign, visible: true},
				{at: 8 * time.Second, subject: pendingCallsign, visible: true},
				{at: 10 * time.Second, subject: pendingCallsign, visible: true},
			},
			releaseAt: 2, // grace runs from the tick the callsign appeared: 4s + 6s
		},
		{
			name:     "a callsign present at enqueue starts the grace immediately",
			enqueued: pendingCallsign,
			delay:    30 * time.Second,
			ticks: []tick{
				{at: 4 * time.Second, subject: pendingCallsign, visible: true},
				{at: 6 * time.Second, subject: pendingCallsign, visible: true},
			},
			releaseAt: 1,
		},
		{
			name:     "no callsign waits for the deadline",
			enqueued: pendingBare,
			delay:    30 * time.Second,
			ticks: []tick{
				{at: 20 * time.Second, subject: pendingBare, visible: true},
				{at: 30 * time.Second, subject: pendingBare, visible: true},
			},
			releaseAt: 1,
		},
		{
			name:     "a callsign arriving late still buys the route its full grace",
			enqueued: pendingBare,
			delay:    30 * time.Second,
			ticks: []tick{
				{at: 28 * time.Second, subject: pendingCallsign, visible: true},
				{at: 30 * time.Second, subject: pendingCallsign, visible: true},
				{at: 34 * time.Second, subject: pendingCallsign, visible: true},
			},
			releaseAt: 2, // 28s + 6s, past the 30s deadline
		},
		{
			name:     "an aircraft that vanishes is not released early",
			enqueued: pendingBare,
			delay:    30 * time.Second,
			ticks: []tick{
				{at: 4 * time.Second, visible: false},
				{at: 10 * time.Second, visible: false},
			},
			releaseAt: -1,
		},
		{
			name:     "an aircraft that vanished is marked when its deadline passes",
			enqueued: pendingBare,
			delay:    30 * time.Second,
			ticks: []tick{
				{at: 4 * time.Second, subject: pendingCallsign, visible: true},
				{at: 30 * time.Second, visible: false},
			},
			releaseAt: 1,
			wantLeft:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := newPendingWatchQueue()
			if !q.enqueue(key, tc.enqueued, base, tc.delay) {
				t.Fatal("enqueue refused an entry into an empty queue")
			}

			got := -1
			var released pendingWatchNotification
			for i, tk := range tc.ticks {
				subjects := map[string]watchSubject{}
				if tk.visible {
					subjects[key.Hex] = tk.subject
				}
				out := q.refresh(subjects, base.Add(tk.at))
				if len(out) == 0 {
					continue
				}
				if got != -1 {
					t.Fatalf("released twice, at tick %d and tick %d", got, i)
				}
				got, released = i, out[0]
			}

			if got != tc.releaseAt {
				t.Fatalf("released at tick %d, want %d", got, tc.releaseAt)
			}
			if got == -1 {
				if q.len() != 1 {
					t.Errorf("an unreleased entry should still be queued, len = %d", q.len())
				}
				return
			}
			if released.LeftCoverage != tc.wantLeft {
				t.Errorf("LeftCoverage = %t, want %t", released.LeftCoverage, tc.wantLeft)
			}
			if q.len() != 0 {
				t.Errorf("a released entry should leave the queue, len = %d", q.len())
			}
		})
	}
}

func TestPendingWatchQueueReleasesImmediatelyWithZeroDelay(t *testing.T) {
	q := newPendingWatchQueue()
	key := watchKey{WatchID: 1, Hex: "78026e"}
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)

	if !q.enqueue(key, pendingBare, now, 0) {
		t.Fatal("enqueue refused an entry into an empty queue")
	}

	// Same tick, same instant: switching the feature off must not cost a tick.
	released := q.refresh(map[string]watchSubject{key.Hex: pendingBare}, now)
	if len(released) != 1 {
		t.Fatalf("released %d entries, want 1", len(released))
	}
	if released[0].LeftCoverage {
		t.Error("an aircraft in the snapshot must not be marked as gone")
	}
}

func TestPendingWatchQueueKeepsTheLastKnownSubjectAcrossAGap(t *testing.T) {
	q := newPendingWatchQueue()
	key := watchKey{WatchID: 1, Hex: "78026e"}
	base := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)

	q.enqueue(key, pendingBare, base, 30*time.Second)

	// The callsign arrives, which starts the route grace.
	if out := q.refresh(map[string]watchSubject{key.Hex: pendingCallsign}, base.Add(2*time.Second)); len(out) != 0 {
		t.Fatalf("released %d entries while the route grace was still running", len(out))
	}
	// The aircraft then drops out of the feed. The grace keeps running and the
	// subject must not be reset to the bare one it was enqueued with.
	if out := q.refresh(map[string]watchSubject{}, base.Add(4*time.Second)); len(out) != 0 {
		t.Fatalf("released %d entries early just because the aircraft vanished", len(out))
	}

	released := q.refresh(map[string]watchSubject{}, base.Add(8*time.Second))
	if len(released) != 1 {
		t.Fatalf("released %d entries, want 1", len(released))
	}
	if released[0].Subject.Callsign != "CAO1160" {
		t.Errorf("callsign = %q, want CAO1160 — the last seen subject should be kept", released[0].Subject.Callsign)
	}
	if !released[0].LeftCoverage {
		t.Error("a release with the aircraft out of the snapshot should be marked")
	}
}

func TestPendingWatchQueueFailsOpenPastTheCap(t *testing.T) {
	q := newPendingWatchQueue()
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)

	for i := 0; i < pendingWatchCap; i++ {
		key := watchKey{WatchID: 1, Hex: fmt.Sprintf("hex%04d", i)}
		if !q.enqueue(key, pendingBare, now, 30*time.Second) {
			t.Fatalf("enqueue refused entry %d, below the cap", i)
		}
	}

	overflow := watchKey{WatchID: 1, Hex: "overflow"}
	if q.enqueue(overflow, pendingBare, now, 30*time.Second) {
		t.Error("enqueue accepted an entry past the cap; the caller must be told to send now")
	}
	if q.len() != pendingWatchCap {
		t.Errorf("queue length = %d, want %d", q.len(), pendingWatchCap)
	}
}

func TestPendingWatchQueueReleasesInAStableOrder(t *testing.T) {
	q := newPendingWatchQueue()
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)

	for _, key := range []watchKey{
		{WatchID: 2, Hex: "bbbb"},
		{WatchID: 1, Hex: "cccc"},
		{WatchID: 1, Hex: "aaaa"},
	} {
		q.enqueue(key, watchSubject{Hex: key.Hex}, now, 0)
	}

	released := q.refresh(map[string]watchSubject{}, now)
	got := make([]watchKey, 0, len(released))
	for _, r := range released {
		got = append(got, r.Key)
	}
	want := []watchKey{{WatchID: 1, Hex: "aaaa"}, {WatchID: 1, Hex: "cccc"}, {WatchID: 2, Hex: "bbbb"}}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("release order = %v, want %v", got, want)
		}
	}
}
