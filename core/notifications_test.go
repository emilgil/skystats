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
	title, body := buildRecordMessage("highest", best, 44200, true)
	if title != "🏆 New all-time record: Highest" {
		t.Errorf("title = %q", title)
	}
	for _, want := range []string{"Altitude: 45000 ft", "Previous: 44200 ft", "Aircraft: N12345 (B738)", "Callsign: SAS1"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q in:\n%s", want, body)
		}
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
