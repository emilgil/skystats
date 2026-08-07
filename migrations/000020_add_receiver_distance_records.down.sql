-- Reverting while 'nearest'/'furthest_range' rows still exist in `records`
-- will fail the narrower CHECK below by design — clear those categories
-- (Settings → Danger Zone, or DELETE FROM records WHERE category IN
-- ('nearest','furthest_range')) before rolling back.
DELETE FROM user_settings WHERE setting_key IN ('notify_record_nearest', 'notify_record_furthest_range');

ALTER TABLE records DROP CONSTRAINT records_category_check;
ALTER TABLE records ADD CONSTRAINT records_category_check CHECK (category IN
    ('fastest','slowest','highest','lowest','furthest_flown','longest_route','most_remaining'));

ALTER TABLE flight_history DROP COLUMN IF EXISTS min_distance_receiver;
ALTER TABLE flight_history DROP COLUMN IF EXISTS min_distance_receiver_altitude;
ALTER TABLE flight_history DROP COLUMN IF EXISTS min_distance_receiver_bearing;
ALTER TABLE flight_history DROP COLUMN IF EXISTS max_distance_receiver;
ALTER TABLE flight_history DROP COLUMN IF EXISTS max_distance_receiver_altitude;
ALTER TABLE flight_history DROP COLUMN IF EXISTS max_distance_receiver_bearing;

ALTER TABLE aircraft_data DROP COLUMN IF EXISTS min_distance_receiver;
ALTER TABLE aircraft_data DROP COLUMN IF EXISTS min_distance_receiver_altitude;
ALTER TABLE aircraft_data DROP COLUMN IF EXISTS min_distance_receiver_bearing;
ALTER TABLE aircraft_data DROP COLUMN IF EXISTS max_distance_receiver;
ALTER TABLE aircraft_data DROP COLUMN IF EXISTS max_distance_receiver_altitude;
ALTER TABLE aircraft_data DROP COLUMN IF EXISTS max_distance_receiver_bearing;
ALTER TABLE aircraft_data DROP COLUMN IF EXISTS nearest_processed;
ALTER TABLE aircraft_data DROP COLUMN IF EXISTS furthest_processed;
