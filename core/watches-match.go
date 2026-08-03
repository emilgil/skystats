package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Watch is one user-defined rule. Conditions are combined by Combinator; a
// watch with no conditions never matches. AppriseKey overrides the global
// apprise_config_key for this watch when non-empty.
type Watch struct {
	ID         int              `json:"id"`
	Name       string           `json:"name"`
	Enabled    bool             `json:"enabled"`
	Combinator string           `json:"combinator"`
	AppriseKey string           `json:"apprise_key"`
	Conditions []WatchCondition `json:"conditions"`
	CreatedAt  time.Time        `json:"created_at"`
	UpdatedAt  time.Time        `json:"updated_at"`
}

// WatchCondition is a single criterion. Value is always stored as text and
// parsed per field kind at match time.
type WatchCondition struct {
	ID       int    `json:"id"`
	WatchID  int    `json:"watch_id"`
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
}

// watchSubject is the flattened, comparable view of one aircraft that
// conditions are evaluated against. The Has* flags distinguish "genuinely
// zero" from "no data", because a missing value must never match.
//
// VerticalRateFpm has no Has flag on purpose: readsb reports 0 both for level
// flight and for an absent baro_rate, and treating that as level flight gives
// the right answer for signed over/under comparisons either way.
type watchSubject struct {
	Hex             string
	Callsign        string
	Registration    string
	TypeCode        string
	Model           string
	Manufacturer    string
	Country         string
	Airline         string
	AirlineCodes    []string
	Origin          []string
	Destination     []string
	Squawk          string
	DistanceKm      float64
	HasPosition     bool
	AltitudeFt      float64
	HasAltitude     bool
	SpeedKt         float64
	HasSpeed        bool
	VerticalRateFpm float64
	FirstSeenEver   bool

	// PhotoURL is the adsbdb photo for this airframe, empty when adsbdb has
	// none (roughly half the fleet). It is not matchable — it exists so a
	// notification can carry the picture.
	PhotoURL string
}

// Field kinds drive the value input the frontend renders and the validation
// the API applies.
const (
	watchKindString = "string"
	watchKindNumber = "number"
	watchKindFlag   = "flag"
)

// watchField describes one selectable criterion. This registry is the single
// source of truth: matching, validation and the frontend dropdowns all read it.
type watchField struct {
	Key       string   `json:"key"`
	Label     string   `json:"label"`
	Kind      string   `json:"kind"`
	Unit      string   `json:"unit,omitempty"`
	Hint      string   `json:"hint,omitempty"`
	Operators []string `json:"operators"`
}

var watchFields = []watchField{
	{Key: "manufacturer", Label: "Manufacturer", Kind: watchKindString, Operators: []string{"equals", "contains"},
		Hint: "From the adsbdb registration lookup. Often missing for military and state aircraft."},
	{Key: "type_code", Label: "Type code (ICAO)", Kind: watchKindString, Operators: []string{"equals", "contains"},
		Hint: "The four-character ICAO type designator, e.g. B738."},
	{Key: "model", Label: "Model", Kind: watchKindString, Operators: []string{"equals", "contains"},
		Hint: "Full model name, e.g. Boeing 737 MAX 8."},
	{Key: "country", Label: "Country of registration", Kind: watchKindString, Operators: []string{"equals"},
		Hint: "Where the aircraft is registered — not the country it is flying to or from."},
	{Key: "airline", Label: "Airline", Kind: watchKindString, Operators: []string{"equals", "contains"},
		Hint: "Matches the airline name or its ICAO/IATA code."},
	{Key: "origin", Label: "Origin airport", Kind: watchKindString, Operators: []string{"equals"},
		Hint: "ICAO or IATA code, e.g. ESSA or ARN."},
	{Key: "destination", Label: "Destination airport", Kind: watchKindString, Operators: []string{"equals"},
		Hint: "ICAO or IATA code, e.g. EKCH or CPH."},
	{Key: "registration", Label: "Registration", Kind: watchKindString, Operators: []string{"equals", "contains"}},
	{Key: "hex", Label: "ICAO 24-bit (hex)", Kind: watchKindString, Operators: []string{"equals", "in_list"},
		Hint: "Use a comma-separated list to watch several specific aircraft."},
	{Key: "distance_km", Label: "Distance from me", Kind: watchKindNumber, Unit: "km", Operators: []string{"over", "under"},
		Hint: "Skystats only tracks aircraft inside RADIUS, so \"over\" is capped by that radius."},
	{Key: "altitude_ft", Label: "Altitude", Kind: watchKindNumber, Unit: "ft", Operators: []string{"over", "under"},
		Hint: "Barometric altitude, the same value the Highest/Lowest records use."},
	{Key: "speed_kt", Label: "Ground speed", Kind: watchKindNumber, Unit: "kt", Operators: []string{"over", "under"},
		Hint: "Aircraft reporting exactly 0 kt are treated as having no speed data, so a very low \"under\" threshold will not match them."},
	{Key: "squawk", Label: "Squawk", Kind: watchKindString, Operators: []string{"equals", "in_list"},
		Hint: "Emergency codes: 7500 hijack, 7600 radio failure, 7700 general emergency."},
	{Key: "first_seen_ever", Label: "First time ever seen", Kind: watchKindFlag, Operators: []string{"is_true"},
		Hint: "Matches only during the very first sighting of an aircraft, never again."},
	{Key: "vertical_rate_fpm", Label: "Vertical rate", Kind: watchKindNumber, Unit: "ft/min", Operators: []string{"over", "under"},
		Hint: "Signed: positive is climbing, negative is descending."},
	{Key: "callsign", Label: "Callsign", Kind: watchKindString, Operators: []string{"equals", "contains", "starts_with"}},
}

