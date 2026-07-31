# Apprise notifications for interesting aircraft & all-time records — Design

**Date:** 2026-07-31
**Status:** Approved for planning

## Goal

Send a push notification (via a self-hosted [Apprise API](https://github.com/caronc/apprise-api)
server) when:

1. a new **"interesting" aircraft** is identified (military / government / police / civilian), or
2. a new **all-time record** is set (highest, fastest, furthest flown, etc.).

Add a **Notifications** tab under Settings to configure the Apprise connection, choose which
interesting categories and which record types notify, and set an anti-spam cooldown.

## Key decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Apprise integration | POST to an **external Apprise API server** the user already runs | Keeps Skystats pure Go, no Python/image bloat. Postgres is already a separate container. |
| Apprise config mode | **Stateful** — Skystats stores only a config *key*, POSTs to `/notify/{key}` | No external-service secrets/tokens ever live in Skystats. Targets managed on the Apprise side. |
| Interesting trigger | New row in `interesting_aircraft_seen` | The moment an interesting aircraft is identified. |
| Interesting anti-spam | **Cooldown per aircraft (ICAO)**, configurable, default **60 min** | Avoids spam from local regulars. |
| Record trigger | Inside `writeRecords`: the `all_time` bucket's best value is **strictly beaten** | `writeRecords` is the single choke point for all 7 categories. |
| Record scope | **`all_time` only** — windowed periods (24h/7d/30d/90d/365d) never notify | User wants "…ever" records only. |
| Record anti-spam | **Dedupe per flight-session** = (category + hex + first_seen) | One aircraft progressively raising a record in one pass = one notification; a different flight (or a new session) that later beats it notifies again. No time cooldown. |
| Category granularity (interesting) | The 4-way `group`: `Mil` / `Gov` / `Pol` / `Civ` | Matches the "Interesting Aircraft" tabs and `interesting_aircraft_seen.group`. |
| Record categories | The 7 in `recordCategories`: fastest, slowest, highest, lowest, furthest_flown, longest_route, most_remaining | The single source of truth in `records-meta.go`. |

## Data flow

```
── Interesting ──────────────────────────────────────────────
updateInterestingSeen()  (every 120s)
  └─ inserts new rows into interesting_aircraft_seen
       └─ go notifier.NotifyBatch(newly-inserted aircraft)   (background goroutine)
            per aircraft: enabled? · group is an enabled category?
                          · ICAO cooldown clear? → POST → log

── All-time records ─────────────────────────────────────────
writeRecords(category, candidates)  (motion every 120s, distance every 300s)
  ├─ oldBest = best all_time row for category   (synchronous, before write)
  ├─ …existing insert + trim…
  ├─ newBest = best all_time row for category   (synchronous, after write)
  └─ if oldBest existed AND newBest strictly beats oldBest:
        go notifier.NotifyRecord(category, newBest, oldBest.value)   (background goroutine)
             enabled? · record category enabled?
             · not already sent for (category, hex, first_seen)? → POST → log
```

All Apprise POSTs run in **background goroutines** so a slow/unreachable server never stalls
the main ticker loop. Each POST uses a **5s timeout**.

Baseline behaviour: if the `all_time` bucket was **empty** before the write (fresh install),
`oldBest` does not exist → no notification (silent baseline seed), avoiding a startup burst of
7 record notifications. On the existing deployment the buckets are already populated, so the
first genuine beat after deploy notifies as intended.

## Backend

### Shared notifier

A package-level `notifier *NotificationService` is created in `main()` (after the Postgres
connect, before the tickers start) and used by both `updateInterestingSeen()` and
`writeRecords()` — both are free functions in `package main`. Calls are guarded with
`if notifier != nil`.

### `core/notifications.go` (new)

- `NotificationConfig` — loaded via `SettingsService`: master enabled flag, api URL, config
  key, the 4 interesting-group flags, the 7 record flags, cooldown minutes.
- `NotifyBatch([]InterestingAircraft)` — loads config once, applies the interesting gate per
  aircraft (enabled → group enabled → ICAO cooldown clear), sends, logs.
- `NotifyRecord(category string, best recordBest, prevValue float64)` — loads config, applies
  the record gate (enabled → record category enabled → session not already notified), sends,
  logs.
- `SendTest(apiURL, key)` — sends a fixed test message; used by the test endpoint. Takes
  explicit URL/key so the UI can test *before* saving.
- HTTP: `net/http` client, **5s timeout**, `POST {apiURL}/notify/{key}`,
  `Content-Type: application/json`, body `{ "body", "title", "type": "info",
  "format": "markdown", "attach"? }`.

### Record detection — `core/records-ingest.go`

`writeRecords` gains a small helper `allTimeBest(pg, category) (recordBest, bool)` returning the
best `all_time` row (`ORDER BY metric_value <bestFirst>, first_seen ASC LIMIT 1`) with
metric_value, hex, flight, registration, type, first_seen. Detection wraps the existing write
(read old best → write → read new best → compare with `meta.KeepMax` direction). "Strictly
beats": `KeepMax ? new > old : new < old`.

### Cooldown / dedupe / logging — `notification_log` (migration `000014`)

```
notification_log (
  id           SERIAL PRIMARY KEY,
  kind         TEXT NOT NULL,        -- 'interesting' | 'record'
  category     TEXT,                 -- interesting: Mil/Gov/Pol/Civ ; record: highest/fastest/…
  icao         TEXT,                 -- aircraft hex
  first_seen   TIMESTAMPTZ,          -- record dedupe: identifies the flight-session
  metric_value DOUBLE PRECISION,     -- record: the value that set the record
  title        TEXT,
  body         TEXT,
  target       TEXT,                 -- config key used
  status       TEXT NOT NULL,        -- 'sent' | 'failed'
  http_status  INTEGER,
  error        TEXT,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
)
-- index (icao, created_at); index (kind, category, icao, first_seen)
```

- **Interesting cooldown**: skip if a row exists with `kind='interesting' AND icao=$1 AND
  status='sent' AND created_at > NOW() - (cooldown minutes)`. Only successful sends count, so a
  failed send doesn't suppress the next attempt.
- **Record dedupe**: skip if a row exists with `kind='record' AND category=$1 AND icao=$2 AND
  first_seen=$3 AND status='sent'`.
- Every attempt (sent/failed) is logged; the table doubles as a debug/audit log. Cooldown &
  dedupe survive daemon restarts because they read the DB.

### Data availability at notify time (interesting)

| Field | Source | Notes |
|-------|--------|-------|
| Altitude | `alt_baro` (ft) | on the sighting |
| Speed | `gs` (kt, ground speed) | on the sighting |
| Distance | computed from `lat`/`lon` via cheap-ruler (`getDistance`), km | on the sighting |
| Route (to/from) | lookup `route_data` by `route_callsign = flight` | **often absent at first sighting**; include only when present |
| Image | `image_link_1` | optional; used as `attach` |

## API — `core/api.go`

- New route `POST /api/notifications/test`.
  - Optional JSON body `{ "apprise_api_url", "apprise_config_key" }`; falls back to saved
    settings when omitted → lets the UI test before saving.
  - Returns `200 { "status": "sent" }` or an error status with a readable message.
- Settings flow through the existing `GET/PUT /api/settings` (no new settings endpoint).

## Frontend — `web/src/components/Settings.svelte`

New menu item **"Notifications"** (between "Cards" and "About"):

- **Enable notifications** — master on/off toggle (gates everything).
- **Apprise API URL** — e.g. `http://192.168.1.x:8000`. Required when enabled.
- **Config key** — e.g. `skystats`. Required when enabled.
- **Interesting categories** — four checkboxes: Military / Government / Police / Civilian.
- **All-time records** — seven checkboxes: Fastest / Slowest / Highest / Lowest /
  Furthest flown / Longest route / Most remaining.
- **Cooldown (minutes)** — number input for interesting aircraft, default 60, min 1.
- **Send test notification** — button → `POST /api/notifications/test` with the current form
  values; shows success/failure inline.
- **Save** — reuses the existing `settings.save` flow.

## Message format (markdown)

### Interesting aircraft
- **Title:** `✈️ {CategoryName}: {registration}` — `Mil→Military`, `Gov→Government`,
  `Pol→Police`, `Civ→Civilian`; falls back to `flight`, then `hex`.
- **Body** (a line is omitted when its value is empty/unknown):
  - `Type: {type name}`
  - `Operator: {operator}`
  - `Callsign: {flight}`
  - `Altitude: {alt_baro} ft`
  - `Speed: {gs} kt`
  - `Distance: {distance} km`
  - `Route: {origin_iata} → {destination_iata}` *(only when route is known)*
  - `Link: {link}`
- **Attachment:** `attach = image_link_1` when present.

### All-time record
- **Title:** `🏆 New all-time record: {RecordName}` — Fastest / Slowest / Highest / Lowest /
  Furthest flown / Longest route / Most remaining.
- **Body:**
  - `{Metric}: {value} {unit}` — altitude ft, speed kt, distances km.
  - `Previous: {prevValue} {unit}` *(when a previous best existed)*
  - `Aircraft: {registration} ({type})`
  - `Callsign: {flight}`
- No attachment/link (records carry no plane-alert image or link).

## Settings keys (seeded in migration `000014`, into `user_settings`)

| key | default | description |
|-----|---------|-------------|
| `notifications_enabled` | `false` | Master on/off for Apprise notifications |
| `apprise_api_url` | `''` | Base URL of the Apprise API server |
| `apprise_config_key` | `''` | Saved config key on the Apprise server |
| `notify_group_mil` | `true` | Notify for military aircraft |
| `notify_group_gov` | `true` | Notify for government aircraft |
| `notify_group_pol` | `true` | Notify for police aircraft |
| `notify_group_civ` | `true` | Notify for civilian (interesting) aircraft |
| `notification_cooldown_minutes` | `60` | Per-ICAO cooldown between interesting notifications |
| `notify_record_fastest` | `true` | Notify on new all-time fastest |
| `notify_record_slowest` | `true` | Notify on new all-time slowest |
| `notify_record_highest` | `true` | Notify on new all-time highest |
| `notify_record_lowest` | `true` | Notify on new all-time lowest |
| `notify_record_furthest_flown` | `true` | Notify on new all-time furthest flown |
| `notify_record_longest_route` | `true` | Notify on new all-time longest route |
| `notify_record_most_remaining` | `true` | Notify on new all-time most remaining |

## Out of scope (YAGNI)

- Stateless Apprise mode (raw service URLs stored in Skystats).
- Bundling the Apprise CLI into the Skystats image.
- Notifications for windowed-period records (24h/7d/…); only `all_time`.
- A notification history UI (`notification_log` is for cooldown/dedupe/debug only).
- Per-category message templates or custom message editing.

## Migration summary

`000014_add_notifications`:
- `up`: create `notification_log` + its two indexes; `INSERT` the 15 default settings above.
- `down`: drop `notification_log`; `DELETE` the 15 settings keys.
