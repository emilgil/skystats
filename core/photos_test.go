package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// planespottersStub serves a fixed body and counts the requests it received, so
// a test can prove a second lookup was answered from the cache. The recorded
// User-Agent is kept because Planespotters rejects some generic ones.
type planespottersStub struct {
	server    *httptest.Server
	requests  atomic.Int32
	userAgent atomic.Value // string
}

func newPlanespottersStub(t *testing.T, status int, body string) *planespottersStub {
	t.Helper()
	s := &planespottersStub{}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		s.userAgent.Store(r.UserAgent())
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(s.server.Close)
	return s
}

func (s *planespottersStub) lookup() *photoLookup {
	p := newPhotoLookup()
	p.baseURL = s.server.URL + "/pub/photos/hex/"
	return p
}

const onePhotoBody = `{"photos":[{"id":"1952979",
	"thumbnail":{"src":"https://t.plnspttrs.net/38761/1952979_t.jpg"},
	"thumbnail_large":{"src":"https://t.plnspttrs.net/38761/1952979_280.jpg"},
	"link":"https://www.planespotters.net/photo/1952979/se-rok",
	"photographer":"Martin Oswald"}]}`

func TestPhotoURLReturnsTheLargeThumbnail(t *testing.T) {
	stub := newPlanespottersStub(t, http.StatusOK, onePhotoBody)

	got := stub.lookup().photoURL("4AC9EB")

	want := "https://t.plnspttrs.net/38761/1952979_280.jpg"
	if got != want {
		t.Fatalf("photoURL() = %q, want %q", got, want)
	}
}

func TestPhotoURLLooksUpTheHexInLowerCase(t *testing.T) {
	var path atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path.Store(r.URL.Path)
		fmt.Fprint(w, onePhotoBody)
	}))
	defer server.Close()

	p := newPhotoLookup()
	p.baseURL = server.URL + "/pub/photos/hex/"
	p.photoURL("  4AC9EB ")

	if got := path.Load(); got != "/pub/photos/hex/4ac9eb" {
		t.Fatalf("requested path = %v, want /pub/photos/hex/4ac9eb", got)
	}
}

func TestPhotoURLReturnsEmptyWhenThereAreNoPhotos(t *testing.T) {
	stub := newPlanespottersStub(t, http.StatusOK, `{"photos":[]}`)

	if got := stub.lookup().photoURL("abc123"); got != "" {
		t.Fatalf("photoURL() = %q, want empty", got)
	}
}

// Planespotters answers a rejected request with HTTP 200 and an error object,
// which decodes to zero photos and is easy to mistake for "this aircraft has no
// picture". The lookup still returns nothing — there is nothing to attach — but
// it must not treat the two cases as the same thing silently.
func TestPhotoURLReturnsEmptyWhenTheAPIReportsAnError(t *testing.T) {
	stub := newPlanespottersStub(t, http.StatusOK,
		`{"error":"Generic library User-Agent strings are not accepted"}`)

	if got := stub.lookup().photoURL("abc123"); got != "" {
		t.Fatalf("photoURL() = %q, want empty", got)
	}
}

func TestPhotoURLReturnsEmptyOnAnHTTPError(t *testing.T) {
	stub := newPlanespottersStub(t, http.StatusInternalServerError, "boom")

	if got := stub.lookup().photoURL("abc123"); got != "" {
		t.Fatalf("photoURL() = %q, want empty", got)
	}
}

func TestPhotoURLReturnsEmptyOnAnUnreachableHost(t *testing.T) {
	p := newPhotoLookup()
	p.baseURL = "http://127.0.0.1:1/pub/photos/hex/"

	if got := p.photoURL("abc123"); got != "" {
		t.Fatalf("photoURL() = %q, want empty", got)
	}
}

func TestPhotoURLSendsADescriptiveUserAgent(t *testing.T) {
	stub := newPlanespottersStub(t, http.StatusOK, onePhotoBody)

	stub.lookup().photoURL("abc123")

	ua, _ := stub.userAgent.Load().(string)
	if !strings.HasPrefix(ua, "Skystats/") {
		t.Fatalf("User-Agent = %q, want it to start with Skystats/", ua)
	}
	if !strings.Contains(ua, "https://github.com/emilgil/skystats") {
		t.Fatalf("User-Agent = %q, want it to carry a contact URL", ua)
	}
}

func TestPhotoURLServesASecondHitFromTheCache(t *testing.T) {
	stub := newPlanespottersStub(t, http.StatusOK, onePhotoBody)
	p := stub.lookup()

	first := p.photoURL("abc123")
	second := p.photoURL("abc123")

	if first == "" || first != second {
		t.Fatalf("cached lookup = %q, first = %q; want the same non-empty URL", second, first)
	}
	if n := stub.requests.Load(); n != 1 {
		t.Fatalf("made %d requests, want 1 — the second lookup should come from the cache", n)
	}
}

// A miss is cached too. Aircraft without a photo are the ones most likely to be
// asked for again and again, so not caching them would send every one of their
// sightings out to the network.
func TestPhotoURLServesASecondMissFromTheCache(t *testing.T) {
	stub := newPlanespottersStub(t, http.StatusOK, `{"photos":[]}`)
	p := stub.lookup()

	p.photoURL("abc123")
	p.photoURL("abc123")

	if n := stub.requests.Load(); n != 1 {
		t.Fatalf("made %d requests, want 1 — the second lookup should come from the cache", n)
	}
}

func TestPhotoURLRefetchesOnceTheCacheEntryHasExpired(t *testing.T) {
	stub := newPlanespottersStub(t, http.StatusOK, onePhotoBody)
	p := stub.lookup()
	p.hitTTL = -time.Second // already expired the moment it is stored

	p.photoURL("abc123")
	p.photoURL("abc123")

	if n := stub.requests.Load(); n != 2 {
		t.Fatalf("made %d requests, want 2 — an expired entry must be refetched", n)
	}
}

func TestPhotoURLDoesNotCallTheAPIForAnEmptyHex(t *testing.T) {
	stub := newPlanespottersStub(t, http.StatusOK, onePhotoBody)

	if got := stub.lookup().photoURL("   "); got != "" {
		t.Fatalf("photoURL() = %q, want empty", got)
	}
	if n := stub.requests.Load(); n != 0 {
		t.Fatalf("made %d requests, want 0", n)
	}
}

// Watch notifications run concurrently, so the cache is shared across
// goroutines. Run with -race.
func TestPhotoURLIsSafeForConcurrentUse(t *testing.T) {
	stub := newPlanespottersStub(t, http.StatusOK, onePhotoBody)
	p := stub.lookup()

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p.photoURL(fmt.Sprintf("abc%02d", i%4))
		}(i)
	}
	wg.Wait()
}

// A NotificationService built without a lookup — every unit test in this package
// does exactly that — must not panic when it asks for a photo.
func TestNotificationServicePhotoURLToleratesAMissingLookup(t *testing.T) {
	n := &NotificationService{}

	if got := n.photoURL("abc123"); got != "" {
		t.Fatalf("photoURL() = %q, want empty", got)
	}
}
