# Notification Deferral Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Hold a watch notification until the aircraft's callsign and route have had a bounded chance to arrive, so notifications stop going out identity-less seconds after first contact.

**Architecture:** A clock-free, database-free in-memory queue (`pendingWatchQueue`) sits between the watch engine's match detection and the Apprise send. `evaluateWatches` enqueues every started match instead of notifying, then each 2-second tick refreshes waiting entries from the fresh snapshot and releases those whose conditions are met. The per-tick push cap moves from match time to release time. Two smaller changes ride along: an age filter on the interesting-aircraft query and a route line in record notifications.

**Tech Stack:** Go 1.25.3 (toolchain at `~/.local/go/bin/go`), PostgreSQL 17 via pgx, Svelte 5 + Tailwind 4 + DaisyUI, Vite.

**Spec:** `docs/superpowers/specs/2026-08-06-notification-deferral-design.md`

## Global Constraints

- Setting key is exactly `notification_delay_seconds`. Default `30`. `0` disables deferral entirely and restores today's immediate-send behaviour.
- The route grace is a hard-coded `6 * time.Second` (3 ticks). It is **not** configurable.
- The queue cap is `500` entries and **fails open**: past the cap, new matches are sent immediately, never dropped.
- The late-notification marker text is exactly `Aircraft has left coverage`. All notification bodies in this codebase are English; do not write Swedish into any user-visible string.
- Maximum total wait for one entry is `delay + 6 s`. A callsign arriving just before the deadline still buys the route its full grace.
- The `watch_active_matches` row is still written at match time (unchanged). The `watch_notifications` history row moves to release time.
- **No migration.** `user_settings` is a key/value table; the database stays at schema v19. Do not add anything to `migrations/`.
- Run the Go suite from the repository root: `~/.local/go/bin/go test ./...`. It must be green before every commit.
- Follow the surrounding comment style: comments explain *why* a decision was made, not what the next line does.

---

### Task 1: The pending queue

A self-contained, fully unit-tested data structure. No engine wiring — nothing calls it yet after this task, and that is expected.

**Files:**
- Create: `core/watches-pending.go`
- Test: `core/watches-pending_test.go`

**Interfaces:**
- Consumes: `watchKey` (`core/watches-store.go:14`, fields `WatchID int`, `Hex string`) and `watchSubject` (`core/watches-match.go:41`, fields used here: `Hex`, `Callsign string`, `Origin []string`, `Destination []string`).
- Produces, relied on by Task 3:
  - `var pendingWatchNotifications = newPendingWatchQueue()` — created in Task 3, not here.
  - `newPendingWatchQueue() *pendingWatchQueue`
  - `(*pendingWatchQueue).enqueue(key watchKey, s watchSubject, now time.Time, delay time.Duration) bool` — returns `false` when the queue is full.
  - `(*pendingWatchQueue).refresh(subjects map[string]watchSubject, now time.Time) []pendingWatchNotification`
  - `(*pendingWatchQueue).len() int`
  - `type pendingWatchNotification struct { Key watchKey; Subject watchSubject; QueuedAt, Deadline, RouteDeadline time.Time; LeftCoverage bool }`
  - `hasRoute(s watchSubject) bool`

- [ ] **Step 1: Write the failing tests**

Create `core/watches-pending_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `~/.local/go/bin/go test ./core/ -run TestPendingWatchQueue`
Expected: FAIL to build — `undefined: newPendingWatchQueue` and `undefined: pendingWatchCap`.

- [ ] **Step 3: Write the implementation**

Create `core/watches-pending.go`:

```go
package main

import (
	"sort"
	"strings"
	"time"
)

// watchRouteGrace is how long a pending notification waits for a route once the
// callsign is known. On-sight route lookup normally answers in one to two
// seconds, so three ticks covers a slow adsb.im response — while keeping a
// callsign upstream has never heard of from holding the notification long.
const watchRouteGrace = 6 * time.Second

// pendingWatchCap bounds the queue. A broad watch ("distance under 100 km")
// can start matching every aircraft in range at once, and an entry lives for at
// most the configured delay plus watchRouteGrace, so in practice the queue
// stays small. The cap exists so it can never grow without bound, and it fails
// open: past it, enqueue refuses and the caller sends immediately. A late
// notification is a nuisance; a dropped one is a lost sighting.
const pendingWatchCap = 500

