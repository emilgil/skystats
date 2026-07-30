package main

import (
	"math"
	"sort"
	"sync"
	"time"
)

// CurrentSighting is one row of the Current Sightings table: an aircraft the
// receiver can see right now. Live values come from the readsb snapshot; the
// optional fields are joined in from Postgres and stay nil until the
// registration and route enrichment jobs have caught up with the aircraft.
type CurrentSighting struct {
	Hex              string    `json:"hex"`
	Flight           string    `json:"flight"`
	Registration     string    `json:"registration"`
	Type             string    `json:"type"`
	TypeDescription  *string   `json:"type_description"`
	Airline          *string   `json:"airline"`
	Operator         *string   `json:"operator"`
	Altitude         int       `json:"altitude"`
	GroundSpeed      float64   `json:"ground_speed"`
	Track            float64   `json:"track"`
	DistanceKm       float64   `json:"distance_km"`
	OriginIata       *string   `json:"origin_iata"`
	OriginName       *string   `json:"origin_name"`
	DestinationIata  *string   `json:"destination_iata"`
	DestinationName  *string   `json:"destination_name"`
	InterestingGroup *string   `json:"interesting_group"`
	LastSeen         time.Time `json:"last_seen"`
}

// aircraftEnrichment holds the per-hex details that live in Postgres rather
// than in the readsb feed.
type aircraftEnrichment struct {
	Registration     *string
	IcaoType         *string
	RegisteredOwner  *string
	AirlineName      *string
	OriginIata       *string
	OriginName       *string
	DestinationIata  *string
	DestinationName  *string
	InterestingGroup *string
}

// buildCurrentSightings turns a readsb snapshot plus database enrichment into
// the rows the API serves, nearest first.
//
// distanceKm is injected rather than calling getDistance directly so this stays
// testable without LAT/LON set in the environment.
func buildCurrentSightings(
	aircraft []Aircraft,
	enrichment map[string]aircraftEnrichment,
	nowEpoch float64,
	distanceKm func(lat, lon float64) float64,
) []CurrentSighting {

	now := time.Unix(int64(nowEpoch), 0)
	sightings := make([]CurrentSighting, 0, len(aircraft))

	for _, a := range aircraft {
		e := enrichment[a.Hex]

		sightings = append(sightings, CurrentSighting{
			Hex:              a.Hex,
			Flight:           a.Flight,
			Registration:     firstNonEmpty(a.R, stringValue(e.Registration)),
			Type:             firstNonEmpty(a.T, stringValue(e.IcaoType)),
			TypeDescription:  nilIfEmpty(a.Desc),
			Airline:          e.AirlineName,
			Operator:         e.RegisteredOwner,
			Altitude:         a.AltBaro,
			GroundSpeed:      a.Gs,
			Track:            a.Track,
			DistanceKm:       math.Round(distanceKm(a.Lat, a.Lon)*100) / 100,
			OriginIata:       e.OriginIata,
			OriginName:       e.OriginName,
			DestinationIata:  e.DestinationIata,
			DestinationName:  e.DestinationName,
			InterestingGroup: e.InterestingGroup,
			LastSeen:         now.Add(-time.Duration(a.Seen * float64(time.Second))),
		})
	}

	sort.SliceStable(sightings, func(i, j int) bool {
		return sightings[i].DistanceKm < sightings[j].DistanceKm
	})

	return sightings
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func nilIfEmpty(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// currentSightingsStore holds the payload the 2s ticker builds so the API
// goroutine can serve it without touching the database per request.
type currentSightingsStore struct {
	mu          sync.RWMutex
	aircraft    []CurrentSighting
	generatedAt time.Time
}

var currentSightings = &currentSightingsStore{}

func (s *currentSightingsStore) replace(aircraft []CurrentSighting, generatedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.aircraft = aircraft
	s.generatedAt = generatedAt
}

// snapshot returns a copy so callers can range over the result while the
// ticker replaces the store's contents.
func (s *currentSightingsStore) snapshot() ([]CurrentSighting, time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	aircraft := make([]CurrentSighting, len(s.aircraft))
	copy(aircraft, s.aircraft)
	return aircraft, s.generatedAt
}
