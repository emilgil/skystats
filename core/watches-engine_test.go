package main

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

func keys(k []watchKey) []string {
	out := make([]string, 0, len(k))
	for _, key := range k {
		out = append(out, key.Hex)
	}
	sort.Strings(out)
	return out
}

func TestDiffMatchesReportsNewMatches(t *testing.T) {
	now := time.Unix(1785399041, 0)
	current := map[watchKey]bool{
		{WatchID: 1, Hex: "aaa111"}: true,
		{WatchID: 1, Hex: "bbb222"}: true,
	}
	previous := map[watchKey]time.Time{
		{WatchID: 1, Hex: "aaa111"}: now.Add(-time.Minute),
	}

	started, ended := diffMatches(current, previous, now, 10*time.Minute)

	if got := keys(started); len(got) != 1 || got[0] != "bbb222" {
		t.Errorf("started: got %v want [bbb222]", got)
	}
	if len(ended) != 0 {
		t.Errorf("ended: got %v want none", keys(ended))
	}
}

func TestDiffMatchesKeepsStaleMatchesInsideGrace(t *testing.T) {
	now := time.Unix(1785399041, 0)
	previous := map[watchKey]time.Time{
		{WatchID: 1, Hex: "aaa111"}: now.Add(-2 * time.Minute),
	}

	started, ended := diffMatches(nil, previous, now, 10*time.Minute)

	if len(started) != 0 {
		t.Errorf("started: got %v want none", keys(started))
	}
	if len(ended) != 0 {
		t.Errorf("a dropout inside the grace window should not end the match, got %v", keys(ended))
	}
}

func TestDiffMatchesEndsMatchesPastGrace(t *testing.T) {
	now := time.Unix(1785399041, 0)
	previous := map[watchKey]time.Time{
		{WatchID: 1, Hex: "aaa111"}: now.Add(-11 * time.Minute),
		{WatchID: 1, Hex: "bbb222"}: now.Add(-time.Minute),
	}

	started, ended := diffMatches(nil, previous, now, 10*time.Minute)

	if len(started) != 0 {
		t.Errorf("started: got %v want none", keys(started))
	}
	if got := keys(ended); len(got) != 1 || got[0] != "aaa111" {
		t.Errorf("ended: got %v want [aaa111]", got)
	}
}

func TestDiffMatchesTreatsSameHexOnDifferentWatchesSeparately(t *testing.T) {
	now := time.Unix(1785399041, 0)
	current := map[watchKey]bool{
		{WatchID: 1, Hex: "aaa111"}: true,
		{WatchID: 2, Hex: "aaa111"}: true,
	}
	previous := map[watchKey]time.Time{
		{WatchID: 1, Hex: "aaa111"}: now.Add(-time.Minute),
	}

	started, _ := diffMatches(current, previous, now, 10*time.Minute)

	if len(started) != 1 || started[0].WatchID != 2 {
		t.Errorf("started: got %+v want one entry for watch 2", started)
	}
}

func strPtr(s string) *string { return &s }

func TestBuildWatchSubjectPrefersLiveValuesOverDatabase(t *testing.T) {
	a := Aircraft{Hex: "4ca7b5", Flight: "SAS1234", R: "SE-RTM", T: "B38M", Desc: "Boeing 737 MAX 8",
		AltBaro: 31000, Gs: 450, BaroRate: -1200, Squawk: "2000"}
	e := aircraftEnrichment{
		Registration: strPtr("SE-XXX"),
		IcaoType:     strPtr("B737"),
		AircraftType: strPtr("Boeing 737-800"),
		Manufacturer: strPtr("Boeing"),
		CountryName:  strPtr("Sweden"),
		AirlineName:  strPtr("Scandinavian Airlines"),
		AirlineIcao:  strPtr("SAS"),
		AirlineIata:  strPtr("SK"),
		OriginIcao:   strPtr("ESSA"),
		OriginIata:   strPtr("ARN"),
	}

	s := buildWatchSubject(a, e, 42.5, true, true)

	if s.Registration != "SE-RTM" {
		t.Errorf("registration: got %s want SE-RTM (live value wins)", s.Registration)
	}
	if s.TypeCode != "B38M" {
		t.Errorf("type code: got %s want B38M (live value wins)", s.TypeCode)
	}
	if s.Model != "Boeing 737 MAX 8" {
		t.Errorf("model: got %s want the live desc", s.Model)
	}
	if s.Manufacturer != "Boeing" || s.Country != "Sweden" {
		t.Errorf("enrichment fields not carried through: %+v", s)
	}
	if len(s.AirlineCodes) != 2 {
		t.Errorf("airline codes: got %v want SAS and SK", s.AirlineCodes)
	}
	if len(s.Origin) != 2 {
		t.Errorf("origin: got %v want ESSA and ARN", s.Origin)
	}
	if s.VerticalRateFpm != -1200 {
		t.Errorf("vertical rate: got %v want -1200", s.VerticalRateFpm)
	}
	if !s.HasAltitude || !s.HasSpeed || !s.HasPosition {
		t.Errorf("presence flags wrong: %+v", s)
	}
	if s.DistanceKm != 42.5 {
		t.Errorf("distance: got %v want 42.5 (the injected distanceKm argument)", s.DistanceKm)
	}
	if s.Squawk != "2000" {
		t.Errorf("squawk: got %s want 2000 (a.Squawk)", s.Squawk)
	}
	if !s.FirstSeenEver {
		t.Errorf("first seen ever: got false want true (the injected firstSeenEver argument)")
	}
}

