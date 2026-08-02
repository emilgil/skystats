# Design: readsb registration fallback in the aircraft detail modal

**Date:** 2026-08-02
**Status:** Approved
**Scope:** Backend only — `core/aircraft-detail.go`

## Problem

The aircraft detail modal shows external links (Flightradar24, JetPhotos) only
when a registration is known, because those services are keyed on registration.
The modal endpoint `GET /api/stats/aircraft/:hex` derives `registration`
**exclusively** from the `registration_data` table, which is enriched from the
adsbdb API. adsbdb has narrow coverage: for the Wizz Air example `4d201f`
(9H-WNR) it returns "unknown aircraft", so `registration` is `null` and the
FR24/JetPhotos buttons hide — even though we already know the registration.

We already ingest a registration for essentially every aircraft we see: the
readsb `aircraft.json` feed carries an `r` field (registration, from readsb's
bundled database), which the daemon writes to `aircraft_data.r` (and
`flight_history.registration`). The modal endpoint simply never reads it.

Measured on the live 251 instance (2026-08-02):

| Metric | Count |
|---|---|
| Aircraft seen (`aircraft_data` rows) | 1744 |
| …with a readsb `r` | 1724 (98.9%) |
| …with an adsbdb registration | 579 (33%) |
| Have `r` but **no** adsbdb registration (links hidden today) | 394 |
| Target `4d201f` | `r = 9H-WNR` |

## Goal

When adsbdb has no registration for an aircraft, fall back to the readsb `r`
value already stored in `aircraft_data`, so the modal shows the registration
and the registration-keyed external links appear.

## Non-goals

- No new external API call, no Planespotters scraping. The value is already in
  our database.
- No write to `registration_data`. The fallback is computed at read time in the
  modal endpoint only; adsbdb remains the source of the richer metadata
  (owner, manufacturer, icao_type, photo).
- No database migration — the `aircraft_data.r` column exists and is populated.
- No frontend change — the modal already renders `data.registration` and builds
  the links from it.

## Design

`getAircraftDetail` in `core/aircraft-detail.go` already runs, as its step 2, a
query against `aircraft_data` for the newest row of this hex (to populate live
status). That query is extended to also select `ad.r`, scanned into a new
`*string`. After the query:

- If `resp["registration"]` is still absent (adsbdb step 1 set nothing) and the
  readsb `r` value is non-empty, set `resp["registration"]` to the trimmed `r`.

Resulting precedence for `registration`:

1. `registration_data.registration` (adsbdb) — richest identity, kept first.
2. `aircraft_data.r` (readsb) — broad coverage fallback.
3. otherwise `null`.

This mirrors an existing pattern in the codebase: `current-sightings.go:76`
already prefers the readsb `r` via `firstNonEmpty(a.R, e.Registration)`.

### Data quality

readsb `r` values in the live DB are clean registrations — a scan for
short/3-letter junk (`length(r) < 4 OR r ~ '^[A-Z]{3}$'`) returned zero rows,
and the ingest path already drops non-aircraft (`isNonAircraft` filters
`R == "TWR"`). No extra sanitization beyond trimming whitespace is needed.

### Edge cases

- adsbdb registration present → unchanged; readsb fallback not consulted.
- No `aircraft_data` row for the hex (`pgx.ErrNoRows` in step 2) → no `r`
  available; `registration` stays whatever step 1 produced (possibly null).
  Acceptable — the links stay hidden, as today.
- `registration_data.registration` is a non-nil empty string → treated as
  absent, so the readsb fallback applies.

## Testing / verification

No automated tests in this repo. Verify manually:

1. `cd core && go build ./... && go vet ./...` — compiles clean.
2. Deploy to 251, then `curl /api/stats/aircraft/4d201f` → expect
   `"registration":"9H-WNR"`.
3. `curl /api/stats/aircraft/448466` (OO-ACF, adsbdb-covered) → registration
   unchanged (adsbdb value retained, no regression).
4. Browser: open the Wizz aircraft's modal → Flightradar24 and JetPhotos
   buttons now appear and resolve; header shows `9H-WNR` instead of the hex.
