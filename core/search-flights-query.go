package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// flightSearchParams is the fully validated, normalized form of every
// /api/search/flights query parameter. parseFlightSearchParams is the only
// way to construct one — every field has already passed whitelist/range
// validation by the time a handler sees it.
type flightSearchParams struct {
	From, To time.Time // zero Time means "no bound"

	Manufacturer string
	Model        string
	Country      string // ISO alpha-2, e.g. "GB"
	Origin       string // IATA code
	Destination  string // IATA code
	Airline      string

	AltitudeOp        string // "gte" or "lte"
	AltitudeValue     int
	HasAltitudeFilter bool

	SpeedOp        string
	SpeedValue     int
	HasSpeedFilter bool

	Interesting string // "" (no filter) or one of Mil/Gov/Pol/Civ

	OriginStatus      string // any/known/unknown
	DestinationStatus string // any/known/unknown

	Query string // free text against flight/registration/hex

	Sort string
	Dir  string

	Page     int
	PageSize int
}

// searchSortColumns whitelists which query-param sort keys are accepted and
// what SQL column each maps to. sort is never taken from the request and used
// in SQL directly — only a value that is a key in this map may pass through
// parseFlightSearchParams.
var searchSortColumns = map[string]string{
	"first_seen":          "fh.first_seen",
	"last_seen":           "fh.last_seen",
	"ground_speed":        "fh.ground_speed",
	"barometric_altitude": "fh.barometric_altitude",
	"distance_flown":      "fh.distance_flown",
	"route_distance":      "fh.route_distance",
	"distance_remaining":  "fh.distance_remaining",
}

// interestingGroupCodes maps the API's English query values to the short
// codes stored in interesting_aircraft."group" (Mil/Gov/Pol/Civ), matching
// the same mapping /api/stats/interesting/{military,government,police,civilian}
// already uses (see api.go's getRecentInterestingAircraft call sites).
var interestingGroupCodes = map[string]string{
	"military":   "Mil",
	"government": "Gov",
	"police":     "Pol",
	"civilian":   "Civ",
}

const dateLayout = "2006-01-02"

