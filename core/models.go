package main

import (
	"database/sql"
	"strings"
	"time"
)

type Response struct {
	Now      float64    `json:"now"`
	Messages int        `json:"messages"`
	Aircraft []Aircraft `json:"aircraft"`
}
type Aircraft struct {
	Id                  int
	Hex                 string  `json:"hex"`
	Type                string  `json:"type"`
	Flight              string  `json:"flight"`
	R                   string  `json:"r"`
	T                   string  `json:"t"`
	Desc                string  `json:"desc"`
	AltBaro             int     `json:"alt_baro"`
	AltGeom             int     `json:"alt_geom"`
	Gs                  float64 `json:"gs"`
	Ias                 int     `json:"ias"`
	Tas                 int     `json:"tas"`
	Track               float64 `json:"track"`
	BaroRate            int     `json:"baro_rate"`
	NavQnh              float64 `json:"nav_qnh"`
	NavAltitudeMcp      int     `json:"nav_altitude_mcp"`
	NavHeading          float64 `json:"nav_heading"`
	Lat                 float64 `json:"lat"`
	Lon                 float64 `json:"lon"`
	Nic                 int     `json:"nic"`
	Rc                  int     `json:"rc"`
	SeenPos             float64 `json:"seen_pos"`
	RDst                float64 `json:"r_dst"`
	RDir                float64 `json:"r_dir"`
	Version             int     `json:"version"`
	NicBaro             int     `json:"nic_baro"`
	NacP                int     `json:"nac_p"`
	NacV                int     `json:"nac_v"`
	Sil                 int     `json:"sil"`
	SilType             string  `json:"sil_type"`
	Alert               int     `json:"alert"`
	Spi                 int     `json:"spi"`
	Mlat                []any   `json:"mlat"`
	Tisb                []any   `json:"tisb"`
	Messages            int     `json:"messages"`
	Seen                float64 `json:"seen"`
	Rssi                int     `json:"rssi"`
	DbFlags             int     `json:"dbFlags"`
	Squawk              string  `json:"squawk"`
	Category            string  `json:"category"`
	FirstSeen           time.Time
	FirstSeenEpoch      float64
	LastSeen            time.Time
	LastSeenEpoch       float64
	LastSeenLat         sql.NullFloat64
	LastSeenLon         sql.NullFloat64
	LastSeenDistance    sql.NullFloat64
	DestinationDistance sql.NullFloat64
	RouteAttempts       int
	LowestProcessed     bool
	HighestProcessed    bool
	FastestProcessed    bool
	SlowestProcessed    bool

	// Fields used by the distance leaderboards (see stats-distance.go).
	// Populated via a LEFT JOIN against route_data, so they may be invalid
	// (sql.NullString / sql.NullFloat64 not Valid) when no route was matched.
	FurthestFlownProcessed bool
	MostRemainingProcessed bool
	LongestRouteProcessed  bool
	OriginIcaoCode         sql.NullString
	OriginIataCode         sql.NullString
	OriginLat              sql.NullFloat64
	OriginLon              sql.NullFloat64
	DestinationIcaoCode    sql.NullString
	DestinationIataCode    sql.NullString
	DestinationLat         sql.NullFloat64
	DestinationLon         sql.NullFloat64
}

type InterestingAircraft struct {
	Icao         string
	Registration sql.NullString
	Operator     sql.NullString
	Type         sql.NullString
	IcaoType     sql.NullString
	Group        sql.NullString
	Tag1         sql.NullString
	Tag2         sql.NullString
	Tag3         sql.NullString
	Category     sql.NullString
	Link         sql.NullString
	ImageLink1   sql.NullString
	ImageLink2   sql.NullString
	ImageLink3   sql.NullString
	ImageLink4   sql.NullString
	Hex          string
	Flight       string
	R            string
	T            string
	AltBaro      int
	AltGeom      int
	Gs           float64
	Ias          int
	Tas          int
	Track        float64
	BaroRate     int
	Lat          float64
	Lon          float64
	Alert        int
	DbFlags      int
	Seen         time.Time
	SeenEpoch    float64
}

// Flight string sometimes has trailing whitespace
func (r *Response) TrimFlightStrings() {
	for i := range r.Aircraft {
		r.Aircraft[i].Flight = strings.TrimSpace(r.Aircraft[i].Flight)
	}
}

type RegistrationInfo struct {
	Response struct {
		Aircraft struct {
			Type                            string `json:"type"`
			IcaoType                        string `json:"icao_type"`
			Manufacturer                    string `json:"manufacturer"`
			ModeS                           string `json:"mode_s"`
			Registration                    string `json:"registration"`
			RegisteredOwnerCountryIsoName   string `json:"registered_owner_country_iso_name"`
			RegisteredOwnerCountryName      string `json:"registered_owner_country_name"`
			RegisteredOwnerOperatorFlagCode string `json:"registered_owner_operator_flag_code"`
			RegisteredOwner                 string `json:"registered_owner"`
			URLPhoto                        any    `json:"url_photo"`
			URLPhotoThumbnail               any    `json:"url_photo_thumbnail"`
		} `json:"aircraft"`
	} `json:"response"`
}

type RouteInfo struct {
	AirportCodesIata string `json:"_airport_codes_iata"`
	Airports         []struct {
		AltFeet     float64 `json:"alt_feet"`
		AltMeters   float64 `json:"alt_meters"`
		CountryIso2 string  `json:"countryiso2"`
		Iata        string  `json:"iata"`
		Icao        string  `json:"icao"`
		Lat         float64 `json:"lat"`
		Location    string  `json:"location"`
		Lon         float64 `json:"lon"`
		Name        string  `json:"name"`
	} `json:"_airports"`
	AirlineCode  string `json:"airline_code"`
	AirportCodes string `json:"airport_codes"`
	Callsign     string `json:"callsign"`
	Number       string `json:"number"`
	Plausible    bool   `json:"plausible"`
}

type RouteAPIPlane struct {
	Callsign string  `json:"callsign"`
	Lat      float64 `json:"lat"`
	Lng      float64 `json:"lng"`
}

type RouteAPIRequest struct {
	Planes []RouteAPIPlane `json:"planes"`
}

type ChartPoint struct {
	X time.Time `json:"x"`
	Y float64   `json:"y"`
}

type ChartSeries struct {
	ID     string       `json:"id"`
	Label  string       `json:"label"`
	Unit   string       `json:"unit,omitempty"`
	Points []ChartPoint `json:"points"`
}

type ChartXAxisMeta struct {
	Type     string `json:"type"`
	Timezone string `json:"timezone,omitempty"`
	Unit     string `json:"unit,omitempty"`
}

type ChartMeta struct {
	GeneratedAt time.Time `json:"generated_at"`
}

type ChartResponse struct {
	Series []ChartSeries  `json:"series"`
	X      ChartXAxisMeta `json:"x"`
	Meta   ChartMeta      `json:"meta"`
}
