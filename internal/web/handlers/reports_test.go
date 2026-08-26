package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"playlistsync/internal/model"
)

func writeReportsFixtures(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "rock_report.json"),
		`{"title":"Rock Hits","direction":"spotify_to_ytmusic","totalSourceTracks":4,"addedTracks":3,"skippedTracks":1}`)
	mustWrite(t, filepath.Join(dir, "jazz_report.json"),
		`{"title":"Jazz","direction":"spotify_to_ytmusic","totalSourceTracks":2,"addedTracks":2,"skippedTracks":0}`)
	// Non-report files must be ignored.
	mustWrite(t, filepath.Join(dir, "readme.txt"), "not a report")
	mustWrite(t, filepath.Join(dir, "playlist_result.json"), "no _report suffix")
	return dir
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListReports(t *testing.T) {
	dir := writeReportsFixtures(t)
	mux := testMux(t, HandlerConfig{ReportsDir: dir})

	w := doReq(t, mux, "GET", "/api/v1/reports", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", w.Code, w.Body.String())
	}
	var reports []reportMeta
	if err := decodeJSON(t, w, &reports); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(reports) != 2 {
		t.Fatalf("reports = %d, want 2 (only *_report.json)", len(reports))
	}
	ids := map[string]bool{}
	for _, r := range reports {
		ids[r.ID] = true
		if !strings.HasSuffix(r.Name, "_report.json") {
			t.Errorf("unexpected report name %q", r.Name)
		}
	}
	if !ids["rock"] || !ids["jazz"] {
		t.Errorf("report ids wrong: %v", ids)
	}
}

func TestListReportsMissingDir(t *testing.T) {
	mux := testMux(t, HandlerConfig{ReportsDir: filepath.Join(t.TempDir(), "nope")})
	w := doReq(t, mux, "GET", "/api/v1/reports", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 empty list (body %s)", w.Code, w.Body.String())
	}
	var reports []reportMeta
	if err := decodeJSON(t, w, &reports); err != nil {
		t.Fatalf("decode: %v (body %s)", err, w.Body.String())
	}
	if len(reports) != 0 {
		t.Errorf("expected empty list, got %+v", reports)
	}
}

func TestExportReportJSON(t *testing.T) {
	dir := writeReportsFixtures(t)
	mux := testMux(t, HandlerConfig{ReportsDir: dir})

	w := doReq(t, mux, "GET", "/api/v1/reports/rock/export", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content-type = %q, want json", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "rock_report.json") {
		t.Errorf("content-disposition = %q", cd)
	}
	var res model.SyncResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode exported json: %v", err)
	}
	if res.Direction != "spotify_to_ytmusic" || res.AddedTracks != 3 {
		t.Errorf("exported result wrong: %+v", res)
	}
}

func TestExportReportMarkdown(t *testing.T) {
	dir := writeReportsFixtures(t)
	mux := testMux(t, HandlerConfig{ReportsDir: dir})

	w := doReq(t, mux, "GET", "/api/v1/reports/rock/export?format=markdown", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "# Migration Report") {
		t.Errorf("markdown header missing:\n%s", body)
	}
	if !strings.Contains(body, "Rock Hits") {
		t.Errorf("title missing in markdown:\n%s", body)
	}
}

func TestExportReportErrors(t *testing.T) {
	dir := writeReportsFixtures(t)
	mux := testMux(t, HandlerConfig{ReportsDir: dir})

	tests := []struct {
		name   string
		target string
		want   int
	}{
		{"unknown id", "/api/v1/reports/ghost/export", http.StatusNotFound},
		{"traversal encoded slash", "/api/v1/reports/..%2Fetc/export", http.StatusNotFound},
		{"traversal dotdot", "/api/v1/reports/..%2f..%2fetc/export", http.StatusNotFound},
		{"bad format", "/api/v1/reports/rock/export?format=xml", http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := doReq(t, mux, "GET", tc.target, "")
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestResolvedReportPathTraversalRejected(t *testing.T) {
	cfg := HandlerConfig{ReportsDir: "reports"}
	tests := []struct {
		id string
		ok bool
	}{
		{"rock", true},
		{"a_b", true},
		{"..", false},
		{".", false},
		{"", false},
		{"a/b", false},
		{"a\\b", false},
		{"..%2Fetc", true}, // raw id is a plain filename (no literal slash); URL-decoded traversal is caught by the %-decoding at the handler boundary
		{"sub/rock", false},
		{"..%2f..%2fetc", true}, // same: literal-percent form is a safe filename; decoded form carries "/" and is rejected
		{"\x00", true},          // NUL never traverses; the OS/read fails safely
		{"../etc", false},
		{"..\\etc", false},
	}
	for _, tc := range tests {
		_, ok := resolvedReportPath(cfg, tc.id)
		if ok != tc.ok {
			t.Errorf("resolvedReportPath(%q) = %v, want %v", tc.id, ok, tc.ok)
		}
	}
}

func TestRenderMarkdown(t *testing.T) {
	res := &model.SyncResult{
		Title: "T", Direction: "spotify_to_ytmusic", TotalSourceTracks: 3,
		AddedTracks: 2, SkippedTracks: 1, PlaylistURL: "https://music.youtube.com/playlist?list=P",
		Skipped: []model.SkippedTrack{{Title: "Low conf", Artists: []string{"X"}, Reason: "low confidence"}},
	}
	md := renderMarkdown("t", res)
	if !strings.Contains(md, "Low conf") || !strings.Contains(md, "low confidence") {
		t.Errorf("skipped section missing from markdown:\n%s", md)
	}
	if !strings.Contains(md, "2") || !strings.Contains(md, "P") {
		t.Errorf("counts/url missing:\n%s", md)
	}
}

func TestListReportsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old_report.json")
	new := filepath.Join(dir, "new_report.json")
	mustWrite(t, old, `{"title":"old"}`)
	mustWrite(t, new, `{"title":"new"}`)
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	mux := testMux(t, HandlerConfig{ReportsDir: dir})
	w := doReq(t, mux, "GET", "/api/v1/reports", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", w.Code, w.Body.String())
	}
	var reports []reportMeta
	if err := decodeJSON(t, w, &reports); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(reports) != 2 {
		t.Fatalf("reports = %d, want 2", len(reports))
	}
	if reports[0].ID != "new" || reports[1].ID != "old" {
		t.Errorf("expected newest-first order, got %+v", reports)
	}
	if !reports[0].ModifiedAt.After(reports[1].ModifiedAt) {
		t.Errorf("ModifiedAt not ordered: %v vs %v", reports[0].ModifiedAt, reports[1].ModifiedAt)
	}
}