func parseFlightSearchParams(q url.Values, now time.Time) (flightSearchParams, error) {
	p := flightSearchParams{
		OriginStatus:      "any",
		DestinationStatus: "any",
		Sort:              "first_seen",
		Dir:               "desc",
		Page:              1,
		PageSize:          50,
	}

	fromStr := strings.TrimSpace(q.Get("from"))
	toStr := strings.TrimSpace(q.Get("to"))
	if fromStr != "" || toStr != "" {
		if fromStr == "" || toStr == "" {
			return p, fmt.Errorf("from and to must both be provided for a custom date range")
		}
		from, err := time.Parse(dateLayout, fromStr)
		if err != nil {
			return p, fmt.Errorf("invalid from date, expected YYYY-MM-DD")
		}
		to, err := time.Parse(dateLayout, toStr)
		if err != nil {
			return p, fmt.Errorf("invalid to date, expected YYYY-MM-DD")
		}
		to = to.Add(24 * time.Hour) // to is inclusive of the whole day
		if !from.Before(to) {
			return p, fmt.Errorf("from must be before to")
		}
		p.From = from
		p.To = to
	} else {
		period := strings.TrimSpace(q.Get("period"))
		if period == "" {
			period = "all_time"
		}
		if !isValidPeriodType(period) {
			return p, fmt.Errorf("invalid period %q", period)
		}
		if window, ok := periodWindow(period); ok {
			p.From = now.Add(-window)
		}
	}

	p.Manufacturer = strings.TrimSpace(q.Get("manufacturer"))
	p.Model = strings.TrimSpace(q.Get("model"))
	p.Country = strings.TrimSpace(q.Get("country"))
	p.Origin = strings.TrimSpace(q.Get("origin"))
	p.Destination = strings.TrimSpace(q.Get("destination"))
	p.Airline = strings.TrimSpace(q.Get("airline"))
	p.Query = strings.TrimSpace(q.Get("q"))

	altOp := strings.TrimSpace(q.Get("altitude_op"))
	altVal := strings.TrimSpace(q.Get("altitude_value"))
	if altOp != "" || altVal != "" {
		if altOp == "" || altVal == "" {
			return p, fmt.Errorf("altitude_op and altitude_value must be provided together")
		}
		if altOp != "gte" && altOp != "lte" {
			return p, fmt.Errorf("altitude_op must be gte or lte")
		}
		v, err := strconv.Atoi(altVal)
		if err != nil {
			return p, fmt.Errorf("altitude_value must be an integer")
		}
		p.AltitudeOp = altOp
		p.AltitudeValue = v
		p.HasAltitudeFilter = true
	}

	speedOp := strings.TrimSpace(q.Get("speed_op"))
	speedVal := strings.TrimSpace(q.Get("speed_value"))
	if speedOp != "" || speedVal != "" {
		if speedOp == "" || speedVal == "" {
			return p, fmt.Errorf("speed_op and speed_value must be provided together")
		}
		if speedOp != "gte" && speedOp != "lte" {
			return p, fmt.Errorf("speed_op must be gte or lte")
		}
		v, err := strconv.Atoi(speedVal)
		if err != nil {
			return p, fmt.Errorf("speed_value must be an integer")
		}
		p.SpeedOp = speedOp
		p.SpeedValue = v
		p.HasSpeedFilter = true
	}

	if interesting := strings.TrimSpace(q.Get("interesting")); interesting != "" {
		code, ok := interestingGroupCodes[interesting]
		if !ok {
			return p, fmt.Errorf("invalid interesting %q", interesting)
		}
		p.Interesting = code
	}

	if v := strings.TrimSpace(q.Get("origin_status")); v != "" {
		if v != "any" && v != "known" && v != "unknown" {
			return p, fmt.Errorf("origin_status must be any, known or unknown")
		}
		p.OriginStatus = v
	}
	if v := strings.TrimSpace(q.Get("destination_status")); v != "" {
		if v != "any" && v != "known" && v != "unknown" {
			return p, fmt.Errorf("destination_status must be any, known or unknown")
		}
		p.DestinationStatus = v
	}

	if v := strings.TrimSpace(q.Get("sort")); v != "" {
		if _, ok := searchSortColumns[v]; !ok {
			return p, fmt.Errorf("invalid sort %q", v)
		}
		p.Sort = v
	}
	if v := strings.TrimSpace(q.Get("dir")); v != "" {
		if v != "asc" && v != "desc" {
			return p, fmt.Errorf("dir must be asc or desc")
		}
		p.Dir = v
	}

	if v, err := strconv.Atoi(q.Get("page")); err == nil && v > 0 {
		p.Page = v
	}
	if v, err := strconv.Atoi(q.Get("page_size")); err == nil && v > 0 && v <= 50 {
		p.PageSize = v
	}

	return p, nil
}

// buildFlightSearchWhere turns validated params into a SQL WHERE fragment
// (without the WHERE keyword) plus the $n-ordered argument list. Every value
// is passed as an argument — no filter value is ever concatenated into where.
// Column names come only from fixed string literals in this function, never
// from user input.
func buildFlightSearchWhere(p flightSearchParams) (string, []any) {
	var conditions []string
	var args []any

	addCond := func(cond string, val any) {
		args = append(args, val)
		conditions = append(conditions, fmt.Sprintf(cond, len(args)))
	}

	if !p.From.IsZero() {
		addCond("fh.first_seen >= $%d", p.From)
	}
	if !p.To.IsZero() {
		addCond("fh.first_seen < $%d", p.To)
	}
	if p.Manufacturer != "" {
		addCond("rd.manufacturer ILIKE $%d", "%"+p.Manufacturer+"%")
	}
	if p.Model != "" {
		addCond("rd.type ILIKE $%d", "%"+p.Model+"%")
	}
	if p.Country != "" {
		addCond("UPPER(rd.registered_owner_country_iso_name) = UPPER($%d)", p.Country)
	}
	if p.Origin != "" {
		addCond("UPPER(fh.origin_iata_code) = UPPER($%d)", p.Origin)
	}
	if p.Destination != "" {
		addCond("UPPER(fh.destination_iata_code) = UPPER($%d)", p.Destination)
	}
	if p.HasAltitudeFilter {
		op := ">="
		if p.AltitudeOp == "lte" {
			op = "<="
		}
		args = append(args, p.AltitudeValue)
		conditions = append(conditions, fmt.Sprintf("fh.barometric_altitude %s $%d", op, len(args)))
	}
	if p.HasSpeedFilter {
		op := ">="
		if p.SpeedOp == "lte" {
			op = "<="
		}
		args = append(args, p.SpeedValue)
		conditions = append(conditions, fmt.Sprintf("fh.ground_speed %s $%d", op, len(args)))
	}
	if p.Airline != "" {
		addCond("rt.airline_name ILIKE $%d", "%"+p.Airline+"%")
	}
	if p.Interesting != "" {
		addCond(`ia."group" = $%d`, p.Interesting)
	}
	switch p.OriginStatus {
	case "known":
		conditions = append(conditions, "fh.origin_iata_code IS NOT NULL AND fh.origin_iata_code != ''")
	case "unknown":
		conditions = append(conditions, "(fh.origin_iata_code IS NULL OR fh.origin_iata_code = '')")
	}
	switch p.DestinationStatus {
	case "known":
		conditions = append(conditions, "fh.destination_iata_code IS NOT NULL AND fh.destination_iata_code != ''")
	case "unknown":
		conditions = append(conditions, "(fh.destination_iata_code IS NULL OR fh.destination_iata_code = '')")
	}
	if p.Query != "" {
		n := len(args) + 1
		args = append(args, "%"+p.Query+"%")
		conditions = append(conditions, fmt.Sprintf("(fh.flight ILIKE $%d OR fh.registration ILIKE $%d OR fh.hex ILIKE $%d)", n, n, n))
	}

	if len(conditions) == 0 {
		return "TRUE", args
	}
	return strings.Join(conditions, " AND "), args
}

