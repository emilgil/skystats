package main

import "testing"

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
