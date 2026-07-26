DROP TABLE IF EXISTS longest_route_aircraft;
DROP TABLE IF EXISTS most_remaining_aircraft;
DROP TABLE IF EXISTS furthest_flown_aircraft;

ALTER TABLE aircraft_data DROP COLUMN IF EXISTS longest_route_processed;
ALTER TABLE aircraft_data DROP COLUMN IF EXISTS most_remaining_processed;
ALTER TABLE aircraft_data DROP COLUMN IF EXISTS furthest_flown_processed;