// pendingWatchNotification is one watch match whose notification is waiting for
// the enrichment the feed had not delivered when the match started.
type pendingWatchNotification struct {
	Key      watchKey
	Subject  watchSubject // refreshed from every tick that still sees the aircraft
	QueuedAt time.Time
	Deadline time.Time // QueuedAt plus the configured delay

	// RouteDeadline is zero until a callsign has been observed for this entry,
	// and watchRouteGrace past that moment afterwards.
	RouteDeadline time.Time

	// LeftCoverage is set at release when the aircraft was not in that tick's
	// snapshot, which turns the notification into an after-the-fact report.
	LeftCoverage bool
}

// pendingWatchQueue holds watch matches waiting for their callsign and route.
//
// It is deliberately clock-free and database-free — every method takes the
// current time — so the release rules can be exercised as a plain table rather
// than by sleeping.
type pendingWatchQueue struct {
	entries map[watchKey]*pendingWatchNotification
}

func newPendingWatchQueue() *pendingWatchQueue {
	return &pendingWatchQueue{entries: map[watchKey]*pendingWatchNotification{}}
}

func (q *pendingWatchQueue) len() int { return len(q.entries) }

// enqueue adds one started match, reporting false when the queue is full — the
// caller must then send straight away rather than drop the match.
//
// A delay of zero or less stores both deadlines at now, so the refresh later in
// the same tick releases the entry. That is how the feature switches off
// without a second code path through the send.
func (q *pendingWatchQueue) enqueue(key watchKey, s watchSubject, now time.Time, delay time.Duration) bool {
	if len(q.entries) >= pendingWatchCap {
		return false
	}

	e := &pendingWatchNotification{
		Key:      key,
		Subject:  s,
		QueuedAt: now,
		Deadline: now.Add(delay),
	}
	switch {
	case delay <= 0:
		e.Deadline = now
		e.RouteDeadline = now
	case hasCallsign(s):
		e.RouteDeadline = now.Add(watchRouteGrace)
	}

	q.entries[key] = e
	return true
}

// refresh updates every waiting entry from this tick's subjects and returns
// those ready to be sent, ordered by watch and hex so a tick's releases are
// reproducible.
//
// An aircraft missing from the snapshot is not released early. readsb drops an
// aircraft from the feed for the odd tick, and an on-sight route request
// already in flight can still come back — so a gap is treated as silence, not
// as departure. Only the release itself records that the aircraft was gone.
func (q *pendingWatchQueue) refresh(subjects map[string]watchSubject, now time.Time) []pendingWatchNotification {
	var released []pendingWatchNotification

	for key, e := range q.entries {
		s, visible := subjects[key.Hex]
		if visible {
			e.Subject = s
			if e.RouteDeadline.IsZero() && hasCallsign(s) {
				e.RouteDeadline = now.Add(watchRouteGrace)
			}
		}
		if !readyToRelease(e, now) {
			continue
		}
		e.LeftCoverage = !visible
		released = append(released, *e)
		delete(q.entries, key)
	}

	sort.Slice(released, func(i, j int) bool {
		if released[i].Key.WatchID != released[j].Key.WatchID {
			return released[i].Key.WatchID < released[j].Key.WatchID
		}
		return released[i].Key.Hex < released[j].Key.Hex
	})
	return released
}

// readyToRelease implements the three release conditions:
//
//  1. callsign and route are both known                    — the complete notification
//  2. callsign is known and the route grace has expired    — no route is coming
//  3. the deadline passed and no callsign ever arrived     — no identity is coming
//
// Condition 3 only applies while the callsign is missing, so a callsign
// arriving just before the deadline still buys the route its full grace. The
// longest an entry can therefore wait is the configured delay plus
// watchRouteGrace.
func readyToRelease(e *pendingWatchNotification, now time.Time) bool {
	if hasCallsign(e.Subject) {
		if hasRoute(e.Subject) {
			return true
		}
		return !now.Before(e.RouteDeadline)
	}
	return !now.Before(e.Deadline)
}

func hasCallsign(s watchSubject) bool { return strings.TrimSpace(s.Callsign) != "" }

