package main

import (
	"sort"
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

	s := buildWatchSubject(a, e, 42.5, true, false)

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
