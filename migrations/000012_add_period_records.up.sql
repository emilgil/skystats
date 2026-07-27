-- Permanent arkiv över varje avslutad flygning (ersätter kasta-efter-utvärdering)
CREATE TABLE flight_history (
    id SERIAL PRIMARY KEY,
    hex VARCHAR NOT NULL,
    flight VARCHAR,
    registration VARCHAR,
    type VARCHAR,
    first_seen TIMESTAMPTZ NOT NULL,
    last_seen TIMESTAMPTZ,
    ground_speed NUMERIC,
    indicated_air_speed INTEGER,
    true_air_speed INTEGER,
    barometric_altitude INTEGER,
    geometric_altitude INTEGER,
    distance_flown NUMERIC(8,2),
    route_distance NUMERIC(8,2),
    distance_remaining NUMERIC(8,2),
    origin_icao_code VARCHAR,
    origin_iata_code VARCHAR,
    destination_icao_code VARCHAR,
    destination_iata_code VARCHAR,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT flight_history_unique_hex_first_seen UNIQUE (hex, first_seen)
);

CREATE INDEX idx_flight_history_first_seen ON flight_history (first_seen);
CREATE INDEX idx_flight_history_ground_speed ON flight_history (ground_speed) WHERE ground_speed IS NOT NULL;
CREATE INDEX idx_flight_history_barometric_altitude ON flight_history (barometric_altitude) WHERE barometric_altitude IS NOT NULL;
CREATE INDEX idx_flight_history_distance_flown ON flight_history (distance_flown) WHERE distance_flown IS NOT NULL;
CREATE INDEX idx_flight_history_route_distance ON flight_history (route_distance) WHERE route_distance IS NOT NULL;
CREATE INDEX idx_flight_history_distance_remaining ON flight_history (distance_remaining) WHERE distance_remaining IS NOT NULL;

-- Enhetlig leaderboard-tabell: en rad per kategori+period+flygning, cappad till 100 i appen
CREATE TABLE records (
    id SERIAL PRIMARY KEY,
    category VARCHAR NOT NULL CHECK (category IN
        ('fastest','slowest','highest','lowest','furthest_flown','longest_route','most_remaining')),
    period_type VARCHAR NOT NULL CHECK (period_type IN
        ('24h','7d','30d','90d','365d','all_time')),
    hex VARCHAR NOT NULL,
    flight VARCHAR,
    registration VARCHAR,
    type VARCHAR,
    first_seen TIMESTAMPTZ NOT NULL,
    last_seen TIMESTAMPTZ,
    metric_name VARCHAR NOT NULL,
    metric_value NUMERIC NOT NULL,
    details JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT records_unique_category_period_hex_first_seen UNIQUE (category, period_type, hex, first_seen)
);

CREATE INDEX idx_records_category_period_metric ON records (category, period_type, metric_value);
CREATE INDEX idx_records_hex_first_seen ON records (hex, first_seen);

-- Nya settings
INSERT INTO user_settings (setting_key, setting_value, description) VALUES
    ('leaderboard_sweep_interval_minutes', '60', 'Hur ofta sweep-jobbet kör och rensar utgångna period-poster, i minuter'),
    ('history_retention_days', '730', 'Hur många dagars flight_history som sparas innan städning. Poster som fortfarande finns i records skyddas oavsett ålder.')
ON CONFLICT (setting_key) DO NOTHING;

-- Bootstrap: flytta befintlig data från de 7 gamla tabellerna in i flight_history
-- (mergar per hex+first_seen ifall samma flygning satte flera rekord samtidigt)
INSERT INTO flight_history (hex, flight, registration, type, first_seen, last_seen, ground_speed, indicated_air_speed, true_air_speed)
SELECT hex, flight, registration, type, first_seen, last_seen, ground_speed, indicated_air_speed, true_air_speed FROM fastest_aircraft
UNION ALL
SELECT hex, flight, registration, type, first_seen, last_seen, ground_speed, indicated_air_speed, true_air_speed FROM slowest_aircraft
ON CONFLICT (hex, first_seen) DO UPDATE SET
    ground_speed = COALESCE(EXCLUDED.ground_speed, flight_history.ground_speed),
    indicated_air_speed = COALESCE(EXCLUDED.indicated_air_speed, flight_history.indicated_air_speed),
    true_air_speed = COALESCE(EXCLUDED.true_air_speed, flight_history.true_air_speed);

INSERT INTO flight_history (hex, flight, registration, type, first_seen, last_seen, barometric_altitude, geometric_altitude)
SELECT hex, flight, registration, type, first_seen, last_seen, barometric_altitude, geometric_altitude FROM highest_aircraft
UNION ALL
SELECT hex, flight, registration, type, first_seen, last_seen, barometric_altitude, geometric_altitude FROM lowest_aircraft
ON CONFLICT (hex, first_seen) DO UPDATE SET
    barometric_altitude = COALESCE(EXCLUDED.barometric_altitude, flight_history.barometric_altitude),
    geometric_altitude = COALESCE(EXCLUDED.geometric_altitude, flight_history.geometric_altitude);