// hasRoute mirrors the condition buildWatchMessage uses to decide whether to
// write a Route line. Waiting for anything it would not print would hold the
// notification for enrichment the user never sees.
func hasRoute(s watchSubject) bool { return len(s.Origin) > 0 && len(s.Destination) > 0 }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `~/.local/go/bin/go test ./core/ -run TestPendingWatchQueue -v`
Expected: PASS, all subtests.

Then run the whole suite to be sure nothing else broke:
Run: `~/.local/go/bin/go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add core/watches-pending.go core/watches-pending_test.go
git commit -m "feat: add the pending watch notification queue"
```

---

### Task 2: Notification message and config layer

Teaches the message builders about the two new pieces of information — that a notification may be arriving late, and that a record has a route — and adds the delay to the config the engine already loads once per tick. The single existing `NotifyWatch` caller is updated with a hard-coded `false` so the build stays green; Task 3 replaces it with the real value.

**Files:**
- Modify: `core/notifications.go` (`NotificationConfig` ~line 60, `loadConfig` ~line 125, `buildRecordMessage` ~line 253, `NotifyRecord` ~line 372, `buildWatchMessage` ~line 417, `NotifyWatch` ~line 467)
- Modify: `core/watches-engine.go:390` (one line, temporary)
- Test: `core/notifications_test.go`

**Interfaces:**
- Consumes: `getIntSetting(pg *postgres, key string, def int) int` (`core/records-jobs.go:12`); `(*NotificationService).lookupRoute(callsign string) (from, to string)` (`core/notifications.go:320`) — already returns `"", ""` for an empty callsign and on any query error.
- Produces, relied on by Task 3 and Task 4:
  - `NotificationConfig.DelaySeconds int`
  - `buildWatchMessage(watchName string, s watchSubject, leftCoverage bool) (string, string)`
  - `(*NotificationService).NotifyWatch(cfg NotificationConfig, w Watch, s watchSubject, allowSend bool, leftCoverage bool)`
  - `buildRecordMessage(category string, best recordBest, prevValue float64, hasPrev bool, routeFrom, routeTo string) (string, string)`

- [ ] **Step 1: Write the failing tests**

In `core/notifications_test.go`, replace the body of `TestBuildRecordMessage` (line 122) so it passes the two new arguments, and add the new tests below it:

```go
func TestBuildRecordMessage(t *testing.T) {
	best := recordBest{Registration: "N12345", Type: "B738", Flight: "SAS1", MetricValue: 45000}
	title, body := buildRecordMessage("highest", best, 44200, true, "", "")
	if title != "🏆 New all-time record: Highest" {
		t.Errorf("title = %q", title)
	}
	for _, want := range []string{"Altitude: 45000 ft", "Previous: 44200 ft", "Aircraft: N12345 (B738)", "Callsign: SAS1"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Route") {
		t.Errorf("body should omit the route when there is none:\n%s", body)
	}
}

func TestBuildRecordMessageIncludesTheRoute(t *testing.T) {
	best := recordBest{Registration: "B-2091", Type: "B77L", Flight: "CAO1008", MetricValue: 578}
	_, body := buildRecordMessage("fastest", best, 574, true, "PVG", "PIK")
	if !strings.Contains(body, "Route: PVG → PIK") {
		t.Errorf("body missing the route:\n%s", body)
	}
}

func TestBuildRecordMessageOmitsAHalfRoute(t *testing.T) {
	best := recordBest{Registration: "B-2091", Flight: "CAO1008", MetricValue: 578}
	_, body := buildRecordMessage("fastest", best, 574, true, "PVG", "")
	if strings.Contains(body, "Route") {
		t.Errorf("a route with only one end should not be printed:\n%s", body)
	}
}

func TestBuildWatchMessageMarksANotificationSentAfterTheAircraftLeft(t *testing.T) {
	_, body := buildWatchMessage("Above 40,000 ft", watchSubject{Hex: "78026e", Callsign: "CAO1160"}, true)
	if !strings.Contains(body, "Aircraft has left coverage") {
		t.Errorf("body should say the aircraft is gone:\n%s", body)
	}
}

func TestBuildWatchMessageOmitsTheMarkerWhileTheAircraftIsStillInRange(t *testing.T) {
	_, body := buildWatchMessage("Above 40,000 ft", watchSubject{Hex: "78026e", Callsign: "CAO1160"}, false)
	if strings.Contains(body, "left coverage") {
		t.Errorf("body should not claim the aircraft is gone:\n%s", body)
	}
}
```

