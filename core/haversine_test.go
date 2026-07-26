package main

import (
	"math"
	"testing"
)

func TestHaversineDistanceKm_SamePoint(t *testing.T) {
	distance := haversineDistanceKm(51.4700, -0.4543, 51.4700, -0.4543)
	if math.Abs(distance) > 0.0001 {
		t.Errorf("expected ~0 km for identical points, got %f", distance)
	}
}

func TestHaversineDistanceKm_QuarterEquator(t *testing.T) {
	// (0,0) to (0,90) is an exact quarter of the Earth's circumference along
	// the equator: (pi/2) * earthRadiusKm.
	distance := haversineDistanceKm(0, 0, 0, 90)
	expected := (math.Pi / 2) * earthRadiusKm
	if math.Abs(distance-expected) > 1.0 {
		t.Errorf("expected %.2f km, got %.2f km", expected, distance)
	}
}

func TestHaversineDistanceKm_KnownRoute(t *testing.T) {
	// London Heathrow (LHR) to New York JFK — commonly cited great-circle
	// distance is ~5540 km. Allow generous tolerance since we're checking
	// order-of-magnitude correctness, not a specific published figure.
	lhrLat, lhrLon := 51.4700, -0.4543
	jfkLat, jfkLon := 40.6413, -73.7781

	distance := haversineDistanceKm(lhrLat, lhrLon, jfkLat, jfkLon)
	if distance < 5400 || distance > 5650 {
		t.Errorf("expected LHR-JFK distance between 5400-5650 km, got %.2f km", distance)
	}
}
