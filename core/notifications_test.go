package main

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestImproved(t *testing.T) {
	if !improved(100, 120, true) {
		t.Error("keepMax: 120 should beat 100")
	}
	if improved(100, 100, true) {
		t.Error("keepMax: equal is not an improvement")
	}
	if !improved(500, 300, false) {
		t.Error("keepMin: 300 should beat 500")
	}
	if improved(300, 500, false) {
		t.Error("keepMin: 500 does not beat 300")
	}
}

func TestIsNewRecordHolder_AnotherFlightTakingTheRecord(t *testing.T) {
	takeoff := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	old := recordBest{Hex: "4cad94", FirstSeen: takeoff, MetricValue: 520}
	new := recordBest{Hex: "48415f", FirstSeen: takeoff.Add(time.Hour), MetricValue: 560}

	if !isNewRecordHolder(old, new, true) {
		t.Error("a different flight beating the record is a new record holder")
	}
}

func TestIsNewRecordHolder_SameFlightImprovingOnItself(t *testing.T) {
	// fastest/highest now rewrite a flight every tick for as long as it is
	// still airborne. Without this guard a single record-breaking flight would
	// announce itself again every 120s for the rest of its climb.
	takeoff := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	old := recordBest{Hex: "4cad94", FirstSeen: takeoff, MetricValue: 560}
	new := recordBest{Hex: "4cad94", FirstSeen: takeoff, MetricValue: 575}

	if isNewRecordHolder(old, new, true) {
		t.Error("the same flight session improving on itself is not a new record holder")
	}
}

func TestIsNewRecordHolder_SameAircraftOnALaterFlight(t *testing.T) {
	// Same airframe, new session: that is a genuine new record and should
	// announce, so the guard has to key on the session and not the hex alone.
	old := recordBest{Hex: "4cad94", FirstSeen: time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC), MetricValue: 560}
	new := recordBest{Hex: "4cad94", FirstSeen: time.Date(2026, 8, 6, 14, 0, 0, 0, time.UTC), MetricValue: 575}

	if !isNewRecordHolder(old, new, true) {
		t.Error("a later flight by the same airframe is a new record holder")
	}
}

func TestIsNewRecordHolder_NotAnImprovement(t *testing.T) {
	takeoff := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	old := recordBest{Hex: "4cad94", FirstSeen: takeoff, MetricValue: 560}
	new := recordBest{Hex: "48415f", FirstSeen: takeoff.Add(time.Hour), MetricValue: 540}

	if isNewRecordHolder(old, new, true) {
		t.Error("a slower flight has not taken the record")
	}
}

func TestBuildInterestingMessage(t *testing.T) {
	dist := 12.4
	a := InterestingAircraft{
		Group:      sql.NullString{String: "Mil", Valid: true},
		R:          "SE-ABC",
		Flight:     "SVK123",
		Operator:   sql.NullString{String: "Swedish Air Force", Valid: true},
		Type:       sql.NullString{String: "JAS 39 Gripen", Valid: true},
		Link:       sql.NullString{String: "https://example/x", Valid: true},
		ImageLink1: sql.NullString{String: "https://img/1.jpg", Valid: true},
		AltBaro:    25000,
		Gs:         420,
	}
	title, body, attach := buildInterestingMessage(a, &dist, "ARN", "LHR")

	if title != "✈️ Military: SE-ABC" {
		t.Errorf("title = %q", title)
	}
	for _, want := range []string{
		"Type: JAS 39 Gripen", "Operator: Swedish Air Force", "Callsign: SVK123",
		"Altitude: 25000 ft", "Speed: 420 kt", "Distance: 12 km",
		"Route: ARN → LHR", "Link: https://example/x",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q in:\n%s", want, body)
		}
	}
	if attach != "https://img/1.jpg" {
		t.Errorf("attach = %q", attach)
	}
}

func TestBuildInterestingMessageOmitsMissing(t *testing.T) {
	a := InterestingAircraft{
		Group: sql.NullString{String: "Civ", Valid: true},
		Hex:   "abc123",
	}
	title, body, attach := buildInterestingMessage(a, nil, "", "")
	if title != "✈️ Civilian: abc123" {
		t.Errorf("title = %q", title)
	}
	if strings.Contains(body, "Route:") || strings.Contains(body, "Distance:") {
		t.Errorf("body should omit unknown fields:\n%s", body)
	}
	if attach != "" {
		t.Errorf("attach should be empty, got %q", attach)
	}
}

