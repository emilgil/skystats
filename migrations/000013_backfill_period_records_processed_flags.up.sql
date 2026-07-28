-- Bugfix: the four motion-stat "processed" flags on aircraft_data predate the
-- period-bucketed leaderboard (migration 000012) and were already true for
-- rows the old single "all-time" leaderboard had already scored. The new
-- ingest logic (getAircraftsForMeasurementStatistics in stats-motion.go)
-- skips any row already marked processed, so those rows were never
-- (re-)evaluated against the new 24h/7d/30d/90d/365d windows in
-- periodsForFirstSeen — they only ended up in period_type = 'all_time' via
-- the migration 000012 bootstrap. Net effect: aircraft first seen before this
-- feature was deployed never appear in the windowed leaderboards, even if
-- they were faster/higher/etc. than everything seen since deploy.
--
-- Reset the flags for rows still within the largest windowed period (365
-- days) so the next tick of updateMeasurementStatistics() re-evaluates them
-- with the existing, already-tested ingest code and buckets them correctly.
-- Rows older than 365 days are left alone — they can only ever affect
-- all_time, which migration 000012 already bootstrapped.
UPDATE aircraft_data
SET fastest_aircraft_processed = false,
    slowest_aircraft_processed = false,
    highest_aircraft_processed = false,
    lowest_aircraft_processed = false
WHERE first_seen >= NOW() - INTERVAL '365 days';
