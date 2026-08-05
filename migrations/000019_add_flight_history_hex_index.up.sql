-- Flight search (docs/superpowers/specs/2026-08-05-flight-search-spec.md) joins
-- flight_history to registration_data and interesting_aircraft on hex for every
-- request. flight_history had no index starting with hex alone (only the
-- composite UNIQUE(hex, first_seen)), so add one.
CREATE INDEX idx_flight_history_hex ON flight_history (hex);
