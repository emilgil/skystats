package main

import "testing"

func TestGetDestinationDistance_LongEastWestLeg(t *testing.T) {
	// Same DOH-IAH geometry as the route test: an aircraft still over Doha
	// with Houston as its destination. destination_distance feeds the Above Me
	// progress bar as the numerator's remaining leg, so it has to agree with
	// route_distance's great-circle figure rather than a flat approximation.
	distance := getDestinationDistance(25.2731, 51.6081, 29.9844, -95.3414)
	if distance < 12800 || distance > 13050 {
		t.Errorf("expected DOH-IAH distance between 12800-13050 km, got %.2f km", distance)
	}
}

func TestGetDestinationDistance_OverTheDestination(t *testing.T) {
	distance := getDestinationDistance(51.4700, -0.4543, 51.4700, -0.4543)
	if distance != 0 {
		t.Errorf("expected 0 km when over the destination, got %.4f km", distance)
	}
}

func TestNormalizeBearing_Positive(t *testing.T) {
	if got := normalizeBearing(90); got != 90 {
		t.Errorf("expected 90, got %f", got)
	}
}

func TestNormalizeBearing_Negative(t *testing.T) {
	if got := normalizeBearing(-90); got != 270 {
		t.Errorf("expected 270, got %f", got)
	}
}

func TestNormalizeBearing_Zero(t *testing.T) {
	if got := normalizeBearing(0); got != 0 {
		t.Errorf("expected 0, got %f", got)
	}
}

func TestNormalizeBearing_UpperBound(t *testing.T) {
	// cheap-ruler's Bearing() range is (-180, 180], so 180 is the largest
	// value it can return and must pass through unchanged.
	if got := normalizeBearing(180); got != 180 {
		t.Errorf("expected 180, got %f", got)
	}
}
