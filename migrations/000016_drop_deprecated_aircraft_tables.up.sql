-- Migration 000012 renamed these 7 snapshot-based leaderboard tables to
-- *_deprecated instead of dropping them outright, so a rollback wouldn't
-- crash app code that was still writing to them. All ingest (stats-motion.go,
-- stats-distance.go) and read (api.go) code has since been rewritten against
-- the unified records/flight_history tables, so nothing references these by
-- name anymore. Safe to drop for good.
DROP TABLE IF EXISTS fastest_aircraft_deprecated;
DROP TABLE IF EXISTS slowest_aircraft_deprecated;
DROP TABLE IF EXISTS highest_aircraft_deprecated;
DROP TABLE IF EXISTS lowest_aircraft_deprecated;
DROP TABLE IF EXISTS furthest_flown_aircraft_deprecated;
DROP TABLE IF EXISTS longest_route_aircraft_deprecated;
DROP TABLE IF EXISTS most_remaining_aircraft_deprecated;
