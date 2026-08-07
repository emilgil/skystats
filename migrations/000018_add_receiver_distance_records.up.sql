-- Löpande min/max-avstånd till mottagaren per flygning, uppdaterat på varje
-- 2s-positionstick (aircraft.go), till skillnad från de befintliga
-- kategoriernas engångs-snapshot. Höjd/bearing sparas från samma tick som
-- satte respektive extremvärde.
ALTER TABLE aircraft_data ADD COLUMN min_distance_receiver NUMERIC(7,2);
ALTER TABLE aircraft_data ADD COLUMN min_distance_receiver_altitude INTEGER;
ALTER TABLE aircraft_data ADD COLUMN min_distance_receiver_bearing NUMERIC(5,2);
ALTER TABLE aircraft_data ADD COLUMN max_distance_receiver NUMERIC(7,2);
ALTER TABLE aircraft_data ADD COLUMN max_distance_receiver_altitude INTEGER;
ALTER TABLE aircraft_data ADD COLUMN max_distance_receiver_bearing NUMERIC(5,2);
ALTER TABLE aircraft_data ADD COLUMN nearest_processed BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE aircraft_data ADD COLUMN furthest_processed BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE flight_history ADD COLUMN min_distance_receiver NUMERIC(7,2);
ALTER TABLE flight_history ADD COLUMN min_distance_receiver_altitude INTEGER;
ALTER TABLE flight_history ADD COLUMN min_distance_receiver_bearing NUMERIC(5,2);
ALTER TABLE flight_history ADD COLUMN max_distance_receiver NUMERIC(7,2);
ALTER TABLE flight_history ADD COLUMN max_distance_receiver_altitude INTEGER;
ALTER TABLE flight_history ADD COLUMN max_distance_receiver_bearing NUMERIC(5,2);

-- records.category was created without an explicit constraint name in
-- migration 000012, so Postgres auto-named it <table>_<column>_check.
ALTER TABLE records DROP CONSTRAINT records_category_check;
ALTER TABLE records ADD CONSTRAINT records_category_check CHECK (category IN
    ('fastest','slowest','highest','lowest','furthest_flown','longest_route','most_remaining',
     'nearest','furthest_range'));

INSERT INTO user_settings (setting_key, setting_value, description) VALUES
    ('notify_record_nearest',        'true', 'Notify on new all-time nearest'),
    ('notify_record_furthest_range', 'true', 'Notify on new all-time furthest range')
ON CONFLICT (setting_key) DO NOTHING;
