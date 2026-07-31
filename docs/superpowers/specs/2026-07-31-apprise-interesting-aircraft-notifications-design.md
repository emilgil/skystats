# Apprise notifications for interesting aircraft — Design

**Date:** 2026-07-31
**Status:** Approved for planning

## Goal

Send a push notification (via a self-hosted [Apprise API](https://github.com/caronc/apprise-api)
server) whenever a new "interesting" aircraft is identified, and add a **Notifications**
tab under Settings to configure the Apprise connection, choose which categories notify,
and set an anti-spam cooldown.

## Key decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Apprise integration | POST to an **external Apprise API server** the user already runs | Keeps Skystats pure Go, no Python/image bloat. Postgres is already a separate container, so multi-container deployment is the norm. |
| Apprise config mode | **Stateful** — Skystats stores only a config *key*, POSTs to `/notify/{key}` | No external-service secrets/tokens ever live in Skystats. All notification targets are managed on the Apprise side. |
| Trigger | New row inserted into `interesting_aircraft_seen` | This is exactly the moment an interesting aircraft is identified. |
| Anti-spam | **Cooldown per aircraft (ICAO)**, configurable, default **60 min** | Avoids spam from local regulars. |
| Category granularity | The 4-way `group` classification: `Mil` / `Gov` / `Pol` / `Civ` | Matches the existing "Interesting Aircraft" tabs and the `interesting_aircraft_seen.group` column. |

## Data flow

```
updateInterestingSeen() (every 120s)
  └─ inserts new rows into interesting_aircraft_seen
       └─ collect the newly-inserted aircraft
            └─ go NotificationService.NotifyBatch(cfg, aircraft)   (background goroutine)
                 for each aircraft:
                   1. notifications_enabled == true?
                   2. group is an enabled category?
                   3. cooldown: no *successful* notification_log row for this ICAO
                      newer than cooldown window?
                   4. build message + POST http://<apprise>/notify/<key>  (5s timeout)
                   5. write result to notification_log
```

Notification dispatch runs in a **background goroutine** so a slow/unreachable Apprise
server never stalls the main ticker loop.

## Backend

### `core/notifications.go` (new)

- `NotificationConfig` struct — loaded once per `updateInterestingSeen()` invocation via
  `SettingsService` (avoids per-aircraft settings queries): enabled flag, api URL, config
  key, per-group enabled flags, cooldown minutes.
- `NotificationService.NotifyBatch(cfg, []InterestingAircraft)` — iterates and applies the
  gate logic above.
- `NotificationService.SendTest(apiURL, key)` — sends a fixed test message; used by the
  test endpoint. Accepts explicit URL/key so the UI can test *before* saving.
- Message build: see [Message format](#message-format).
- HTTP: `net/http` client with a **5s timeout**. `POST {apiURL}/notify/{key}`,
  `Content-Type: application/json`, body `{ "body", "title", "type": "info",
  "format": "markdown", "attach"? }`.

### Cooldown & logging — `notification_log` table (migration `000014`)

```
notification_log (
  id          SERIAL PRIMARY KEY,
  icao        TEXT NOT NULL,
  "group"     TEXT,
  title       TEXT,
  body        TEXT,
  target      TEXT,          -- config key used
  status      TEXT NOT NULL, -- 'sent' | 'failed'
  http_status INTEGER,
  error       TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
)
-- index on (icao, created_at)
```

Cooldown check: skip if a row exists with `icao = $1 AND status = 'sent' AND created_at >
NOW() - (cooldown minutes)`. Only **successful** sends count toward cooldown, so a failed
send does not suppress the next attempt. The table doubles as a debug/audit log.

Restart behaviour: the in-DB log means cooldown survives daemon restarts. After a restart
only genuinely new sightings (unprocessed rows) are considered, so no re-notification storm.

### Data availability at notify time

| Field | Source | Notes |
|-------|--------|-------|
| Altitude | `alt_baro` (ft) | present on the sighting |
| Speed | `gs` (kt, ground speed) | present on the sighting |
| Distance | computed from `lat`/`lon` via the existing cheap-ruler (`getDistance`), km | present on the sighting |
| Route (to/from) | lookup `route_data` by `route_callsign = flight` | **often absent at first sighting** (routes enriched every 300s); include the line only when present |
| Image | `image_link_1` | optional; used as `attach` |

## API — `core/api.go`

- New route: `POST /api/notifications/test`.
  - Optional JSON body `{ "apprise_api_url", "apprise_config_key" }`; if omitted, falls
    back to saved settings. This lets the UI test before saving.
  - Returns `200 { "status": "sent" }` or an error status with a human-readable message.
- Settings themselves flow through the existing `GET/PUT /api/settings` (no new settings
  endpoint needed).

## Frontend — `web/src/components/Settings.svelte`

New menu item **"Notifications"** (between "Cards" and "About"):

- **Enable notifications** — master on/off toggle.
- **Apprise API URL** — text input, e.g. `http://192.168.1.x:8000`. Required when enabled.
- **Config key** — text input, e.g. `skystats`. Required when enabled.
- **Categories** — four checkboxes: Military / Government / Police / Civilian.
- **Cooldown (minutes)** — number input, default 60, min 1.
- **Send test notification** — button → `POST /api/notifications/test` with the current
  form values; shows success/failure inline.
- **Save** — reuses the existing `settings.save` flow.

## Message format

Markdown (`format: "markdown"`).

- **Title:** `✈️ {CategoryName}: {registration}` — CategoryName maps `Mil→Military`,
  `Gov→Government`, `Pol→Police`, `Civ→Civilian`. Falls back to `flight`, then `hex`, when
  registration is empty.
- **Body** (lines omitted when the value is empty/unknown):
  - `Type: {type name}`
  - `Operator: {operator}`
  - `Callsign: {flight}`
  - `Altitude: {alt_baro} ft`
  - `Speed: {gs} kt`
  - `Distance: {distance} km`
  - `Route: {origin_iata} → {destination_iata}` *(only when route is known)*
  - `Link: {link}`
- **Attachment:** `attach = image_link_1` when present.

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
| `notification_cooldown_minutes` | `60` | Per-ICAO cooldown between notifications |

## Out of scope (YAGNI)

- Stateless Apprise mode (storing raw service URLs in Skystats).
- Bundling the Apprise CLI into the Skystats image.
- A notification history UI (the `notification_log` table exists for cooldown/debug only).
- Per-category message templates or custom message editing.

## Migration summary

`000014_add_notifications`:
- `up`: create `notification_log` + index; `INSERT` the 8 default settings above.
- `down`: drop `notification_log`; `DELETE` the 8 settings keys.
