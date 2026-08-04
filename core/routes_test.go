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
