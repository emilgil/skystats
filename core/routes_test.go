// core/routes_test.go
package main

import "testing"

func TestClassifyRouteAttempt_Matched(t *testing.T) {
	outcome := classifyRouteAttempt(true, 0)
	if outcome != routeMatched {
		t.Errorf("expected routeMatched, got %v", outcome)
	}
}

func TestClassifyRouteAttempt_RetryUnderCap(t *testing.T) {
	outcome := classifyRouteAttempt(false, 0)
	if outcome != routeRetry {
		t.Errorf("expected routeRetry on first miss, got %v", outcome)
	}

	outcome = classifyRouteAttempt(false, maxRouteAttempts-2)
	if outcome != routeRetry {
		t.Errorf("expected routeRetry with one attempt left, got %v", outcome)
	}
}

func TestClassifyRouteAttempt_ExhaustedAtCap(t *testing.T) {
	outcome := classifyRouteAttempt(false, maxRouteAttempts-1)
	if outcome != routeExhausted {
		t.Errorf("expected routeExhausted on the %dth miss, got %v", maxRouteAttempts, outcome)
	}
}

func TestClassifyRouteAttempt_ExhaustedPastCap(t *testing.T) {
	outcome := classifyRouteAttempt(false, maxRouteAttempts+10)
	if outcome != routeExhausted {
		t.Errorf("expected routeExhausted past the cap, got %v", outcome)
	}
}

func TestGetDistanceBetweenAirports_LongEastWestRoute(t *testing.T) {
	// DOH (Hamad Intl) to IAH (Houston George Bush) spans ~147 degrees of
	// longitude. A flat-plane approximation anchored at the receiver's
	// latitude collapses on a route this long — cheap-ruler reports roughly
	// 8900 km against a true great-circle distance of ~12930 km.
	doha := []float64{51.6081, 25.2731}     // lon, lat
	houston := []float64{-95.3414, 29.9844} // lon, lat

	distance := getDistanceBetweenAirports(doha, houston)
	if distance == nil {
		t.Fatal("expected a distance, got nil")
	}
	if *distance < 12800 || *distance > 13050 {
		t.Errorf("expected DOH-IAH distance between 12800-13050 km, got %.2f km", *distance)
	}
}

func TestGetDistanceBetweenAirports_SameAirport(t *testing.T) {
	lhr := []float64{-0.4543, 51.4700} // lon, lat

	distance := getDistanceBetweenAirports(lhr, lhr)
	if distance == nil {
		t.Fatal("expected a distance, got nil")
	}
	if *distance > 0.0001 {
		t.Errorf("expected ~0 km for identical airports, got %.4f km", *distance)
	}
}