// flightSearchOrderBy builds the ORDER BY clause. p.Sort is guaranteed by
// parseFlightSearchParams to be a key of searchSortColumns, and p.Dir is
// guaranteed to be "asc" or "desc" — both are validated before this is ever
// called, so no further checking happens here. fh.id is a tiebreaker: several
// flight_history metric columns (and first_seen itself, whole-second
// granularity) can tie across rows ingested in the same tick.
func flightSearchOrderBy(p flightSearchParams) string {
	col := searchSortColumns[p.Sort]
	dir := "DESC"
	if p.Dir == "asc" {
		dir = "ASC"
	}
	return fmt.Sprintf("%s %s, fh.id %s", col, dir, dir)
}

// flightSearchRow is one row of the search result set — every column the
// frontend needs, including the metrics that aren't filterable in v1
// (indicated/true air speed, geometric altitude), since the detail modal
// shows all of them. Nullable columns are pointers because LEFT JOINs and
// legitimately-missing readsb metrics both produce SQL NULLs here.
type flightSearchRow struct {
	Hex                        string
	Flight                     *string
	Registration               *string
	Type                       *string
	FirstSeen                  time.Time
	LastSeen                   *time.Time
	GroundSpeed                *float64
	IndicatedAirSpeed          *int
	TrueAirSpeed               *int
	BarometricAltitude         *int
	GeometricAltitude          *int
	DistanceFlown              *float64
	RouteDistance              *float64
	DistanceRemaining          *float64
	OriginIataCode             *string
	OriginIcaoCode             *string
	DestinationIataCode        *string
	DestinationIcaoCode        *string
	Manufacturer               *string
	Model                      *string
	RegisteredOwnerCountryName *string
	RegisteredOwnerCountryIso  *string
	InterestingGroup           *string
	AirlineName                *string
}

// flightSearchSelectColumns and flightSearchBaseQuery are shared verbatim by
// the JSON search handler and the CSV export handler (search-flights.go) so
// both endpoints filter and join identically. mode_s/hex are both lowercase
// (no case conversion needed); interesting_aircraft.icao is uppercase.
const flightSearchSelectColumns = `
	fh.hex, fh.flight, fh.registration, fh.type, fh.first_seen, fh.last_seen,
	fh.ground_speed, fh.indicated_air_speed, fh.true_air_speed,
	fh.barometric_altitude, fh.geometric_altitude,
	fh.distance_flown, fh.route_distance, fh.distance_remaining,
	fh.origin_iata_code, fh.origin_icao_code,
	fh.destination_iata_code, fh.destination_icao_code,
	rd.manufacturer, rd.type AS model,
	rd.registered_owner_country_name, rd.registered_owner_country_iso_name,
	ia."group" AS interesting_group, rt.airline_name`

const flightSearchBaseQuery = `
	FROM flight_history fh
	LEFT JOIN registration_data rd ON rd.mode_s = fh.hex
	LEFT JOIN interesting_aircraft ia ON ia.icao = UPPER(fh.hex)
	LEFT JOIN route_data rt ON rt.route_callsign = fh.flight
	WHERE `

