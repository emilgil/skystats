# Notification Deferral Design

**Date:** 2026-08-06
**Status:** Approved

## Problem

Watch notifications fire on the 2-second ingest tick, in the same instant the
condition first matches. At that instant readsb has usually delivered position,
altitude and speed but not yet the callsign: ADS-B broadcasts identity far less
often than position. Without a callsign there is no key to look a route up
with, so the notification carries neither identity nor route.

Observed on 2026-08-05, B-6070 (hex `78026e`, later CAO1160):

```
23:11:37.593  Watch "Above 40,000 ft" matched 78026e   ← notification sent
23:11:59.968  fetchRoutesOnSight() ... matched=1        ← route arrived, 22.4 s later
```

The stored snapshot for that hit:

```json
{"callsign": "", "registration": "B-6070", "altitude_ft": 41000,
 "origin": null, "destination": null, "distance_km": 29.4}
```

The route pipeline was not at fault. It had the answer (PIK→CTU) within seconds
of the callsign arriving; it simply had nothing to work with for the first 21
seconds.

Measured across the whole history on the production instance: **40 watch hits,
13 without a callsign (33%), 22 without a route (55%).**

Two smaller gaps found while investigating:

- Interesting-aircraft notifications run on the 120-second ticker and read the
  callsign from `aircraft_data`, so they normally have time. All three in the
  history had a callsign; the two without a route were ambulance flights
  (SWE72L, SWE34A) whose callsigns have no `route_data` row at all and never
  will. There is no measured instance of the problem here. The structural risk
  remains: an aircraft first seen in the last ~20 seconds before a tick has not
  had time to transmit its identity.
- `buildRecordMessage` never writes a route line at all. The CAO1008 all-time
  speed record notification could have said `Route: PVG → PIK` and did not.

## Goal

A notification should not go out until the enrichment it wants has had a fair,
bounded chance to arrive — without ever losing a hit, and without a notification
becoming a silent no-op.

## Release conditions

A pending watch notification is released when **any** of three conditions holds:

| # | Condition | Result |
|---|---|---|
| 1 | callsign **and** route present | complete notification |
| 2 | callsign present, route grace (6 s) expired | notification without route |
| 3 | overall delay expired **and** callsign still missing | notification without identity, as today |

Condition 3 applies only while the callsign is absent. If the callsign arrives
at second 29, condition 2 governs and the release waits until second 35, so the
**maximum total wait is delay + 6 s**.

The route grace is a hard-coded 3 ticks (6 s). On-sight route lookup normally
answers in 1-2 s, so this is ample margin even for a slow adsb.im response. It
is not configurable: it exists to cover network latency, not user preference.

`RouteDeadline` is set the moment a callsign is first observed for an entry —
including at enqueue, when the aircraft was already broadcasting its identity
when the match started. Such an entry releases on the same tick if its route is
already stored, and otherwise waits at most the 6-second grace.

**An aircraft leaving coverage does not release the notification early.** Two
reasons: readsb intermittently drops an aircraft for a single tick, and an
on-sight request already in flight can still come back with the route. The
entry waits out its deadline. If the hex is absent from the current snapshot at
release time, the message gains the line `Aircraft has left coverage`.

Notification bodies are English throughout (`Callsign:`, `Altitude:`, `First
time ever seen`), so the marker is English too.

## Architecture

### The pending queue

New file `core/watches-pending.go`. `watches-engine.go` is already ~420 lines,
and the queue is a self-contained, testable unit.

```go
type pendingWatchNotification struct {
    Key           watchKey     // watch id + hex
    Subject       watchSubject // most recent known subject, refreshed each tick
    Deadline      time.Time    // matchedAt + configured delay
    RouteDeadline time.Time    // zero until a callsign is first observed, then +6 s
}
```

The queue is pure logic. `now` is passed in; it holds no clock and touches no
database. That is what makes the release conditions table-testable.

**Safety valve:** above 500 pending entries, new matches are released
immediately rather than queued. The queue must never be able to grow without
bound, and it fails open — never by dropping notifications.

### Per-tick flow

```
evaluateWatches
  ├─ build subjects                    (unchanged)
  ├─ diffMatches → started / ended     (unchanged)
  ├─ persistStartedMatches → confirmed (unchanged; the match row is written at once)
  ├─ NEW: pending.enqueue(confirmed, subjects, now, delay)
  ├─ NEW: released := pending.refresh(subjects, now)
  ├─ planWatchSends(released, names, watchNotifyCap)   ← the cap moves here
  └─ go NotifyWatch(cfg, watch, subject, sendable, leftCoverage)
```

