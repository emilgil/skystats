-- User-defined watches: rules matched against tracked aircraft, one Apprise
-- notification per sighting that starts matching.
CREATE TABLE watches (
    id          SERIAL PRIMARY KEY,
    name        TEXT NOT NULL,
    enabled     BOOLEAN NOT NULL DEFAULT true,
    combinator  TEXT NOT NULL DEFAULT 'AND' CHECK (combinator IN ('AND', 'OR')),
    apprise_key TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Flat list of criteria per watch. No nested groups by design.
CREATE TABLE watch_conditions (
    id       SERIAL PRIMARY KEY,
    watch_id INTEGER NOT NULL REFERENCES watches(id) ON DELETE CASCADE,
    field    TEXT NOT NULL,
    operator TEXT NOT NULL,
    value    TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_watch_conditions_watch ON watch_conditions (watch_id);

-- Which aircraft currently match which watch. A row is created when a match
-- starts (and only then is a notification sent) and removed when the match has
-- not been re-confirmed for the grace window.
CREATE TABLE watch_active_matches (
    watch_id   INTEGER NOT NULL REFERENCES watches(id) ON DELETE CASCADE,
    hex        TEXT NOT NULL,
    matched_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (watch_id, hex)
);

-- Hit history. watch_id goes NULL if the watch is deleted; watch_name keeps
-- the row readable.
CREATE TABLE watch_notifications (
    id              SERIAL PRIMARY KEY,
    watch_id        INTEGER REFERENCES watches(id) ON DELETE SET NULL,
    watch_name      TEXT NOT NULL,
    hex             TEXT NOT NULL,
    flight          TEXT,
    registration    TEXT,
    snapshot        JSONB,
    notified_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    apprise_success BOOLEAN NOT NULL DEFAULT false,
    apprise_error   TEXT
);

CREATE INDEX idx_watch_notifications_notified_at ON watch_notifications (notified_at DESC);
CREATE INDEX idx_watch_notifications_watch ON watch_notifications (watch_id, notified_at DESC);

-- Permanent "have we ever seen this hex" archive, independent of any retention
-- applied to aircraft_data or flight_history. Never pruned.
CREATE TABLE known_aircraft (
    hex           TEXT PRIMARY KEY,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Backfill from the full sighting archive so existing aircraft are not
-- reported as first-ever sightings after this migration runs.
INSERT INTO known_aircraft (hex, first_seen_at)
SELECT hex, MIN(first_seen)
FROM aircraft_data
WHERE hex IS NOT NULL AND hex <> '' AND first_seen IS NOT NULL
GROUP BY hex
ON CONFLICT (hex) DO NOTHING;