// scanFlightSearchRow scans one row of a query selecting exactly
// flightSearchSelectColumns, in that order.
func scanFlightSearchRow(rows interface {
	Scan(dest ...any) error
}) (flightSearchRow, error) {
	var r flightSearchRow
	err := rows.Scan(
		&r.Hex, &r.Flight, &r.Registration, &r.Type, &r.FirstSeen, &r.LastSeen,
		&r.GroundSpeed, &r.IndicatedAirSpeed, &r.TrueAirSpeed,
		&r.BarometricAltitude, &r.GeometricAltitude,
		&r.DistanceFlown, &r.RouteDistance, &r.DistanceRemaining,
		&r.OriginIataCode, &r.OriginIcaoCode,
		&r.DestinationIataCode, &r.DestinationIcaoCode,
		&r.Manufacturer, &r.Model,
		&r.RegisteredOwnerCountryName, &r.RegisteredOwnerCountryIso,
		&r.InterestingGroup, &r.AirlineName,
	)
	return r, err
}

func flightSearchRowToJSON(r flightSearchRow) gin.H {
	return gin.H{
		"hex":                               r.Hex,
		"flight":                            r.Flight,
		"registration":                      r.Registration,
		"type":                              r.Type,
		"first_seen":                        r.FirstSeen,
		"last_seen":                         r.LastSeen,
		"ground_speed":                      r.GroundSpeed,
		"indicated_air_speed":               r.IndicatedAirSpeed,
		"true_air_speed":                    r.TrueAirSpeed,
		"barometric_altitude":               r.BarometricAltitude,
		"geometric_altitude":                r.GeometricAltitude,
		"distance_flown":                    r.DistanceFlown,
		"route_distance":                    r.RouteDistance,
		"distance_remaining":                r.DistanceRemaining,
		"origin_iata_code":                  r.OriginIataCode,
		"origin_icao_code":                  r.OriginIcaoCode,
		"destination_iata_code":             r.DestinationIataCode,
		"destination_icao_code":             r.DestinationIcaoCode,
		"manufacturer":                      r.Manufacturer,
		"model":                             r.Model,
		"registered_owner_country_name":     r.RegisteredOwnerCountryName,
		"registered_owner_country_iso_name": r.RegisteredOwnerCountryIso,
		"interesting_group":                 r.InterestingGroup,
		"airline_name":                      r.AirlineName,
	}
}

var flightSearchCSVHeader = []string{
	"hex", "flight", "registration", "type", "first_seen", "last_seen",
	"ground_speed", "indicated_air_speed", "true_air_speed",
	"barometric_altitude", "geometric_altitude",
	"distance_flown", "route_distance", "distance_remaining",
	"origin_iata_code", "origin_icao_code", "destination_iata_code", "destination_icao_code",
	"manufacturer", "model", "registered_owner_country_name", "registered_owner_country_iso_name",
	"interesting_group", "airline_name",
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func formatTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func formatFloatPtr(f *float64) string {
	if f == nil {
		return ""
	}
	return strconv.FormatFloat(*f, 'f', 2, 64)
}

func formatIntPtr(i *int) string {
	if i == nil {
		return ""
	}
	return strconv.Itoa(*i)
}

// flightSearchRowToCSVRecord must produce fields in exactly the order of
// flightSearchCSVHeader.
func flightSearchRowToCSVRecord(r flightSearchRow) []string {
	return []string{
		r.Hex,
		derefStr(r.Flight),
		derefStr(r.Registration),
		derefStr(r.Type),
		r.FirstSeen.UTC().Format(time.RFC3339),
		formatTimePtr(r.LastSeen),
		formatFloatPtr(r.GroundSpeed),
		formatIntPtr(r.IndicatedAirSpeed),
		formatIntPtr(r.TrueAirSpeed),
		formatIntPtr(r.BarometricAltitude),
		formatIntPtr(r.GeometricAltitude),
		formatFloatPtr(r.DistanceFlown),
		formatFloatPtr(r.RouteDistance),
		formatFloatPtr(r.DistanceRemaining),
		derefStr(r.OriginIataCode),
		derefStr(r.OriginIcaoCode),
		derefStr(r.DestinationIataCode),
		derefStr(r.DestinationIcaoCode),
		derefStr(r.Manufacturer),
		derefStr(r.Model),
		derefStr(r.RegisteredOwnerCountryName),
		derefStr(r.RegisteredOwnerCountryIso),
		derefStr(r.InterestingGroup),
		derefStr(r.AirlineName),
	}
}