var watchFieldsByKey = func() map[string]watchField {
	m := make(map[string]watchField, len(watchFields))
	for _, f := range watchFields {
		m[f.Key] = f
	}
	return m
}()

// watchFieldList returns the field registry for the API and the frontend.
func watchFieldList() []watchField { return watchFields }

func (f watchField) allows(operator string) bool {
	for _, op := range f.Operators {
		if op == operator {
			return true
		}
	}
	return false
}

// matchWatch reports whether the subject satisfies the watch. A watch with no
// conditions never matches, whichever combinator it uses.
func matchWatch(w Watch, s watchSubject) bool {
	if len(w.Conditions) == 0 {
		return false
	}
	if w.Combinator == "OR" {
		for _, c := range w.Conditions {
			if matchCondition(c, s) {
				return true
			}
		}
		return false
	}
	for _, c := range w.Conditions {
		if !matchCondition(c, s) {
			return false
		}
	}
	return true
}

// matchCondition evaluates one criterion. Unknown fields, operators the field
// does not allow, and absent subject data all evaluate to false.
func matchCondition(c WatchCondition, s watchSubject) bool {
	field, ok := watchFieldsByKey[c.Field]
	if !ok || !field.allows(c.Operator) {
		return false
	}

	switch field.Kind {
	case watchKindFlag:
		return c.Field == "first_seen_ever" && s.FirstSeenEver
	case watchKindNumber:
		value, err := strconv.ParseFloat(strings.TrimSpace(c.Value), 64)
		if err != nil {
			return false
		}
		subjectValue, present := numericSubjectValue(c.Field, s)
		if !present {
			return false
		}
		if c.Operator == "over" {
			return subjectValue > value
		}
		return subjectValue < value
	default:
		return matchStringCondition(c, stringSubjectValues(c.Field, s))
	}
}

// numericSubjectValue returns the subject's value for a numeric field and
// whether the aircraft actually reported it.
func numericSubjectValue(field string, s watchSubject) (float64, bool) {
	switch field {
	case "distance_km":
		return s.DistanceKm, s.HasPosition
	case "altitude_ft":
		return s.AltitudeFt, s.HasAltitude
	case "speed_kt":
		return s.SpeedKt, s.HasSpeed
	case "vertical_rate_fpm":
		return s.VerticalRateFpm, true
	}
	return 0, false
}

// stringSubjectValues returns every value a string field may legitimately
// match against — airline matches its name or either code, and an airport
// matches its ICAO or IATA code.
func stringSubjectValues(field string, s watchSubject) []string {
	switch field {
	case "manufacturer":
		return []string{s.Manufacturer}
	case "type_code":
		return []string{s.TypeCode}
	case "model":
		return []string{s.Model}
	case "country":
		return []string{s.Country}
	case "airline":
		return append([]string{s.Airline}, s.AirlineCodes...)
	case "origin":
		return s.Origin
	case "destination":
		return s.Destination
	case "registration":
		return []string{s.Registration}
	case "hex":
		return []string{s.Hex}
	case "squawk":
		return []string{s.Squawk}
	case "callsign":
		return []string{s.Callsign}
	}
	return nil
}

// matchStringCondition applies a string operator case-insensitively across all
// candidate values. An empty condition value or an all-empty subject never
// matches.
func matchStringCondition(c WatchCondition, subjectValues []string) bool {
	value := strings.ToUpper(strings.TrimSpace(c.Value))
	if value == "" {
		return false
	}

	var wanted []string
	if c.Operator == "in_list" {
		for _, part := range strings.Split(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				wanted = append(wanted, part)
			}
		}
	} else {
		wanted = []string{value}
	}

	for _, raw := range subjectValues {
		subject := strings.ToUpper(strings.TrimSpace(raw))
		if subject == "" {
			continue
		}
		for _, w := range wanted {
			switch c.Operator {
			case "contains":
				if strings.Contains(subject, w) {
					return true
				}
			case "starts_with":
				if strings.HasPrefix(subject, w) {
					return true
				}
			default: // equals, in_list
				if subject == w {
					return true
				}
			}
		}
	}
	return false
}

// validateWatch checks a watch coming in over the API before it is persisted.
func validateWatch(w Watch) error {
	if strings.TrimSpace(w.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if len(w.Name) > 200 {
		return fmt.Errorf("name must be 200 characters or fewer")
	}
	if w.Combinator != "AND" && w.Combinator != "OR" {
		return fmt.Errorf("combinator must be AND or OR")
	}
	if len(w.Conditions) == 0 {
		return fmt.Errorf("at least one condition is required")
	}
	for i, c := range w.Conditions {
		field, ok := watchFieldsByKey[c.Field]
		if !ok {
			return fmt.Errorf("condition %d: unknown field %q", i+1, c.Field)
		}
		if !field.allows(c.Operator) {
			return fmt.Errorf("condition %d: operator %q is not valid for field %q", i+1, c.Operator, c.Field)
		}
		if field.Kind == watchKindFlag {
			continue
		}
		if strings.TrimSpace(c.Value) == "" {
			return fmt.Errorf("condition %d: a value is required for field %q", i+1, c.Field)
		}
		if field.Kind == watchKindNumber {
			if _, err := strconv.ParseFloat(strings.TrimSpace(c.Value), 64); err != nil {
				return fmt.Errorf("condition %d: value for field %q must be a number", i+1, c.Field)
			}
		}
	}
	return nil
}
