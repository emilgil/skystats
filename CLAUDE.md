# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

Skystats retrieves, stores, and displays aircraft ADS-B data received via an SDR. It is a Go daemon + PostgreSQL database + Svelte frontend, normally deployed as a single Docker image.

## Commands

Run the Go tests with `go test ./...` from the repository root. Coverage is limited to the pure Go logic — matching, diffing, formatting, aggregation — which is deliberately kept free of I/O so it can be tested without fixtures. The database, HTTP handler and Svelte layers have no test harness, so changes there have to be verified by running the stack.

### Backend (Go, in `core/`)

```bash
cd core && go build -o skystats-daemon   # build
./skystats-daemon                        # run (daemonizes itself outside Docker)
kill $(cat core/skystats.pid)            # stop the daemon
```

- Requires a running PostgreSQL and a populated `.env` in the repo root (copy from `.env.example`). The binary loads `../.env` relative to its working directory.
- Outside Docker (`DOCKER_ENV` unset) the process forks into a daemon: logs go to `skystats.log` and the PID to `skystats.pid` next to the binary — not to the terminal.
- `scripts/build` automates kill/rebuild/restart but has `$HOME/dev/skystats` hardcoded as the repo path.

### Frontend (Svelte, in `web/`)

```bash
cd web && npm install
npm run dev -- --host    # dev server on :5173, proxies /api to localhost:8080
npm run build            # production build to dist/
```

### Docker

`docker compose up -d` with `example.compose.yml` (copied to `compose.yml`) runs the full stack. The `Dockerfile` is a multi-stage build (Go binary + Vite build) on the sdr-enthusiasts s6-overlay base image; service startup scripts live in `rootfs/`. Release binaries are built with goreleaser (`.goreleaser.yaml`).

## Architecture

### Data flow

readsb `aircraft.json` (polled over HTTP) → Go daemon → PostgreSQL → Gin API (`:8080`, `/api/...`) → Svelte frontend (`:5173`).

### Go daemon (`core/`, single `main` package)

`core.go` is the entry point. After connecting to Postgres, running migrations, and syncing plane-alert-db reference data, it starts the API server in a goroutine and then loops forever on tickers:

- **2s** — fetch `READSB_AIRCRAFT_JSON` and upsert aircraft positions (`aircraft.go`, `readsb.go`). Only aircraft within `RADIUS` km of `LAT`/`LON` are recorded (distance via cheap-ruler). This hot path uses an in-memory recently-seen cache (`recentAircraftCache` on the `postgres` struct, 10-minute sliding expiry) so unchanged aircraft skip the DB lookup. The same tick fetches Postgres enrichment once (`enrichAircraftSnapshot`) and feeds it to both Current Sightings and the watch engine (`watches-engine.go`), which matches every aircraft against the user's watches and fires one Apprise notification per sighting that starts matching.
- **30s** — enrich registrations from the external adsbdb API (`registrations.go`)
- **120s** — recompute measurement statistics (`stats-motion.go`) and "interesting seen" (`stats-interesting.go`)
- **300s** — enrich routes from adsbdb (`routes.go`)

"Interesting" aircraft (military/government/police/civilian) are matched against a local copy of plane-alert-db, downloaded and upserted at startup (`db-plane-alert-data.go`); a custom CSV can be supplied via `PLANE_DB_URL`.

### API (`api.go`)

Gin server exposing read-only stats under `/api/stats/...` (above, seen, routes, motion, interesting, types, charts) plus settings endpoints. Two supporting services:

- `CachedStatsService` (`cached-stats.go`) — expensive aggregate counts stored in the `cached_stats` table with a 24h TTL, recalculated lazily on read.
- `SettingsService` (`settings.go`) — key/value user settings in the `user_settings` table.

Endpoints accept a `tz` query param for timezone-aware stats. Gin runs in debug mode when `LOG_LEVEL` is `DEBUG`/`TRACE`, release mode otherwise. The same Gin server also serves the built frontend as static files, which is how the Docker image serves both API and UI from one process.

### Watches

User-defined rules (`watches` + `watch_conditions`) are matched against the live snapshot every 2s. `watches-match.go` holds the field registry, the `watchSubject` model and the pure matching predicates — it is the single source of truth for which operators each field accepts, and it is what `/api/watches/fields` serves to the frontend. `watch_active_matches` records which aircraft currently match which watch so a notification fires only when a match starts; a match that goes 10 minutes without being re-confirmed ends, and the aircraft can notify again on its next sighting. `watch_notifications` is the permanent hit history and is written whether or not Apprise sending succeeded. `known_aircraft` is a never-pruned archive of every hex ever seen, backing the "first time ever seen" criterion.

Matching runs against the live readsb snapshot rather than `aircraft_data`: `squawk` is never stored there at all, and `baro_rate` is written once at insert and never refreshed — like `alt_baro`/`gs`, which only ever grow to the session maximum, it describes the past, not the present. Postgres supplies only the enrichment the feed lacks (registration, routing, airline), fetched once per tick and shared with Current Sightings.

One accepted limitation: `HasSpeed` is derived from `a.Gs != 0`, and readsb omits `gs` entirely when it has no value, so an aircraft genuinely reporting 0 kt is treated as having no speed data — a "ground speed under N" watch will not match a stationary aircraft. This was kept rather than reworking the shared `Aircraft` model, since it errs toward not matching, which is the safer direction.

### Database

- Runtime queries use pgx (`db-connector.go`); migrations use golang-migrate with `lib/pq`, reading SQL files from `migrations/` (`db-migrations.go`).
- Schema changes are made by adding a numbered `.up.sql`/`.down.sql` pair to `migrations/` — they run automatically at startup.
- `data/` is the only other Go package: it embeds `airlines.csv` (`//go:embed`) for airline code→name lookups.

### Frontend (`web/`)

Svelte 5 + Tailwind CSS 4 + DaisyUI + Chart.js, built with Vite. No router — `App.svelte` composes tab components (`TabActivity`, etc.) and one component per stat card in `src/components/`. Components fetch directly from the `/api` proxy defined in `vite.config.js`.

## Configuration

All config is via environment variables loaded from `.env` (see `.env.example` and the table in README.md): readsb URL, Postgres connection, receiver `LAT`/`LON`/`RADIUS`, `DOMESTIC_COUNTRY_ISO`, `LOG_LEVEL`. `DOCKER_ENV=true` switches off daemonization; `API_PORT` overrides the default 8080.
