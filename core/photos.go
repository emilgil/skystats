package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// planespottersBaseURL is the public Planespotters photo API, keyed by ICAO hex
// — the one identifier every sighting carries. It replaced the photo URLs that
// come with the adsbdb registration data: those point at airport-data.com paths
// that have since 404'd wholesale, and Apprise refuses a notification whose
// attachment it cannot fetch.
const planespottersBaseURL = "https://api.planespotters.net/pub/photos/hex/"

// How long a lookup result is reused. A hit keeps for a day — an airframe's
// photo does not change. A miss keeps for less, because a photo can be uploaded
// at any time, but long enough that a camera-shy aircraft cannot turn every one
// of its sightings into an outbound request.
const (
	photoHitTTL  = 24 * time.Hour
	photoMissTTL = 6 * time.Hour
)

type photoCacheEntry struct {
	url       string
	expiresAt time.Time
}

// photoLookup resolves an ICAO hex to a photo URL fit for an Apprise
// attachment, memoising hits and misses alike.
//
// Safe for concurrent use: watch notifications run several at a time. The
// mutex covers the map only, never the HTTP call, so one slow request cannot
// hold up every other lookup. Two goroutines racing for the same hex will both
// fetch it, which costs one redundant request and is cheaper than serialising
// all of them.
type photoLookup struct {
	baseURL string
	client  *http.Client
	hitTTL  time.Duration
	missTTL time.Duration

	mu sync.Mutex
	// cache grows by one entry per distinct aircraft ever notified about, and
	// entries are replaced in place once stale, so it is bounded by the size of
	// the fleet the receiver can see.
	cache map[string]photoCacheEntry
}

func newPhotoLookup() *photoLookup {
	return &photoLookup{
		baseURL: planespottersBaseURL,
		client:  &http.Client{Timeout: 5 * time.Second},
		hitTTL:  photoHitTTL,
		missTTL: photoMissTTL,
		cache:   map[string]photoCacheEntry{},
	}
}

// photoURL returns a photo for hex, or "" when there is none.
//
// Every failure — no photo, a refused request, an unparseable response —
// returns "" rather than an error. The picture decorates a notification, and no
// caller has anything better to do with a failure than send the message
// without one.
func (p *photoLookup) photoURL(hex string) string {
	hex = strings.ToLower(strings.TrimSpace(hex))
	if hex == "" {
		return ""
	}

	if url, ok := p.cached(hex); ok {
		return url
	}

	url := p.fetch(hex)
	ttl := p.hitTTL
	if url == "" {
		ttl = p.missTTL
	}
	p.store(hex, url, ttl)
	return url
}

func (p *photoLookup) cached(hex string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.cache[hex]
	if !ok || time.Now().After(entry.expiresAt) {
		return "", false
	}
	return entry.url, true
}

func (p *photoLookup) store(hex, url string, ttl time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cache[hex] = photoCacheEntry{url: url, expiresAt: time.Now().Add(ttl)}
}

// planespottersResponse is the slice of the API's payload we use. The API
// answers a refused request with HTTP 200 and an error string rather than a
// status code, so error is read alongside the photos: without it a rejection
// looks exactly like an aircraft nobody has photographed.
type planespottersResponse struct {
	Error  string `json:"error"`
	Photos []struct {
		ThumbnailLarge struct {
			Src string `json:"src"`
		} `json:"thumbnail_large"`
	} `json:"photos"`
}

func (p *photoLookup) fetch(hex string) string {
	req, err := http.NewRequest(http.MethodGet, p.baseURL+hex, nil)
	if err != nil {
		log.Debug().Err(err).Msgf("photoURL() - cannot build request for %s", hex)
		return ""
	}
	// Planespotters turns away requests carrying a generic library User-Agent,
	// so identify the application and how to reach whoever runs it.
	req.Header.Set("User-Agent", fmt.Sprintf("Skystats/%s (+https://github.com/emilgil/skystats)", version))
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		log.Debug().Err(err).Msgf("photoURL() - request failed for %s", hex)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Debug().Msgf("photoURL() - planespotters returned status %d for %s", resp.StatusCode, hex)
		return ""
	}

	var payload planespottersResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		log.Debug().Err(err).Msgf("photoURL() - cannot decode response for %s", hex)
		return ""
	}
	if payload.Error != "" {
		log.Warn().Msgf("photoURL() - planespotters refused the request for %s: %s", hex, payload.Error)
		return ""
	}
	if len(payload.Photos) == 0 {
		return ""
	}
	return payload.Photos[0].ThumbnailLarge.Src
}