Enqueue runs before refresh within the same tick, so a match can be enqueued and
released on the tick it started. That is how `delay == 0` restores today's
behaviour without a special case in the release logic: enqueue sets both
`Deadline` and `RouteDeadline` to `now`, and refresh releases the entry
immediately on the same tick.

`refresh` updates each entry's subject from the current snapshot when the hex is
present, leaves it untouched when it is not, and returns the entries whose
release conditions are now met.

The 50-pushes-per-tick cap now applies to what is **actually sent**, not to what
matched. A broad watch that starts matching 200 aircraft queues all 200 and is
trimmed to 50 when they are released — the same outcome as today, shifted in
time. `planWatchSends` itself is unchanged, including its round-robin fairness
across watches.

### Invariants preserved

- The `watch_active_matches` row is still written at match time, so
  `diffMatches` can never report the same match again and a duplicate
  notification is impossible.
- The `watch_notifications` history row is written at release, not at match.
  A daemon restart inside the wait window therefore loses the hit — silently,
  and at most one delay-window's worth, and only on deploy. This is an accepted
  trade for keeping the queue in memory.
- If the watch is deleted or disabled during the wait, the entry is dropped at
  release: it is no longer in `watchByID`, and its history row would fail the
  `watch_id` foreign key anyway.

## The setting

Key `notification_delay_seconds`, default `30`, `0` disables deferral entirely
and restores today's immediate-send behaviour. The name is deliberately not
`watch_`-prefixed: the same value governs the interesting-aircraft guard.

It is read in `loadConfig()`, which `evaluateWatches` already calls once per
tick and only when there is something to send. No extra query enters the hot
path.

`NotificationConfig` gains `DelaySeconds int`.

In `web/src/components/Settings.svelte` it becomes a number input beside
Cooldown, following that field's existing pattern: a local variable initialised
from `$settings`, `min="0"`, `step="1"`, and a fallback to 30 when the value is
not a finite number ≥ 0.

## Interesting-aircraft guard

`unprocessedInteresting` gains an age filter:

```sql
AND first_seen < NOW() - make_interval(secs => $1)
```

Sessions younger than the delay are left untouched and picked up on the next
120-second tick. At `0` the clause is inert. The delay is read once per tick
with `getIntSetting`, which at 120-second cadence costs nothing.

Cost: an interesting notification that would have gone out without a callsign
instead arrives up to 120 s later, with one.

## Record route line

`NotifyRecord` looks the route up on `best.Flight` with the existing
`lookupRoute`, and `buildRecordMessage` writes a `Route: X → Y` line after
`Callsign:` when both ends are present. Records are computed on the 120-second
tick, so the route is normally already stored.

## Error handling

- **Empty enrichment map.** `enrichAircraftSnapshot` returns an empty map on
  query failure. Any subject built from it has no route, which under condition
  2 means every pending entry waits its full 6-second route grace and then
  releases without a route. Degraded, never stuck, never a burst — but the
  queue must not treat "no enrichment" as a reason to keep waiting past the
  deadline.
- **Watch removed mid-wait.** Entry dropped at release, logged at debug.
- **Queue overflow.** Above 500 entries, release immediately.
- **Restart mid-wait.** Hit lost, as described above. Accepted.

## Testing

Pure functions, no new fixtures:

- `core/watches-pending_test.go` — table-driven over (callsign present?, route
  present?, elapsed time, hex in snapshot?) → expected release decision and
  `leftCoverage` flag; that an entry's subject is refreshed from a later tick;
  that the 500-entry valve releases immediately.
- `core/notifications_test.go` — `buildWatchMessage` with and without
  `leftCoverage`; `buildRecordMessage` with and without a route.

Outside test coverage, verified by running the stack: the SQL change in
`unprocessedInteresting` and the settings input.

## Files

| | |
|---|---|
| Create | `core/watches-pending.go`, `core/watches-pending_test.go` |
| Modify | `core/watches-engine.go`, `core/notifications.go`, `core/stats-interesting.go`, `core/notifications_test.go`, `web/src/components/Settings.svelte` |
| Migration | none — `user_settings` is key/value; the database stays at v19 |

## Out of scope

- Persisting the pending queue so it survives a restart.
- Any change to how routes are fetched, or to the multi-leg filter.
- Suppressing notifications for aircraft that have already left coverage. They
  are sent, marked.
