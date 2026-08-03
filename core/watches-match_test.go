package main

import "testing"

func subject() watchSubject {
	return watchSubject{
		Hex:             "4ca7b5",
		Callsign:        "SAS1234",
		Registration:    "SE-RTM",
		TypeCode:        "B38M",
		Model:           "Boeing 737 MAX 8",
		Manufacturer:    "Boeing",
		Country:         "Sweden",
		Airline:         "Scandinavian Airlines",
		AirlineCodes:    []string{"SAS", "SK"},
		Origin:          []string{"ESSA", "ARN"},
		Destination:     []string{"EKCH", "CPH"},
		Squawk:          "2000",
		DistanceKm:      42.5,
		HasPosition:     true,
		AltitudeFt:      31000,
		HasAltitude:     true,
		SpeedKt:         450,
		HasSpeed:        true,
		VerticalRateFpm: -1200,
		FirstSeenEver:   false,
	}
}

func cond(field, operator, value string) WatchCondition {
	return WatchCondition{Field: field, Operator: operator, Value: value}
}

func TestMatchConditionStringOperators(t *testing.T) {
	s := subject()
	cases := []struct {
		name string
		c    WatchCondition
		want bool
	}{
		{"equals is case-insensitive", cond("manufacturer", "equals", "boeing"), true},
		{"equals rejects a partial value", cond("manufacturer", "equals", "boe"), false},
		{"contains matches a substring", cond("model", "contains", "MAX"), true},
		{"contains rejects an absent substring", cond("model", "contains", "Airbus"), false},
		{"starts_with matches a prefix", cond("callsign", "starts_with", "SAS"), true},
		{"starts_with rejects a non-prefix", cond("callsign", "starts_with", "RYR"), false},
		{"in_list matches one entry", cond("hex", "in_list", "abc123, 4ca7b5 ,def456"), true},
		{"in_list rejects an absent entry", cond("hex", "in_list", "abc123,def456"), false},
		{"registration equals", cond("registration", "equals", "SE-RTM"), true},
		{"type_code equals", cond("type_code", "equals", "b38m"), true},
		{"country equals", cond("country", "equals", "Sweden"), true},
		{"airline contains", cond("airline", "contains", "scandinavian"), true},
	}
	for _, tc := range cases {
		if got := matchCondition(tc.c, s); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestMatchConditionAirlineMatchesCodeOrName(t *testing.T) {
	s := subject()
	if !matchCondition(cond("airline", "equals", "SAS"), s) {
		t.Error("airline equals should match the ICAO code")
	}
	if !matchCondition(cond("airline", "equals", "SK"), s) {
		t.Error("airline equals should match the IATA code")
	}
	if matchCondition(cond("airline", "equals", "RYR"), s) {
		t.Error("airline equals should not match an unrelated code")
	}
}

func TestMatchConditionRouteMatchesIcaoOrIata(t *testing.T) {
	s := subject()
	if !matchCondition(cond("origin", "equals", "ESSA"), s) {
		t.Error("origin should match the ICAO code")
	}
	if !matchCondition(cond("origin", "equals", "arn"), s) {
		t.Error("origin should match the IATA code, case-insensitively")
	}
	if !matchCondition(cond("destination", "equals", "CPH"), s) {
		t.Error("destination should match the IATA code")
	}
	if matchCondition(cond("destination", "equals", "LHR"), s) {
		t.Error("destination should not match an unrelated airport")
	}
}

func TestMatchConditionNumericOperators(t *testing.T) {
	s := subject()
	cases := []struct {
		name string
		c    WatchCondition
		want bool
	}{
		{"distance under", cond("distance_km", "under", "50"), true},
		{"distance over", cond("distance_km", "over", "50"), false},
		{"altitude over", cond("altitude_ft", "over", "30000"), true},
		{"altitude under", cond("altitude_ft", "under", "30000"), false},
		{"speed over", cond("speed_kt", "over", "400"), true},
		{"vertical rate is signed, under", cond("vertical_rate_fpm", "under", "-1000"), true},
		{"vertical rate is signed, over", cond("vertical_rate_fpm", "over", "1000"), false},
		{"unparseable value never matches", cond("altitude_ft", "over", "high"), false},
		{"empty value never matches", cond("altitude_ft", "over", ""), false},
	}
	for _, tc := range cases {
		if got := matchCondition(tc.c, s); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestMatchConditionMissingDataNeverMatches(t *testing.T) {
	empty := watchSubject{Hex: "4ca7b5"}
	cases := []WatchCondition{
		cond("manufacturer", "contains", "Boeing"),
		cond("manufacturer", "equals", ""),
		cond("country", "equals", "Sweden"),
		cond("origin", "equals", "ESSA"),
		cond("destination", "equals", "EKCH"),
		cond("airline", "contains", "SAS"),
		cond("callsign", "starts_with", "SAS"),
		cond("squawk", "in_list", "7500,7600,7700"),
		cond("distance_km", "under", "50"),
		cond("altitude_ft", "over", "1000"),
		cond("speed_kt", "under", "100"),
		cond("first_seen_ever", "is_true", ""),
	}
	for _, c := range cases {
		if matchCondition(c, empty) {
			t.Errorf("%s/%s matched on an empty subject", c.Field, c.Operator)
		}
	}
}

func TestMatchConditionFirstSeenEver(t *testing.T) {
	s := subject()
	s.FirstSeenEver = true
	if !matchCondition(cond("first_seen_ever", "is_true", ""), s) {
		t.Error("first_seen_ever should match when the flag is set")
	}
	s.FirstSeenEver = false
	if matchCondition(cond("first_seen_ever", "is_true", ""), s) {
		t.Error("first_seen_ever should not match when the flag is clear")
	}
}

func TestMatchConditionUnknownFieldOrOperatorNeverMatches(t *testing.T) {
	s := subject()
	if matchCondition(cond("colour", "equals", "blue"), s) {
		t.Error("an unknown field should not match")
	}
	if matchCondition(cond("manufacturer", "rhymes_with", "Boeing"), s) {
		t.Error("an unknown operator should not match")
	}
	if matchCondition(cond("manufacturer", "over", "5"), s) {
		t.Error("an operator not allowed for the field should not match")
	}
}

func TestMatchWatchCombinators(t *testing.T) {
	s := subject()
	hit := cond("manufacturer", "equals", "Boeing")
	miss := cond("manufacturer", "equals", "Airbus")

	cases := []struct {
		name string
		w    Watch
		want bool
	}{
		{"AND all true", Watch{Combinator: "AND", Conditions: []WatchCondition{hit, hit}}, true},
		{"AND one false", Watch{Combinator: "AND", Conditions: []WatchCondition{hit, miss}}, false},
		{"OR one true", Watch{Combinator: "OR", Conditions: []WatchCondition{miss, hit}}, true},
		{"OR none true", Watch{Combinator: "OR", Conditions: []WatchCondition{miss, miss}}, false},
		{"AND with no conditions", Watch{Combinator: "AND"}, false},
		{"OR with no conditions", Watch{Combinator: "OR"}, false},
	}
	for _, tc := range cases {
		if got := matchWatch(tc.w, s); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestValidateWatch(t *testing.T) {
	ok := Watch{Name: "Boeing close by", Combinator: "AND", Conditions: []WatchCondition{
		cond("manufacturer", "equals", "Boeing"),
		cond("distance_km", "under", "50"),
		cond("first_seen_ever", "is_true", ""),
	}}
	if err := validateWatch(ok); err != nil {
		t.Fatalf("valid watch rejected: %v", err)
	}

	bad := []struct {
		name string
		w    Watch
	}{
		{"empty name", Watch{Name: "  ", Combinator: "AND", Conditions: []WatchCondition{cond("hex", "equals", "abc")}}},
		{"bad combinator", Watch{Name: "x", Combinator: "XOR", Conditions: []WatchCondition{cond("hex", "equals", "abc")}}},
		{"no conditions", Watch{Name: "x", Combinator: "AND"}},
		{"unknown field", Watch{Name: "x", Combinator: "AND", Conditions: []WatchCondition{cond("colour", "equals", "blue")}}},
		{"operator not allowed for field", Watch{Name: "x", Combinator: "AND", Conditions: []WatchCondition{cond("distance_km", "contains", "5")}}},
		{"empty value on a value field", Watch{Name: "x", Combinator: "AND", Conditions: []WatchCondition{cond("hex", "equals", "")}}},
		{"non-numeric value on a numeric field", Watch{Name: "x", Combinator: "AND", Conditions: []WatchCondition{cond("altitude_ft", "over", "high")}}},
	}
	for _, tc := range bad {
		if err := validateWatch(tc.w); err == nil {
			t.Errorf("%s: expected an error, got nil", tc.name)
		}
	}
}

func TestWatchFieldListIsSelfConsistent(t *testing.T) {
	fields := watchFieldList()
	if len(fields) != 16 {
		t.Fatalf("got %d fields want 16", len(fields))
	}
	seen := map[string]bool{}
	for _, f := range fields {
		if seen[f.Key] {
			t.Errorf("duplicate field key %s", f.Key)
		}
		seen[f.Key] = true
		if f.Label == "" {
			t.Errorf("field %s has no label", f.Key)
		}
		if len(f.Operators) == 0 {
			t.Errorf("field %s has no operators", f.Key)
		}
	}
}
