package main

import (
	"context"
	"math"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// CurrentSighting is one row of the Current Sightings table: an aircraft the
// receiver can see right now. Live values come from the readsb snapshot.
// TypeDescription also comes straight from the readsb feed (its desc field)
// and has no database fallback. The remaining optional fields are joined in
// from Postgres and stay nil until the registration and route enrichment jobs
// have caught up with the aircraft.
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
// than in the readsb feed. Consumed by both Current Sightings and the watch
// engine, so it carries fields neither uses on its own.
type aircraftEnrichment struct {
	Registration     *string
	IcaoType         *string
	AircraftType     *string
	Manufacturer     *string
	CountryName      *string
	RegisteredOwner  *string
	PhotoURL         *string
	AirlineName      *string
	AirlineIcao      *string
	AirlineIata      *string
	OriginIata       *string
	OriginIcao       *string
	OriginName       *string
	DestinationIata  *string
	DestinationIcao  *string
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

// fetchAircraftEnrichment looks up the slower-moving details for a whole
// snapshot in one round trip, keyed by hex.
func fetchAircraftEnrichment(pg *postgres, hexes []string, flights []string) (map[string]aircraftEnrichment, error) {

	query := `
		SELECT s.hex,
		       reg.registration,
		       reg.icao_type,
		       reg.type,
		       reg.manufacturer,
		       reg.registered_owner_country_name,
		       reg.registered_owner,
		       reg.url_photo,
		       rt.airline_name,
		       rt.airline_icao,
		       rt.airline_iata,
		       rt.origin_iata_code,
		       rt.origin_icao_code,
		       rt.origin_name,
		       rt.destination_iata_code,
		       rt.destination_icao_code,
		       rt.destination_name,
		       ia."group"
		FROM unnest($1::text[], $2::text[]) AS s(hex, flight)
		LEFT JOIN registration_data reg ON reg.mode_s = s.hex
		LEFT JOIN LATERAL (
			SELECT airline_name, airline_icao, airline_iata,
			       origin_iata_code, origin_icao_code, origin_name,
			       destination_iata_code, destination_icao_code, destination_name
			FROM route_data
			WHERE route_callsign = s.flight
			LIMIT 1
		) rt ON true
		LEFT JOIN LATERAL (
			SELECT "group"
			FROM interesting_aircraft
			WHERE icao = UPPER(s.hex)
			LIMIT 1
		) ia ON true`

	rows, err := pg.db.Query(context.Background(), query, hexes, flights)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	enrichment := make(map[string]aircraftEnrichment, len(hexes))

	for rows.Next() {
		var hex string
		var e aircraftEnrichment

		err := rows.Scan(
			&hex,
			&e.Registration,
			&e.IcaoType,
			&e.AircraftType,
			&e.Manufacturer,
			&e.CountryName,
			&e.RegisteredOwner,
			&e.PhotoURL,
			&e.AirlineName,
			&e.AirlineIcao,
			&e.AirlineIata,
			&e.OriginIata,
			&e.OriginIcao,
			&e.OriginName,
			&e.DestinationIata,
			&e.DestinationIcao,
			&e.DestinationName,
			&e.InterestingGroup,
		)
		if err != nil {
			log.Error().Err(err).Msg("fetchAircraftEnrichment() - error scanning rows")
			continue
		}

		enrichment[hex] = e
	}

	return enrichment, rows.Err()
}

// enrichAircraftSnapshot fetches the Postgres-side details for one readsb
// snapshot, once per tick, for every consumer that needs them. On error it
// returns an empty map so callers degrade to live-only data rather than
// freezing on stale values.
func enrichAircraftSnapshot(pg *postgres, aircraft []Aircraft) map[string]aircraftEnrichment {

	if len(aircraft) == 0 {
		return map[string]aircraftEnrichment{}
	}

	hexes := make([]string, 0, len(aircraft))
	flights := make([]string, 0, len(aircraft))
	for _, a := range aircraft {
		hexes = append(hexes, a.Hex)
		flights = append(flights, a.Flight)
	}

	enrichment, err := fetchAircraftEnrichment(pg, hexes, flights)
	if err != nil {
		log.Error().Err(err).Msg("enrichAircraftSnapshot() - unable to fetch enrichment")
		return map[string]aircraftEnrichment{}
	}

	return enrichment
}

// refreshCurrentSightings rebuilds the Current Sightings payload from the
// snapshot the ingest ticker just processed.
func refreshCurrentSightings(nowEpoch float64, aircraft []Aircraft, enrichment map[string]aircraftEnrichment) {

	// A non-JSON readsb response (e.g. an HTML error page) leaves nowEpoch at
	// its zero value, since json.Unmarshal's error is discarded upstream. Bail
	// out rather than stamping the store with a 1970 timestamp and wiping the
	// aircraft list — let it age like any other outage instead.
	if nowEpoch == 0 {
		return
	}

	// Use the local clock rather than readsb's nowEpoch: the frontend compares
	// generatedAt against the viewer's Date.now(), so deriving it from the
	// feeder's (potentially unsynced) clock would make staleness detection
	// unreliable.
	generatedAt := time.Now()

	if len(aircraft) == 0 {
		currentSightings.replace([]CurrentSighting{}, generatedAt)
		return
	}

	sightings := buildCurrentSightings(aircraft, enrichment, nowEpoch, func(lat, lon float64) float64 {
		return *getDistance([]float64{lon, lat})
	})

	currentSightings.replace(sightings, generatedAt)
}

// getCurrentSightings serves the payload the ticker has already assembled.
// generated_at is null until the first tick has completed, and lets the
// frontend tell "nothing in range" apart from "the feed stopped answering".
func (s *APIServer) getCurrentSightings(c *gin.Context) {

	aircraft, generatedAt := currentSightings.snapshot()

	var generated *time.Time
	if !generatedAt.IsZero() {
		generated = &generatedAt
	}

	c.JSON(http.StatusOK, gin.H{
		"generated_at": generated,
		"aircraft":     aircraft,
	})
}