Also update the three other existing `buildWatchMessage` calls to pass `false` — line 188 (`buildWatchMessage("Boeing close by", s, false)`), line 204 (`buildWatchMessage("Anything", watchSubject{Hex: "4ca7b5"}, false)`) and line 217 (`buildWatchMessage("New aircraft", watchSubject{Hex: "4ca7b5", FirstSeenEver: true}, false)`).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `~/.local/go/bin/go test ./core/ -run 'TestBuildRecordMessage|TestBuildWatchMessage'`
Expected: FAIL to build — too many arguments to `buildRecordMessage` and `buildWatchMessage`.

- [ ] **Step 3: Write the implementation**

In `core/notifications.go`, add the field to `NotificationConfig`:

```go
	CooldownMinutes int
	DelaySeconds    int // how long a watch notification waits for its enrichment
```

In `loadConfig`, add the read beside the cooldown:

```go
		CooldownMinutes: getIntSetting(n.pg, "notification_cooldown_minutes", 60),
		DelaySeconds:    getIntSetting(n.pg, "notification_delay_seconds", 30),
```

Change `buildRecordMessage`'s signature and add the route line after the callsign:

```go
// buildRecordMessage returns (title, body) for a new all-time record.
//
// routeFrom/routeTo are the record flight's route, or empty when the callsign
// has none stored. Both ends are required: half a route tells the reader less
// than no route at all.
func buildRecordMessage(category string, best recordBest, prevValue float64, hasPrev bool, routeFrom, routeTo string) (string, string) {
```

and, immediately after the `Callsign:` block and before the closing `return`:

```go
	if routeFrom != "" && routeTo != "" {
		fmt.Fprintf(&b, "Route: %s → %s\n", routeFrom, routeTo)
	}
```

In `NotifyRecord`, look the route up before building the message. Replace:

```go
	title, body := buildRecordMessage(category, best, prevValue, hasPrev)
```

with:

```go
	from, to := n.lookupRoute(strings.TrimSpace(best.Flight))
	title, body := buildRecordMessage(category, best, prevValue, hasPrev, from, to)
```

Change `buildWatchMessage`'s signature and append the marker as the last line of the body:

```go
func buildWatchMessage(watchName string, s watchSubject, leftCoverage bool) (string, string) {
```

and, after the `FirstSeenEver` block and before the closing `return`:

```go
	// The aircraft was already out of the snapshot when this notification was
	// released, so it is a report rather than an alert. Saying so beats letting
	// the reader run to the window for nothing.
	if leftCoverage {
		fmt.Fprintf(&b, "Aircraft has left coverage\n")
	}
```

Change `NotifyWatch`'s signature and pass the flag through:

```go
// cfg is loaded once per tick by evaluateWatches and passed in rather than read
// here, and allowSend is false when the per-tick cap has already been spent —
// the row is still written, only the push is dropped. leftCoverage marks a
// notification released after the aircraft had already gone.
func (n *NotificationService) NotifyWatch(cfg NotificationConfig, w Watch, s watchSubject, allowSend bool, leftCoverage bool) {

	if n.watchSends != nil {
		n.watchSends <- struct{}{}
		defer func() { <-n.watchSends }()
	}

	title, body := buildWatchMessage(w.Name, s, leftCoverage)
```

Finally, keep the build green by updating the one caller. In `core/watches-engine.go:390`:

```go
			go notifier.NotifyWatch(cfg, watchByID[key.WatchID], subjects[key.Hex], sendable[key], false)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `~/.local/go/bin/go test ./...`
Expected: PASS.

Run: `~/.local/go/bin/go vet ./...`
Expected: no output.

- [ ] **Step 5: Commit**

```bash
git add core/notifications.go core/notifications_test.go core/watches-engine.go
git commit -m "feat: give notifications a route line and a left-coverage marker"
```

---

### Task 3: Wire the queue into the watch engine

Replaces immediate notification with enqueue-then-release, and moves the per-tick push cap from match time to release time.

**Files:**
- Modify: `core/watches-engine.go` (the block from `// The notification config is read once per tick` at line ~370 to the end of the `for _, key := range confirmed` loop at line ~392)
- Test: `core/watches-engine_test.go`