func TestBuildWatchSubjectMarksMissingDataAbsent(t *testing.T) {
	s := buildWatchSubject(Aircraft{Hex: "4ca7b5"}, aircraftEnrichment{}, 0, false, false)

	if s.HasAltitude || s.HasSpeed || s.HasPosition {
		t.Errorf("an empty aircraft should report no altitude, speed or position: %+v", s)
	}
	if s.Manufacturer != "" || s.Country != "" || len(s.Origin) != 0 {
		t.Errorf("an empty enrichment should leave string fields empty: %+v", s)
	}
}

func TestBuildWatchSubjectFallsBackToDatabaseValues(t *testing.T) {
	e := aircraftEnrichment{Registration: strPtr("SE-DBB"), IcaoType: strPtr("B737"), AircraftType: strPtr("Boeing 737-800")}

	s := buildWatchSubject(Aircraft{Hex: "4ca7b5"}, e, 0, false, false)

	if s.Registration != "SE-DBB" {
		t.Errorf("registration: got %s want SE-DBB", s.Registration)
	}
	if s.TypeCode != "B737" {
		t.Errorf("type code: got %s want B737", s.TypeCode)
	}
	if s.Model != "Boeing 737-800" {
		t.Errorf("model: got %s want Boeing 737-800", s.Model)
	}
}

func startedKeys(watchID, n int) []watchKey {
	out := make([]watchKey, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, watchKey{WatchID: watchID, Hex: fmt.Sprintf("%d-%04d", watchID, i)})
	}
	return out
}

func TestPlanWatchSendsAllowsEverythingUnderTheCap(t *testing.T) {
	started := startedKeys(1, 10)

	send, warning := planWatchSends(started, map[int]string{1: "Nearby"}, 50)

	if len(send) != 10 {
		t.Errorf("got %d sends want all 10", len(send))
	}
	if warning != "" {
		t.Errorf("nothing was suppressed, so there should be no warning, got %q", warning)
	}
}

func TestPlanWatchSendsAllowsExactlyTheCap(t *testing.T) {
	started := startedKeys(1, 50)

	send, warning := planWatchSends(started, map[int]string{1: "Nearby"}, 50)

	if len(send) != 50 {
		t.Errorf("got %d sends want 50", len(send))
	}
	if warning != "" {
		t.Errorf("hitting the cap exactly should not warn, got %q", warning)
	}
}

func TestPlanWatchSendsCapsTheBurstAndNamesTheWatch(t *testing.T) {
	started := startedKeys(1, 150)

	send, warning := planWatchSends(started, map[int]string{1: "Under 100 km"}, 50)

	if len(send) != 50 {
		t.Fatalf("got %d sends want the cap of 50", len(send))
	}
	for _, want := range []string{"50", "100 of 150", `"Under 100 km": 100`} {
		if !strings.Contains(warning, want) {
			t.Errorf("warning is missing %q:\n%s", want, warning)
		}
	}
}

func TestPlanWatchSendsReportsEverySuppressedWatchOnce(t *testing.T) {
	started := append(startedKeys(1, 40), startedKeys(2, 40)...)

	send, warning := planWatchSends(started, map[int]string{1: "Broad", 2: "Also broad"}, 50)

	if len(send) != 50 {
		t.Fatalf("got %d sends want 50", len(send))
	}
	if strings.Count(warning, "suppressed") != 1 {
		t.Errorf("the cap should produce one warning, not one per watch:\n%s", warning)
	}
	for _, want := range []string{`"Broad": 15`, `"Also broad": 15`} {
		if !strings.Contains(warning, want) {
			t.Errorf("warning is missing %q:\n%s", want, warning)
		}
	}
}

func TestPlanWatchSendsSharesTheCapBetweenWatches(t *testing.T) {
	// A broad watch flooding the tick must not starve a precise one: the user
	// cares far more about the two hits from "Mil" than about any single one of
	// the hundred from a distance rule.
	started := append(startedKeys(1, 100), startedKeys(2, 2)...)

	send, _ := planWatchSends(started, map[int]string{1: "Under 100 km", 2: "Mil"}, 50)

	for _, key := range startedKeys(2, 2) {
		if !send[key] {
			t.Errorf("%s from the precise watch should have been sent", key.Hex)
		}
	}
	if len(send) != 50 {
		t.Errorf("got %d sends want 50", len(send))
	}
}

func TestPlanWatchSendsFallsBackToTheWatchIdWhenUnnamed(t *testing.T) {
	_, warning := planWatchSends(startedKeys(7, 60), nil, 50)

	if !strings.Contains(warning, "watch 7") {
		t.Errorf("warning should identify the watch even with no name:\n%s", warning)
	}
}

func TestPlanWatchSendsTreatsANonPositiveCapAsUnlimited(t *testing.T) {
	started := startedKeys(1, 150)

	send, warning := planWatchSends(started, nil, 0)

	if len(send) != 150 {
		t.Errorf("got %d sends want all 150 with the cap disabled", len(send))
	}
	if warning != "" {
		t.Errorf("an unlimited cap should not warn, got %q", warning)
	}
}

func TestPlanWatchSendsHandlesNoStartedMatches(t *testing.T) {
	send, warning := planWatchSends(nil, nil, 50)

	if len(send) != 0 || warning != "" {
		t.Errorf("got %d sends and warning %q, want neither", len(send), warning)
	}
}
