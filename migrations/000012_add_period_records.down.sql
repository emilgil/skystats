ALTER TABLE fastest_aircraft_deprecated RENAME TO fastest_aircraft;
ALTER TABLE slowest_aircraft_deprecated RENAME TO slowest_aircraft;
ALTER TABLE highest_aircraft_deprecated RENAME TO highest_aircraft;
ALTER TABLE lowest_aircraft_deprecated RENAME TO lowest_aircraft;
ALTER TABLE furthest_flown_aircraft_deprecated RENAME TO furthest_flown_aircraft;
ALTER TABLE longest_route_aircraft_deprecated RENAME TO longest_route_aircraft;
ALTER TABLE most_remaining_aircraft_deprecated RENAME TO most_remaining_aircraft;

DELETE FROM user_settings WHERE setting_key IN
    ('leaderboard_sweep_interval_minutes', 'history_retention_days');

DROP TABLE IF EXISTS records;
DROP TABLE IF EXISTS flight_history;