**Interfaces:**
- Consumes: `newPendingWatchQueue()`, `(*pendingWatchQueue).enqueue/refresh/len`, `pendingWatchNotification`, `hasRoute` (Task 1); `NotificationConfig.DelaySeconds` and the five-argument `NotifyWatch` (Task 2); `planWatchSends(started []watchKey, names map[int]string, capacity int) (map[watchKey]bool, string)` and `watchNotifyCap = 50` (both unchanged, `core/watches-engine.go`).
- Produces: `releasableEntries(released []pendingWatchNotification, watchByID map[int]Watch) []pendingWatchNotification`.

- [ ] **Step 1: Write the failing test**

Append to `core/watches-engine_test.go`:

```go
func TestReleasableEntriesDropsNotificationsForVanishedWatches(t *testing.T) {
	watchByID := map[int]Watch{
		1: {ID: 1, Name: "Above 40,000 ft"},
	}
	released := []pendingWatchNotification{
		{Key: watchKey{WatchID: 1, Hex: "78026e"}},
		{Key: watchKey{WatchID: 9, Hex: "4ca7b5"}}, // deleted or disabled while waiting
		{Key: watchKey{WatchID: 1, Hex: "45d970"}},
	}

	got := releasableEntries(released, watchByID)

	if len(got) != 2 {
		t.Fatalf("kept %d entries, want 2", len(got))
	}
	for _, e := range got {
		if e.Key.WatchID != 1 {
			t.Errorf("kept an entry for watch %d, which no longer exists", e.Key.WatchID)
		}
	}
	if got[0].Key.Hex != "78026e" || got[1].Key.Hex != "45d970" {
		t.Errorf("order should be preserved, got %s then %s", got[0].Key.Hex, got[1].Key.Hex)
	}
}

func TestReleasableEntriesKeepsEverythingWhenEveryWatchStillExists(t *testing.T) {
	watchByID := map[int]Watch{1: {ID: 1, Name: "A"}, 2: {ID: 2, Name: "B"}}
	released := []pendingWatchNotification{
		{Key: watchKey{WatchID: 1, Hex: "aaaa"}},
		{Key: watchKey{WatchID: 2, Hex: "bbbb"}},
	}

	if got := releasableEntries(released, watchByID); len(got) != 2 {
		t.Fatalf("kept %d entries, want 2", len(got))
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `~/.local/go/bin/go test ./core/ -run TestReleasableEntries`
Expected: FAIL to build — `undefined: releasableEntries`.

- [ ] **Step 3: Write the implementation**

In `core/watches-engine.go`, add the package-level queue next to the existing `firstSeen` tracker declaration:

```go
// pendingWatchNotifications holds started matches whose notification is waiting
// for the callsign and route to arrive. Like evaluateWatches itself it is only
// ever touched from the single ingest-tick goroutine, so it needs no lock.
var pendingWatchNotifications = newPendingWatchQueue()
```

Add `releasableEntries` beside `planWatchSends`:

```go
// releasableEntries drops pending notifications whose watch disappeared while
// they waited. A deleted or disabled watch is no longer in watchByID, and its
// history row would fail the watch_id foreign key anyway — so the entry is
// discarded before it can spend any of the per-tick send cap.
func releasableEntries(released []pendingWatchNotification, watchByID map[int]Watch) []pendingWatchNotification {

	kept := make([]pendingWatchNotification, 0, len(released))
	for _, e := range released {
		if _, ok := watchByID[e.Key.WatchID]; !ok {
			log.Debug().Msgf("Dropping pending notification for watch %d / %s, which no longer exists", e.Key.WatchID, e.Key.Hex)
			continue
		}
		kept = append(kept, e)
	}
	return kept
}
```

Then replace everything from the `// The notification config is read once per tick` comment down to the end of the `for _, key := range confirmed` loop with:

