-- route_data.route_distance was computed with cheap-ruler, a flat-plane
-- approximation anchored at the receiver's own latitude. That made a fixed
-- pair of airports resolve to a different distance depending on where the
-- receiver stands, and it collapses on long east-west routes (DOH-IAH came
-- out at roughly 8900 km from a 59N receiver against a true 12930 km).
--
-- getDistanceBetweenAirports() now uses haversine. Existing rows are only
-- rewritten when the same callsign is seen again more than an hour later,
-- which can take a very long time for rare callsigns, so recompute them here.
-- This mirrors haversineDistanceKm() in core/haversine.go, including the
-- 6371 km mean Earth radius and the atan2 form.
--
-- The WHERE clause matches the guard in insertRoutes(): rows with missing or
-- zero coordinates never had a distance written from them in the first place.
WITH haversine AS (
    SELECT
        route_callsign,
        sin(radians(destination_latitude::double precision - origin_latitude::double precision) / 2) ^ 2
        + cos(radians(origin_latitude::double precision))
        * cos(radians(destination_latitude::double precision))
        * sin(radians(destination_longitude::double precision - origin_longitude::double precision) / 2) ^ 2
            AS a
    FROM route_data
    WHERE origin_latitude IS NOT NULL
      AND origin_longitude IS NOT NULL
      AND destination_latitude IS NOT NULL
      AND destination_longitude IS NOT NULL
      AND origin_latitude <> 0
      AND origin_longitude <> 0
      AND destination_latitude <> 0
      AND destination_longitude <> 0
)
UPDATE route_data rd
-- GREATEST guards sqrt() against a marginally negative 1 - a from floating
-- point rounding on near-antipodal pairs.
SET route_distance = 2 * 6371.0 * atan2(sqrt(h.a), sqrt(GREATEST(0, 1 - h.a)))
FROM haversine h
WHERE rd.route_callsign = h.route_callsign;
