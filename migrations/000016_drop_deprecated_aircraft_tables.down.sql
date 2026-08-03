-- Recreates the 7 tables dropped by the up migration, matching the structure
-- and constraint names they had at drop time (post-000012 rename). Empty --
-- their data is not recoverable, same tradeoff 000012's own down migration
-- already makes for records/flight_history.
CREATE TABLE fastest_aircraft_deprecated (
    id SERIAL PRIMARY KEY,
    hex VARCHAR,
    flight VARCHAR,
    registration VARCHAR,
    type VARCHAR,
    first_seen TIMESTAMPTZ,
    last_seen TIMESTAMPTZ,
    ground_speed NUMERIC(6,1),
    indicated_air_speed INTEGER,
    true_air_speed INTEGER,
    CONSTRAINT fastest_aircraft_unique_hex_first_seen UNIQUE (hex, first_seen)
);

CREATE TABLE slowest_aircraft_deprecated (
    id SERIAL PRIMARY KEY,
    hex VARCHAR,
    flight VARCHAR,
    registration VARCHAR,
    type VARCHAR,
    first_seen TIMESTAMPTZ,
    last_seen TIMESTAMPTZ,
    ground_speed NUMERIC(6,1),
    indicated_air_speed INTEGER,
    true_air_speed INTEGER,
    CONSTRAINT slowest_aircraft_unique_hex_first_seen UNIQUE (hex, first_seen)
);

CREATE TABLE highest_aircraft_deprecated (
    id SERIAL PRIMARY KEY,
    hex VARCHAR,
    flight VARCHAR,
    registration VARCHAR,
    type VARCHAR,
    first_seen TIMESTAMPTZ,
    last_seen TIMESTAMPTZ,
    barometric_altitude INTEGER,
    geometric_altitude INTEGER,
    CONSTRAINT highest_aircraft_unique_hex_first_seen UNIQUE (hex, first_seen)
);

CREATE TABLE lowest_aircraft_deprecated (
    id SERIAL PRIMARY KEY,
    hex VARCHAR,
    flight VARCHAR,
    registration VARCHAR,
    type VARCHAR,
    first_seen TIMESTAMPTZ,
    last_seen TIMESTAMPTZ,
    barometric_altitude INTEGER,
    geometric_altitude INTEGER,
    CONSTRAINT lowest_aircraft_unique_hex_first_seen UNIQUE (hex, first_seen)
);

CREATE TABLE furthest_flown_aircraft_deprecated (
    id SERIAL PRIMARY KEY,
    hex VARCHAR,
    flight VARCHAR,
    registration VARCHAR,
    type VARCHAR,
    first_seen TIMESTAMPTZ,
    last_seen TIMESTAMPTZ,
    origin_icao_code VARCHAR,
    origin_iata_code VARCHAR,
    destination_icao_code VARCHAR,
    destination_iata_code VARCHAR,
    distance_flown NUMERIC(8,2),
    CONSTRAINT furthest_flown_aircraft_unique_hex_first_seen UNIQUE (hex, first_seen)
);

CREATE TABLE most_remaining_aircraft_deprecated (
    id SERIAL PRIMARY KEY,
    hex VARCHAR,
    flight VARCHAR,
    registration VARCHAR,
    type VARCHAR,
    first_seen TIMESTAMPTZ,
    last_seen TIMESTAMPTZ,
    destination_icao_code VARCHAR,
    destination_iata_code VARCHAR,
    distance_remaining NUMERIC(8,2),
    CONSTRAINT most_remaining_aircraft_unique_hex_first_seen UNIQUE (hex, first_seen)
);

CREATE TABLE longest_route_aircraft_deprecated (
    id SERIAL PRIMARY KEY,
    hex VARCHAR,
    flight VARCHAR,
    registration VARCHAR,
    type VARCHAR,
    first_seen TIMESTAMPTZ,
    last_seen TIMESTAMPTZ,
    origin_icao_code VARCHAR,
    origin_iata_code VARCHAR,
    destination_icao_code VARCHAR,
    destination_iata_code VARCHAR,
    route_distance NUMERIC(8,2),
    CONSTRAINT longest_route_aircraft_unique_hex_first_seen UNIQUE (hex, first_seen)
);
