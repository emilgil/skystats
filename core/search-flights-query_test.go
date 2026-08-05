package main

import (
	"net/url"
	"testing"
	"time"
)

func mustParseTime(t *testing.T, layout, value string) time.Time {
	t.Helper()
	tm, err := time.Parse(layout, value)
	if err != nil {
		t.Fatalf("bad fixture time %q: %v", value, err)
	}
	return tm
}

func TestParseFlightSearchParamsDefaults(t *testing.T) {
	now := mustParseTime(t, time.RFC3339, "2026-08-05T12:00:00Z")
	p, err := parseFlightSearchParams(url.Values{}, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Sort != "first_seen" {
		t.Errorf("Sort = %q, want first_seen", p.Sort)
	}
	if p.Dir != "desc" {
		t.Errorf("Dir = %q, want desc", p.Dir)
	}
	if p.Page != 1 {
		t.Errorf("Page = %d, want 1", p.Page)
	}
	if p.PageSize != 50 {
		t.Errorf("PageSize = %d, want 50", p.PageSize)
	}
	if p.OriginStatus != "any" || p.DestinationStatus != "any" {
		t.Errorf("status defaults = %q/%q, want any/any", p.OriginStatus, p.DestinationStatus)
	}
	if !p.From.IsZero() {
		t.Errorf("From = %v, want zero (all_time has no lower bound)", p.From)
	}
}

func TestParseFlightSearchParamsPeriodPresets(t *testing.T) {
	now := mustParseTime(t, time.RFC3339, "2026-08-05T12:00:00Z")
	cases := []struct {
		period   string
		wantFrom time.Time
	}{
		{"24h", now.Add(-24 * time.Hour)},
		{"7d", now.Add(-7 * 24 * time.Hour)},
		{"all_time", time.Time{}},
	}
	for _, tc := range cases {
		q := url.Values{"period": {tc.period}}
		p, err := parseFlightSearchParams(q, now)
		if err != nil {
			t.Fatalf("period %q: unexpected error: %v", tc.period, err)
		}
		if !p.From.Equal(tc.wantFrom) {
			t.Errorf("period %q: From = %v, want %v", tc.period, p.From, tc.wantFrom)
		}
	}
}

func TestParseFlightSearchParamsInvalidPeriod(t *testing.T) {
	now := time.Now()
	_, err := parseFlightSearchParams(url.Values{"period": {"bogus"}}, now)
	if err == nil {
		t.Fatal("expected error for invalid period, got nil")
	}
}

func TestParseFlightSearchParamsCustomRange(t *testing.T) {
	now := time.Now()
	q := url.Values{"from": {"2026-01-01"}, "to": {"2026-01-31"}}
	p, err := parseFlightSearchParams(q, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantFrom := mustParseTime(t, "2006-01-02", "2026-01-01")
	wantTo := mustParseTime(t, "2006-01-02", "2026-02-01") // to is end-exclusive, +1 day
	if !p.From.Equal(wantFrom) {
		t.Errorf("From = %v, want %v", p.From, wantFrom)
	}
	if !p.To.Equal(wantTo) {
		t.Errorf("To = %v, want %v", p.To, wantTo)
	}
}

func TestParseFlightSearchParamsCustomRangeErrors(t *testing.T) {
	now := time.Now()
	cases := map[string]url.Values{
		"only from, no to": {"from": {"2026-01-01"}},
		"only to, no from": {"to": {"2026-01-31"}},
		"bad from format":  {"from": {"01/01/2026"}, "to": {"2026-01-31"}},
		"from after to":    {"from": {"2026-02-01"}, "to": {"2026-01-01"}},
	}
	for name, q := range cases {
		if _, err := parseFlightSearchParams(q, now); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

func TestParseFlightSearchParamsAltitudeSpeedPairing(t *testing.T) {
	now := time.Now()
	cases := map[string]url.Values{
		"altitude value without op":  {"altitude_value": {"35000"}},
		"altitude op without value":  {"altitude_op": {"gte"}},
		"altitude bad op":            {"altitude_op": {"sideways"}, "altitude_value": {"35000"}},
		"altitude non-numeric value": {"altitude_op": {"gte"}, "altitude_value": {"high"}},
		"speed value without op":     {"speed_value": {"450"}},
		"speed op without value":     {"speed_op": {"lte"}},
	}
	for name, q := range cases {
		if _, err := parseFlightSearchParams(q, now); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}

	q := url.Values{"altitude_op": {"gte"}, "altitude_value": {"35000"}, "speed_op": {"lte"}, "speed_value": {"450"}}
	p, err := parseFlightSearchParams(q, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !p.HasAltitudeFilter || p.AltitudeOp != "gte" || p.AltitudeValue != 35000 {
		t.Errorf("altitude filter = %+v, want gte/35000", p)
	}
	if !p.HasSpeedFilter || p.SpeedOp != "lte" || p.SpeedValue != 450 {
		t.Errorf("speed filter = %+v, want lte/450", p)
	}
}

func TestParseFlightSearchParamsInteresting(t *testing.T) {
	now := time.Now()
	p, err := parseFlightSearchParams(url.Values{"interesting": {"military"}}, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Interesting != "Mil" {
		t.Errorf("Interesting = %q, want Mil", p.Interesting)
	}
	if _, err := parseFlightSearchParams(url.Values{"interesting": {"alien"}}, now); err == nil {
		t.Error("expected error for invalid interesting value")
	}
}

func TestParseFlightSearchParamsStatusValidation(t *testing.T) {
	now := time.Now()
	if _, err := parseFlightSearchParams(url.Values{"origin_status": {"maybe"}}, now); err == nil {
		t.Error("expected error for invalid origin_status")
	}
	if _, err := parseFlightSearchParams(url.Values{"destination_status": {"maybe"}}, now); err == nil {
		t.Error("expected error for invalid destination_status")
	}
	p, err := parseFlightSearchParams(url.Values{"origin_status": {"known"}, "destination_status": {"unknown"}}, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.OriginStatus != "known" || p.DestinationStatus != "unknown" {
		t.Errorf("status = %q/%q, want known/unknown", p.OriginStatus, p.DestinationStatus)
	}
}

func TestParseFlightSearchParamsSortAndDirWhitelist(t *testing.T) {
	now := time.Now()
	if _, err := parseFlightSearchParams(url.Values{"sort": {"'; DROP TABLE flight_history;--"}}, now); err == nil {
		t.Error("expected error for non-whitelisted sort column")
	}
	if _, err := parseFlightSearchParams(url.Values{"dir": {"sideways"}}, now); err == nil {
		t.Error("expected error for invalid dir")
	}
	p, err := parseFlightSearchParams(url.Values{"sort": {"ground_speed"}, "dir": {"asc"}}, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Sort != "ground_speed" || p.Dir != "asc" {
		t.Errorf("sort/dir = %q/%q, want ground_speed/asc", p.Sort, p.Dir)
	}
}

func TestParseFlightSearchParamsPageClamping(t *testing.T) {
	now := time.Now()
	cases := []struct {
		q            url.Values
		wantPage     int
		wantPageSize int
	}{
		{url.Values{}, 1, 50},
		{url.Values{"page": {"3"}}, 3, 50},
		{url.Values{"page": {"-1"}}, 1, 50},   // invalid, falls back to default
		{url.Values{"page": {"nope"}}, 1, 50}, // non-numeric, falls back to default
		{url.Values{"page_size": {"10"}}, 1, 10},
		{url.Values{"page_size": {"999"}}, 1, 50}, // over the cap, falls back to default
		{url.Values{"page_size": {"0"}}, 1, 50},   // zero, falls back to default
	}
	for _, tc := range cases {
		p, err := parseFlightSearchParams(tc.q, now)
		if err != nil {
			t.Fatalf("query %v: unexpected error: %v", tc.q, err)
		}
		if p.Page != tc.wantPage || p.PageSize != tc.wantPageSize {
			t.Errorf("query %v: page/pageSize = %d/%d, want %d/%d", tc.q, p.Page, p.PageSize, tc.wantPage, tc.wantPageSize)
		}
	}
}

func TestBuildFlightSearchWhereEmpty(t *testing.T) {
	p, _ := parseFlightSearchParams(url.Values{}, time.Now())
	where, args := buildFlightSearchWhere(p)
	if where != "TRUE" {
		t.Errorf("where = %q, want TRUE for no filters", where)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want empty", args)
	}
}

func TestBuildFlightSearchWhereEachFilter(t *testing.T) {
	now := mustParseTime(t, time.RFC3339, "2026-08-05T12:00:00Z")

	cases := []struct {
		name      string
		q         url.Values
		wantWhere string
		wantArgs  []any
	}{
		{"manufacturer", url.Values{"manufacturer": {"Boeing"}}, "rd.manufacturer ILIKE $1", []any{"%Boeing%"}},
		{"model", url.Values{"model": {"A320"}}, "rd.type ILIKE $1", []any{"%A320%"}},
		{"country", url.Values{"country": {"GB"}}, "UPPER(rd.registered_owner_country_iso_name) = UPPER($1)", []any{"GB"}},
		{"origin", url.Values{"origin": {"ARN"}}, "UPPER(fh.origin_iata_code) = UPPER($1)", []any{"ARN"}},
		{"destination", url.Values{"destination": {"LHR"}}, "UPPER(fh.destination_iata_code) = UPPER($1)", []any{"LHR"}},
		{"airline", url.Values{"airline": {"Ryanair"}}, "rt.airline_name ILIKE $1", []any{"%Ryanair%"}},
		{"altitude gte", url.Values{"altitude_op": {"gte"}, "altitude_value": {"35000"}}, "fh.barometric_altitude >= $1", []any{35000}},
		{"altitude lte", url.Values{"altitude_op": {"lte"}, "altitude_value": {"1000"}}, "fh.barometric_altitude <= $1", []any{1000}},
		{"speed gte", url.Values{"speed_op": {"gte"}, "speed_value": {"450"}}, "fh.ground_speed >= $1", []any{450}},
		{"interesting", url.Values{"interesting": {"military"}}, `ia."group" = $1`, []any{"Mil"}},
		{"origin known", url.Values{"origin_status": {"known"}}, "fh.origin_iata_code IS NOT NULL AND fh.origin_iata_code != ''", nil},
		{"origin unknown", url.Values{"origin_status": {"unknown"}}, "(fh.origin_iata_code IS NULL OR fh.origin_iata_code = '')", nil},
		{"destination known", url.Values{"destination_status": {"known"}}, "fh.destination_iata_code IS NOT NULL AND fh.destination_iata_code != ''", nil},
		{"destination unknown", url.Values{"destination_status": {"unknown"}}, "(fh.destination_iata_code IS NULL OR fh.destination_iata_code = '')", nil},
		{"free text", url.Values{"q": {"SAS123"}}, "(fh.flight ILIKE $1 OR fh.registration ILIKE $1 OR fh.hex ILIKE $1)", []any{"%SAS123%"}},
	}

	for _, tc := range cases {
		p, err := parseFlightSearchParams(tc.q, now)
		if err != nil {
			t.Fatalf("%s: unexpected parse error: %v", tc.name, err)
		}
		where, args := buildFlightSearchWhere(p)
		if where != tc.wantWhere {
			t.Errorf("%s: where = %q, want %q", tc.name, where, tc.wantWhere)
		}
		if tc.wantArgs == nil {
			if len(args) != 0 {
				t.Errorf("%s: args = %v, want empty", tc.name, args)
			}
			continue
		}
		if len(args) != len(tc.wantArgs) {
			t.Fatalf("%s: args = %v, want %v", tc.name, args, tc.wantArgs)
		}
		for i := range args {
			if args[i] != tc.wantArgs[i] {
				t.Errorf("%s: args[%d] = %v, want %v", tc.name, i, args[i], tc.wantArgs[i])
			}
		}
	}
}

func TestBuildFlightSearchWhereCombinesWithAndAndOrdersArgs(t *testing.T) {
	now := mustParseTime(t, time.RFC3339, "2026-08-05T12:00:00Z")
	q := url.Values{"manufacturer": {"Boeing"}, "origin": {"ARN"}, "period": {"7d"}}
	p, err := parseFlightSearchParams(q, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	where, args := buildFlightSearchWhere(p)
	want := "fh.first_seen >= $1 AND rd.manufacturer ILIKE $2 AND UPPER(fh.origin_iata_code) = UPPER($3)"
	if where != want {
		t.Errorf("where = %q, want %q", where, want)
	}
	if len(args) != 3 || args[1] != "%Boeing%" || args[2] != "ARN" {
		t.Errorf("args = %v", args)
	}
}

func TestFlightSearchOrderBy(t *testing.T) {
	cases := []struct {
		sort, dir string
		want      string
	}{
		{"first_seen", "desc", "fh.first_seen DESC, fh.id DESC"},
		{"first_seen", "asc", "fh.first_seen ASC, fh.id ASC"},
		{"ground_speed", "asc", "fh.ground_speed ASC, fh.id ASC"},
	}
	for _, tc := range cases {
		p := flightSearchParams{Sort: tc.sort, Dir: tc.dir}
		if got := flightSearchOrderBy(p); got != tc.want {
			t.Errorf("sort=%s dir=%s: got %q, want %q", tc.sort, tc.dir, got, tc.want)
		}
	}
}

func strp(s string) *string   { return &s }
func intp(i int) *int         { return &i }
func f64p(f float64) *float64 { return &f }

func TestFlightSearchRowToCSVRecordHandlesNils(t *testing.T) {
	r := flightSearchRow{
		Hex:       "4d201f",
		Flight:    strp("SAS123 "),
		FirstSeen: mustParseTime(t, time.RFC3339, "2026-08-01T10:00:00Z"),
		// every other pointer field left nil
	}
	rec := flightSearchRowToCSVRecord(r)
	if len(rec) != len(flightSearchCSVHeader) {
		t.Fatalf("record has %d fields, header has %d", len(rec), len(flightSearchCSVHeader))
	}
	if rec[0] != "4d201f" {
		t.Errorf("hex = %q, want 4d201f", rec[0])
	}
	if rec[1] != "SAS123 " {
		t.Errorf("flight = %q, want %q", rec[1], "SAS123 ")
	}
	// last_seen (index 5) is nil -> must render as empty string, not "<nil>" or panic
	if rec[5] != "" {
		t.Errorf("last_seen = %q, want empty string for nil", rec[5])
	}
}

func TestFlightSearchRowToCSVRecordFormatsNumbers(t *testing.T) {
	r := flightSearchRow{
		Hex:                "4d201f",
		FirstSeen:          mustParseTime(t, time.RFC3339, "2026-08-01T10:00:00Z"),
		BarometricAltitude: intp(35000),
		GroundSpeed:        f64p(450.5),
		DistanceFlown:      f64p(850.25),
	}
	rec := flightSearchRowToCSVRecord(r)
	idx := func(col string) int {
		for i, h := range flightSearchCSVHeader {
			if h == col {
				return i
			}
		}
		t.Fatalf("no CSV column named %q", col)
		return -1
	}
	if rec[idx("barometric_altitude")] != "35000" {
		t.Errorf("barometric_altitude = %q, want 35000", rec[idx("barometric_altitude")])
	}
	if rec[idx("ground_speed")] != "450.50" {
		t.Errorf("ground_speed = %q, want 450.50", rec[idx("ground_speed")])
	}
	if rec[idx("distance_flown")] != "850.25" {
		t.Errorf("distance_flown = %q, want 850.25", rec[idx("distance_flown")])
	}
}

func TestFlightSearchRowToJSONFieldMapping(t *testing.T) {
	firstSeen := mustParseTime(t, time.RFC3339, "2026-08-01T10:00:00Z")
	lastSeen := mustParseTime(t, time.RFC3339, "2026-08-01T12:00:00Z")

	r := flightSearchRow{
		Hex:                        "4d201f",
		Flight:                     strp("SAS123"),
		Registration:               strp("SE-ABC"),
		Type:                       strp("A320"),
		FirstSeen:                  firstSeen,
		LastSeen:                   &lastSeen,
		GroundSpeed:                f64p(450.5),
		IndicatedAirSpeed:          intp(280),
		TrueAirSpeed:               intp(430),
		BarometricAltitude:         intp(35000),
		GeometricAltitude:          intp(35100),
		DistanceFlown:              f64p(850.25),
		RouteDistance:              f64p(900.5),
		DistanceRemaining:          f64p(50.25),
		OriginIataCode:             strp("ARN"),
		OriginIcaoCode:             strp("ESSA"),
		DestinationIataCode:        strp("CPH"),
		DestinationIcaoCode:        strp("EKCH"),
		Manufacturer:               strp("Airbus"),
		Model:                      strp("A320neo"),
		RegisteredOwnerCountryName: strp("Sweden"),
		RegisteredOwnerCountryIso:  strp("SE"),
		InterestingGroup:           strp("military"),
		AirlineName:                strp("Scandinavian Airlines"),
	}

	j := flightSearchRowToJSON(r)

	cases := []struct {
		key  string
		want any
	}{
		{"hex", r.Hex},
		{"flight", r.Flight},
		{"registration", r.Registration},
		{"type", r.Type},
		{"first_seen", r.FirstSeen},
		{"last_seen", r.LastSeen},
		{"ground_speed", r.GroundSpeed},
		{"indicated_air_speed", r.IndicatedAirSpeed},
		{"true_air_speed", r.TrueAirSpeed},
		{"barometric_altitude", r.BarometricAltitude},
		{"geometric_altitude", r.GeometricAltitude},
		{"distance_flown", r.DistanceFlown},
		{"route_distance", r.RouteDistance},
		{"distance_remaining", r.DistanceRemaining},
		{"origin_iata_code", r.OriginIataCode},
		{"origin_icao_code", r.OriginIcaoCode},
		{"destination_iata_code", r.DestinationIataCode},
		{"destination_icao_code", r.DestinationIcaoCode},
		{"manufacturer", r.Manufacturer},
		{"model", r.Model},
		{"registered_owner_country_name", r.RegisteredOwnerCountryName},
		{"registered_owner_country_iso_name", r.RegisteredOwnerCountryIso},
		{"interesting_group", r.InterestingGroup},
		{"airline_name", r.AirlineName},
	}

	if len(j) != len(cases) {
		t.Fatalf("gin.H has %d keys, want %d", len(j), len(cases))
	}

	for _, tc := range cases {
		got, ok := j[tc.key]
		if !ok {
			t.Errorf("key %q missing from gin.H", tc.key)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %v, want %v", tc.key, got, tc.want)
		}
	}
}

func TestFlightSearchRowToJSONHandlesNils(t *testing.T) {
	r := flightSearchRow{
		Hex:       "4d201f",
		FirstSeen: mustParseTime(t, time.RFC3339, "2026-08-01T10:00:00Z"),
		// every other pointer field left nil
	}

	j := flightSearchRowToJSON(r)

	// Each "want" is a nil pointer of the field's own type, not the bare
	// literal nil: an interface{} wrapping a typed nil pointer is not ==
	// untyped nil in Go, and comparing against the wrong nil-typed value
	// would just as wrongly flag a correct result. Asserting the exact
	// typed nil is what actually proves the pointer survived un-dereferenced.
	nilCases := []struct {
		key  string
		want any
	}{
		{"flight", (*string)(nil)},
		{"registration", (*string)(nil)},
		{"type", (*string)(nil)},
		{"last_seen", (*time.Time)(nil)},
		{"ground_speed", (*float64)(nil)},
		{"indicated_air_speed", (*int)(nil)},
		{"true_air_speed", (*int)(nil)},
		{"barometric_altitude", (*int)(nil)},
		{"geometric_altitude", (*int)(nil)},
		{"distance_flown", (*float64)(nil)},
		{"route_distance", (*float64)(nil)},
		{"distance_remaining", (*float64)(nil)},
		{"origin_iata_code", (*string)(nil)},
		{"origin_icao_code", (*string)(nil)},
		{"destination_iata_code", (*string)(nil)},
		{"destination_icao_code", (*string)(nil)},
		{"manufacturer", (*string)(nil)},
		{"model", (*string)(nil)},
		{"registered_owner_country_name", (*string)(nil)},
		{"registered_owner_country_iso_name", (*string)(nil)},
		{"interesting_group", (*string)(nil)},
		{"airline_name", (*string)(nil)},
	}

	for _, tc := range nilCases {
		got, ok := j[tc.key]
		if !ok {
			t.Errorf("key %q missing from gin.H", tc.key)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %#v (%T), want nil %T", tc.key, got, got, tc.want)
		}
	}

	// Non-pointer fields are unaffected by the nil pointers elsewhere in the row.
	if j["hex"] != r.Hex {
		t.Errorf("hex = %v, want %v", j["hex"], r.Hex)
	}
	if j["first_seen"] != r.FirstSeen {
		t.Errorf("first_seen = %v, want %v", j["first_seen"], r.FirstSeen)
	}
}
