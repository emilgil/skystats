-- Processed flags for the three new distance-based statistics
ALTER TABLE aircraft_data ADD COLUMN furthest_flown_processed BOOLEAN DEFAULT false;
ALTER TABLE aircraft_data ADD COLUMN most_remaining_processed BOOLEAN DEFAULT false;
ALTER TABLE aircraft_data ADD COLUMN longest_route_processed BOOLEAN DEFAULT false;

-- Create furthest_flown_aircraft table
CREATE TABLE furthest_flown_aircraft (
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

-- Create most_remaining_aircraft table
CREATE TABLE most_remaining_aircraft (
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

-- Create longest_route_aircraft table
CREATE TABLE longest_route_aircraft (
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