```go
	// The notification config is read once per tick and handed to every send.
	// Loading it inside each NotifyWatch would be 15 QueryRow calls per started
	// match — 2400 of them on a pool of four connections when a broad watch
	// starts matching every aircraft in range at once. It is also needed when
	// nothing started but the queue still holds entries that may release now.
	var cfg NotificationConfig
	if notifier != nil && (len(confirmed) > 0 || pendingWatchNotifications.len() > 0) {
		cfg = notifier.loadConfig()
	}

	// A started match is queued, not notified. Its watch_active_matches row is
	// already written, so diffMatches can never report it again — from here the
	// queue is the only thing that will ever notify for it. A full queue fails
	// open: the entry is released on the spot rather than lost.
	delay := time.Duration(cfg.DelaySeconds) * time.Second
	var released []pendingWatchNotification
	for _, key := range confirmed {
		persisted[key] = true
		log.Info().Msgf("Watch %q matched %s", names[key.WatchID], key.Hex)

		if !pendingWatchNotifications.enqueue(key, subjects[key.Hex], now, delay) {
			log.Warn().Msgf("Pending notification queue is full, sending for %s without waiting", key.Hex)
			released = append(released, pendingWatchNotification{
				Key:      key,
				Subject:  subjects[key.Hex],
				QueuedAt: now,
			})
		}
	}

	released = append(released, pendingWatchNotifications.refresh(subjects, now)...)

	if notifier != nil && len(released) > 0 {
		sendableEntries := releasableEntries(released, watchByID)

		// The cap now bounds what is actually pushed on a tick rather than what
		// matched on it. A broad watch that starts matching 200 aircraft queues
		// all 200 and is trimmed here when they release — the same outcome as
		// before, moved to the moment it describes.
		keys := make([]watchKey, 0, len(sendableEntries))
		for _, e := range sendableEntries {
			keys = append(keys, e.Key)
		}
		sendable, capWarning := planWatchSends(keys, names, watchNotifyCap)
		if capWarning != "" {
			log.Warn().Msg(capWarning)
		}

		for _, e := range sendableEntries {
			log.Info().Msgf("Watch %q releasing notification for %s after %.1fs (callsign=%q route=%t gone=%t)",
				names[e.Key.WatchID], e.Key.Hex, now.Sub(e.QueuedAt).Seconds(),
				strings.TrimSpace(e.Subject.Callsign), hasRoute(e.Subject), e.LeftCoverage)

			go notifier.NotifyWatch(cfg, watchByID[e.Key.WatchID], e.Subject, sendable[e.Key], e.LeftCoverage)
		}
	}
```

`strings` and `time` are already imported in this file; `sort` too. Do not add imports it does not need.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `~/.local/go/bin/go test ./core/ -run TestReleasableEntries -v`
Expected: PASS.

Run: `~/.local/go/bin/go test ./...`
Expected: PASS.

Run: `~/.local/go/bin/go vet ./...`
Expected: no output.

Run: `~/.local/go/bin/go test -race ./core/`
Expected: PASS. The queue is single-goroutine by construction; this confirms nothing in the release path reaches into it from the `go notifier.NotifyWatch` goroutines.

- [ ] **Step 5: Commit**

```bash
git add core/watches-engine.go core/watches-engine_test.go
git commit -m "feat: hold watch notifications until their enrichment arrives"
```

---

### Task 4: The delay setting's surface

Exposes the delay in the settings UI and applies it to the interesting-aircraft query, which has the same problem in slower form: its 120-second tick can pick up an aircraft first seen twenty seconds earlier, before it has transmitted its identity.

Neither change is reachable by the Go test suite — the SQL needs a database and the input needs a browser. The build commands below are the gate.

**Files:**
- Modify: `core/stats-interesting.go` (`updateInterestingSeen` line 11, `unprocessedInteresting` line ~196)
- Modify: `web/src/components/Settings.svelte` (state line ~44, load line ~150, save line ~196, markup line ~433)

**Interfaces:**
- Consumes: `getIntSetting(pg *postgres, key string, def int) int` (`core/records-jobs.go:12`); the setting key `notification_delay_seconds` with default `30`, established in Task 2's `loadConfig`.
- Produces: nothing later tasks depend on.

- [ ] **Step 1: Add the age filter to the interesting query**

In `core/stats-interesting.go`, change `unprocessedInteresting` to take a minimum age and filter on it. The signature becomes:

```go
// unprocessedInteresting returns sightings old enough to have transmitted their
// identity. ADS-B broadcasts a callsign far less often than a position, so an
// aircraft picked up moments after first contact would be notified as a bare
// hex; leaving it for the next tick costs 120 seconds and buys an identity.
func unprocessedInteresting(pg *postgres, minAge time.Duration) []Aircraft {
```

and the query's WHERE clause becomes:

