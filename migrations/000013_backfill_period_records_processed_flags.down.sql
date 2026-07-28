-- Not perfectly reversible (per-row original values aren't tracked), but
-- restores the pre-migration "already processed" state for this cohort so a
-- rollback doesn't leave the up migration's effects in place.
UPDATE aircraft_data
SET fastest_aircraft_processed = true,
    slowest_aircraft_processed = true,
    highest_aircraft_processed = true,
    lowest_aircraft_processed = true
WHERE first_seen >= NOW() - INTERVAL '365 days';