func TestBuildRecordMessage(t *testing.T) {
	best := recordBest{Registration: "N12345", Type: "B738", Flight: "SAS1", MetricValue: 45000}
	title, body := buildRecordMessage("highest", best, 44200, true, "", "")
	if title != "🏆 New all-time record: Highest" {
		t.Errorf("title = %q", title)
	}
	for _, want := range []string{"Altitude: 45000 ft", "Previous: 44200 ft", "Aircraft: N12345 (B738)", "Callsign: SAS1"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Route") {
		t.Errorf("body should omit the route when there is none:\n%s", body)
	}
}

func TestBuildRecordMessageIncludesTheRoute(t *testing.T) {
	best := recordBest{Registration: "B-2091", Type: "B77L", Flight: "CAO1008", MetricValue: 578}
	_, body := buildRecordMessage("fastest", best, 574, true, "PVG", "PIK")
	if !strings.Contains(body, "Route: PVG → PIK") {
		t.Errorf("body missing the route:\n%s", body)
	}
}

func TestBuildRecordMessageOmitsAHalfRoute(t *testing.T) {
	best := recordBest{Registration: "B-2091", Flight: "CAO1008", MetricValue: 578}
	_, body := buildRecordMessage("fastest", best, 574, true, "PVG", "")
	if strings.Contains(body, "Route") {
		t.Errorf("a route with only one end should not be printed:\n%s", body)
	}
}

func TestBuildWatchMessageMarksANotificationSentAfterTheAircraftLeft(t *testing.T) {
	_, body := buildWatchMessage("Above 40,000 ft", watchSubject{Hex: "78026e", Callsign: "CAO1160"}, true)
	if !strings.Contains(body, "Aircraft has left coverage") {
		t.Errorf("body should say the aircraft is gone:\n%s", body)
	}
}

func TestBuildWatchMessageOmitsTheMarkerWhileTheAircraftIsStillInRange(t *testing.T) {
	_, body := buildWatchMessage("Above 40,000 ft", watchSubject{Hex: "78026e", Callsign: "CAO1160"}, false)
	if strings.Contains(body, "left coverage") {
		t.Errorf("body should not claim the aircraft is gone:\n%s", body)
	}
}

func TestSendPostsStatefulPayload(t *testing.T) {
	var gotPath, gotBody, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := &NotificationService{client: &http.Client{Timeout: 5 * time.Second}}
	status, err := n.send(srv.URL, "skystats", apprisePayload{Title: "T", Body: "B"})
	if err != nil {
		t.Fatalf("send error: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("status = %d", status)
	}
	if gotPath != "/notify/skystats" {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.HasPrefix(gotCT, "application/json") {
		t.Errorf("content-type = %q", gotCT)
	}
	var p apprisePayload
	if err := json.Unmarshal([]byte(gotBody), &p); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, gotBody)
	}
	if p.Type != "info" || p.Format != "markdown" {
		t.Errorf("type/format = %q/%q", p.Type, p.Format)
	}
}

func TestSendRejectsMissingConfig(t *testing.T) {
	n := &NotificationService{client: &http.Client{Timeout: time.Second}}
	if _, err := n.send("", "key", apprisePayload{Body: "b"}); err == nil {
		t.Error("expected error for empty url")
	}
	if _, err := n.send("http://x", "", apprisePayload{Body: "b"}); err == nil {
		t.Error("expected error for empty key")
	}
}

func TestBuildWatchMessageUsesRegistrationInTheTitle(t *testing.T) {
	s := watchSubject{
		Hex: "4ca7b5", Callsign: "SAS1234", Registration: "SE-RTM", TypeCode: "B38M",
		Model: "Boeing 737 MAX 8", Airline: "Scandinavian Airlines",
		Origin: []string{"ESSA", "ARN"}, Destination: []string{"EKCH", "CPH"},
		AltitudeFt: 31000, HasAltitude: true, SpeedKt: 450, HasSpeed: true,
		DistanceKm: 42.5, HasPosition: true, Squawk: "2000",
	}

	title, body := buildWatchMessage("Boeing close by", s, false)

	if !strings.Contains(title, "Boeing close by") {
		t.Errorf("title should name the watch, got %q", title)
	}
	if !strings.Contains(title, "SE-RTM") {
		t.Errorf("title should identify the aircraft, got %q", title)
	}
	for _, want := range []string{"SAS1234", "B38M", "Boeing 737 MAX 8", "Scandinavian Airlines", "ARN", "CPH", "31000", "450", "42", "2000"} {
		if !strings.Contains(body, want) {
			t.Errorf("body is missing %q:\n%s", want, body)
		}
	}
}