```go
	query := `
		SELECT id,
				hex,
				flight,
				r,
				t,
				alt_baro,
				alt_geom,
				gs,
				ias,
				tas,
				track,
				baro_rate,
				lat,
				lon,
				alert,
				db_flags,
				first_seen,
				first_seen_epoch
		FROM aircraft_data
		WHERE
			hex != '' AND
			interesting_processed = false AND
			first_seen < NOW() - make_interval(secs => $1)
		ORDER BY first_seen ASC`

	rows, err := pg.db.Query(context.Background(), query, minAge.Seconds())
```

Add `"time"` to the file's import block.

In `updateInterestingSeen`, read the setting and pass it in. Replace the first statement of the function:

```go
func updateInterestingSeen(pg *postgres) {

	// The same setting that defers watch notifications. A negative value would
	// pull first_seen into the future and select nothing, so it is clamped.
	delaySeconds := getIntSetting(pg, "notification_delay_seconds", 30)
	if delaySeconds < 0 {
		delaySeconds = 0
	}

	aircrafts := unprocessedInteresting(pg, time.Duration(delaySeconds)*time.Second)
```

- [ ] **Step 2: Verify the backend still builds and the suite is green**

Run: `~/.local/go/bin/go build ./...`
Expected: no output.

Run: `~/.local/go/bin/go test ./...`
Expected: PASS.

- [ ] **Step 3: Add the settings input**

In `web/src/components/Settings.svelte`, declare the state beside the cooldown (line ~44):

```javascript
    let notificationCooldownMinutes = 60;
    let notificationDelaySeconds = 30;
```

Load it where the other notification settings are read (after the `notification_cooldown_minutes` line, ~150):

```javascript
        if ($settings.notification_cooldown_minutes) notificationCooldownMinutes = parseInt($settings.notification_cooldown_minutes.setting_value);
        if ($settings.notification_delay_seconds) notificationDelaySeconds = parseInt($settings.notification_delay_seconds.setting_value);
```

Save it in `saveNotificationSettings`, immediately after the cooldown entry (~196). Note the bound is `>= 0`, not `>= 1`: zero is a meaningful value here, it switches deferral off.

```javascript
            notification_cooldown_minutes: (Number.isFinite(notificationCooldownMinutes) && notificationCooldownMinutes >= 1 ? Math.floor(notificationCooldownMinutes) : 60).toString(),
            notification_delay_seconds: (Number.isFinite(notificationDelaySeconds) && notificationDelaySeconds >= 0 ? Math.floor(notificationDelaySeconds) : 30).toString(),
```

Add the markup directly after the Cooldown `<div>` block (~433):

```svelte
                            <div>
                                <p class="text-xl font-extralight tracking-wider mb-2">Delay</p>
                                <p class="text-m text-base-content/70 mb-2">Seconds to hold a notification while the aircraft's callsign and route arrive. 0 sends immediately.</p>
                                <input type="number" bind:value={notificationDelaySeconds} on:input={handleNotificationChange} min="0" step="1" class="input w-20" />
                            </div>
```

- [ ] **Step 4: Verify the frontend builds**

Run: `cd web && npm run build`
Expected: a successful Vite build writing to `dist/`, with no errors. Return to the repository root afterwards.

- [ ] **Step 5: Commit**

```bash
git add core/stats-interesting.go web/src/components/Settings.svelte
git commit -m "feat: make the notification delay configurable and apply it to interesting sightings"
```

---

## Verification after the plan is complete

The Go suite covers the queue's release rules, the message builders and the
removed-watch filter. Three things it cannot reach, to be checked by running the
stack against the live receiver:

1. **The deferral itself.** `docker logs skystats | grep 'releasing notification'` — the release line reports the actual wait, the callsign and whether a route was found. A hit on an aircraft entering range should show a non-zero wait and a non-empty callsign, which is exactly what the B-6070 notification lacked.
2. **The interesting age filter.** `SELECT count(*) FROM aircraft_data WHERE interesting_processed = false AND first_seen > NOW() - interval '30 seconds'` should be non-zero at times, showing young sessions are being held rather than processed.
3. **The settings input.** Save a delay in the UI, then confirm `SELECT setting_value FROM user_settings WHERE setting_key = 'notification_delay_seconds'` matches, and that setting it to `0` restores immediate sending.
