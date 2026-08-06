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