INSERT INTO flight_history (hex, flight, registration, type, first_seen, last_seen, distance_flown, origin_icao_code, origin_iata_code, destination_icao_code, destination_iata_code)
SELECT hex, flight, registration, type, first_seen, last_seen, distance_flown, origin_icao_code, origin_iata_code, destination_icao_code, destination_iata_code
FROM furthest_flown_aircraft
ON CONFLICT (hex, first_seen) DO UPDATE SET
    distance_flown = COALESCE(EXCLUDED.distance_flown, flight_history.distance_flown),
    origin_icao_code = COALESCE(EXCLUDED.origin_icao_code, flight_history.origin_icao_code),
    origin_iata_code = COALESCE(EXCLUDED.origin_iata_code, flight_history.origin_iata_code),
    destination_icao_code = COALESCE(EXCLUDED.destination_icao_code, flight_history.destination_icao_code),
    destination_iata_code = COALESCE(EXCLUDED.destination_iata_code, flight_history.destination_iata_code);

INSERT INTO flight_history (hex, flight, registration, type, first_seen, last_seen, route_distance, origin_icao_code, origin_iata_code, destination_icao_code, destination_iata_code)
SELECT hex, flight, registration, type, first_seen, last_seen, route_distance, origin_icao_code, origin_iata_code, destination_icao_code, destination_iata_code
FROM longest_route_aircraft
ON CONFLICT (hex, first_seen) DO UPDATE SET
    route_distance = COALESCE(EXCLUDED.route_distance, flight_history.route_distance),
    origin_icao_code = COALESCE(EXCLUDED.origin_icao_code, flight_history.origin_icao_code),
    origin_iata_code = COALESCE(EXCLUDED.origin_iata_code, flight_history.origin_iata_code),
    destination_icao_code = COALESCE(EXCLUDED.destination_icao_code, flight_history.destination_icao_code),
    destination_iata_code = COALESCE(EXCLUDED.destination_iata_code, flight_history.destination_iata_code);

INSERT INTO flight_history (hex, flight, registration, type, first_seen, last_seen, distance_remaining, destination_icao_code, destination_iata_code)
SELECT hex, flight, registration, type, first_seen, last_seen, distance_remaining, destination_icao_code, destination_iata_code
FROM most_remaining_aircraft
ON CONFLICT (hex, first_seen) DO UPDATE SET
    distance_remaining = COALESCE(EXCLUDED.distance_remaining, flight_history.distance_remaining),
    destination_icao_code = COALESCE(EXCLUDED.destination_icao_code, flight_history.destination_icao_code),
    destination_iata_code = COALESCE(EXCLUDED.destination_iata_code, flight_history.destination_iata_code);

-- Bootstrap: samma gamla data in i records som period_type = 'all_time'
INSERT INTO records (category, period_type, hex, flight, registration, type, first_seen, last_seen, metric_name, metric_value, details)
SELECT 'fastest', 'all_time', hex, flight, registration, type, first_seen, last_seen, 'ground_speed', ground_speed,
       jsonb_build_object('indicated_air_speed', indicated_air_speed, 'true_air_speed', true_air_speed)
FROM fastest_aircraft
UNION ALL
SELECT 'slowest', 'all_time', hex, flight, registration, type, first_seen, last_seen, 'ground_speed', ground_speed,
       jsonb_build_object('indicated_air_speed', indicated_air_speed, 'true_air_speed', true_air_speed)
FROM slowest_aircraft
UNION ALL
SELECT 'highest', 'all_time', hex, flight, registration, type, first_seen, last_seen, 'barometric_altitude', barometric_altitude,
       jsonb_build_object('geometric_altitude', geometric_altitude)
FROM highest_aircraft
UNION ALL
SELECT 'lowest', 'all_time', hex, flight, registration, type, first_seen, last_seen, 'barometric_altitude', barometric_altitude,
       jsonb_build_object('geometric_altitude', geometric_altitude)
FROM lowest_aircraft
UNION ALL
SELECT 'furthest_flown', 'all_time', hex, flight, registration, type, first_seen, last_seen, 'distance_flown', distance_flown,
       jsonb_build_object('origin_icao_code', origin_icao_code, 'origin_iata_code', origin_iata_code,
                           'destination_icao_code', destination_icao_code, 'destination_iata_code', destination_iata_code)
FROM furthest_flown_aircraft
UNION ALL
SELECT 'longest_route', 'all_time', hex, flight, registration, type, first_seen, last_seen, 'route_distance', route_distance,
       jsonb_build_object('origin_icao_code', origin_icao_code, 'origin_iata_code', origin_iata_code,
                           'destination_icao_code', destination_icao_code, 'destination_iata_code', destination_iata_code)
FROM longest_route_aircraft
UNION ALL
SELECT 'most_remaining', 'all_time', hex, flight, registration, type, first_seen, last_seen, 'distance_remaining', distance_remaining,
       jsonb_build_object('destination_icao_code', destination_icao_code, 'destination_iata_code', destination_iata_code)
FROM most_remaining_aircraft
ON CONFLICT (category, period_type, hex, first_seen) DO NOTHING;

-- Döp om (INTE droppa) de gamla tabellerna, så appkoden inte kraschar innan den är
-- omskriven mot de nya tabellerna. Drop dem manuellt i en senare migration när appkoden är klar.
ALTER TABLE fastest_aircraft RENAME TO fastest_aircraft_deprecated;
ALTER TABLE slowest_aircraft RENAME TO slowest_aircraft_deprecated;
ALTER TABLE highest_aircraft RENAME TO highest_aircraft_deprecated;
ALTER TABLE lowest_aircraft RENAME TO lowest_aircraft_deprecated;
ALTER TABLE furthest_flown_aircraft RENAME TO furthest_flown_aircraft_deprecated;
ALTER TABLE longest_route_aircraft RENAME TO longest_route_aircraft_deprecated;
ALTER TABLE most_remaining_aircraft RENAME TO most_remaining_aircraft_deprecated;
