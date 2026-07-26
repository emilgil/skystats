package main

import "math"

// earthRadiusKm is the mean radius of the Earth in kilometers, used for
// great-circle distance calculations.
const earthRadiusKm = 6371.0

// haversineDistanceKm returns the great-circle distance in kilometers
// between two points given as decimal degrees latitude/longitude.
//
// This is deliberately independent of the cheap-ruler based distance
// calculations used elsewhere in this codebase (see getRuler() in
// aircraft.go). Cheap-ruler is a flat-plane approximation anchored at the
// receiver's own latitude and loses accuracy over long distances / large
// latitude spans — exactly the routes the distance leaderboards are meant
// to surface. Haversine gives a consistent, receiver-independent result
// for any pair of points on Earth.
func haversineDistanceKm(lat1, lon1, lat2, lon2 float64) float64 {
	lat1Rad := lat1 * math.Pi / 180
	lat2Rad := lat2 * math.Pi / 180
	deltaLat := (lat2 - lat1) * math.Pi / 180
	deltaLon := (lon2 - lon1) * math.Pi / 180

	a := math.Sin(deltaLat/2)*math.Sin(deltaLat/2) +
		math.Cos(lat1Rad)*math.Cos(lat2Rad)*
			math.Sin(deltaLon/2)*math.Sin(deltaLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return earthRadiusKm * c
}