func TestBuildWatchMessageFallsBackToHexAndOmitsMissingData(t *testing.T) {
	title, body := buildWatchMessage("Anything", watchSubject{Hex: "4ca7b5"}, false)

	if !strings.Contains(title, "4ca7b5") {
		t.Errorf("title should fall back to the hex, got %q", title)
	}
	for _, unwanted := range []string{"Altitude", "Speed", "Distance", "Route", "Squawk"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("body should omit %q when there is no data:\n%s", unwanted, body)
		}
	}
}

func TestBuildWatchMessageMarksFirstEverSighting(t *testing.T) {
	_, body := buildWatchMessage("New aircraft", watchSubject{Hex: "4ca7b5", FirstSeenEver: true}, false)

	if !strings.Contains(body, "First time") {
		t.Errorf("body should flag a first-ever sighting:\n%s", body)
	}
}

// attachRejectingServer stands in for the Apprise deployment observed in
// production: it refuses any notification carrying an attachment with the same
// 400 it returns for a dead image URL, and accepts the identical payload once
// the attachment is dropped. It records every payload it saw.
func attachRejectingServer(t *testing.T, alwaysFail bool) (*httptest.Server, *[]apprisePayload) {
	t.Helper()
	var seen []apprisePayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p apprisePayload
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &p); err != nil {
			t.Errorf("body not JSON: %v (%s)", err, b)
		}
		seen = append(seen, p)
		if alwaysFail || p.Attach != "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error": "Bad Attachment"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	return srv, &seen
}

func TestSendRetriesWithoutTheAttachmentWhenAppriseRejectsIt(t *testing.T) {
	srv, seen := attachRejectingServer(t, false)
	defer srv.Close()

	n := &NotificationService{client: &http.Client{Timeout: 5 * time.Second}}
	status, err := n.send(srv.URL, "skystats", apprisePayload{
		Title: "T", Body: "B", Attach: "https://example.invalid/photo.jpg",
	})

	if err != nil {
		t.Fatalf("the message should still be delivered without its picture, got error: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("status = %d want 200", status)
	}
	if len(*seen) != 2 {
		t.Fatalf("got %d requests want 2 (the rejected one, then the retry)", len(*seen))
	}
	if (*seen)[0].Attach == "" {
		t.Error("the first attempt should have carried the attachment")
	}
	if (*seen)[1].Attach != "" {
		t.Errorf("the retry must drop the attachment, got %q", (*seen)[1].Attach)
	}
	if (*seen)[1].Title != "T" || (*seen)[1].Body != "B" {
		t.Errorf("the retry must keep title and body, got %q / %q", (*seen)[1].Title, (*seen)[1].Body)
	}
}

func TestSendDoesNotRetryWhenThereWasNoAttachment(t *testing.T) {
	srv, seen := attachRejectingServer(t, true)
	defer srv.Close()

	n := &NotificationService{client: &http.Client{Timeout: 5 * time.Second}}
	if _, err := n.send(srv.URL, "skystats", apprisePayload{Title: "T", Body: "B"}); err == nil {
		t.Error("a 400 with nothing to drop is a real failure and must be reported")
	}
	if len(*seen) != 1 {
		t.Errorf("got %d requests want 1 — there is nothing to retry without", len(*seen))
	}
}

func TestSendReportsFailureWhenTheRetryAlsoFails(t *testing.T) {
	srv, seen := attachRejectingServer(t, true)
	defer srv.Close()

	n := &NotificationService{client: &http.Client{Timeout: 5 * time.Second}}
	if _, err := n.send(srv.URL, "skystats", apprisePayload{
		Title: "T", Body: "B", Attach: "https://example.invalid/photo.jpg",
	}); err == nil {
		t.Error("expected an error when the attachment was not the problem")
	}
	if len(*seen) != 2 {
		t.Errorf("got %d requests want 2 — one retry, not a loop", len(*seen))
	}
}
