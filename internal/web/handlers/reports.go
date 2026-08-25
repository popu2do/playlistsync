package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"playlistsync/internal/model"
)

// reportMeta is one entry in the GET /reports list.
type reportMeta struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
}

// RegisterReportHandlers mounts the report endpoints (spec 02 §2.4):
//
//	GET /api/v1/reports                  list local audit reports
//	GET /api/v1/reports/{id}/export      download a report (json|markdown)
func RegisterReportHandlers(mux *http.ServeMux, cfg HandlerConfig) {
	mux.HandleFunc("GET /api/v1/reports", listReports(cfg))
	mux.HandleFunc("GET /api/v1/reports/{id}/export", exportReport(cfg))
}

// listReports scans the reports directory for *_report.json artifacts and
// returns them newest-first.
func listReports(cfg HandlerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dir := cfg.ReportsDir
		if dir == "" {
			writeErrorJSON(w, http.StatusServiceUnavailable, "reports directory not configured")
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				writeJSON(w, http.StatusOK, []reportMeta{})
				return
			}
			writeErrorJSON(w, http.StatusInternalServerError, "read reports dir: "+sanitize(err.Error()))
			return
		}
		var reports []reportMeta
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasSuffix(name, "_report.json") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			id := strings.TrimSuffix(name, "_report.json")
			reports = append(reports, reportMeta{
				ID:         id,
				Name:       name,
				Size:       info.Size(),
				ModifiedAt: info.ModTime(),
			})
		}
		sort.Slice(reports, func(i, j int) bool {
			return reports[i].ModifiedAt.After(reports[j].ModifiedAt)
		})
		writeJSON(w, http.StatusOK, reports)
	}
}

// resolvedReportPath sanitizes a route id to an absolute report path within
// ReportsDir. It rejects traversal ("..", absolute paths, separators) so a
// crafted id can never escape the reports directory (zero-trace sandbox).
func resolvedReportPath(cfg HandlerConfig, id string) (string, bool) {
	if id == "" || id == "." || id == ".." {
		return "", false
	}
	if strings.ContainsAny(id, `/\`) || filepath.IsAbs(id) {
		return "", false
	}
	base := filepath.Base(id)
	if base != id {
		return "", false
	}
	dir := cfg.ReportsDir
	if dir == "" {
		return "", false
	}
	return filepath.Join(dir, id+"_report.json"), true
}

func exportReport(cfg HandlerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			writeErrorJSON(w, http.StatusBadRequest, "missing report id")
			return
		}
		path, ok := resolvedReportPath(cfg, id)
		if !ok {
			writeErrorJSON(w, http.StatusNotFound, "invalid report id")
			return
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				writeErrorJSON(w, http.StatusNotFound, "report not found: "+id)
				return
			}
			writeErrorJSON(w, http.StatusInternalServerError, "read report: "+sanitize(err.Error()))
			return
		}

		format := r.URL.Query().Get("format")
		if format == "" {
			format = "json"
		}
		switch format {
		case "json":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Disposition",
				fmt.Sprintf(`attachment; filename="%s_report.json"`, id))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
		case "markdown":
			var res model.SyncResult
			if err := json.Unmarshal(data, &res); err != nil {
				writeErrorJSON(w, http.StatusInternalServerError, "corrupted report: "+sanitize(err.Error()))
				return
			}
			md := renderMarkdown(id, &res)
			w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			w.Header().Set("Content-Disposition",
				fmt.Sprintf(`attachment; filename="%s_report.md"`, id))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(md))
		default:
			writeErrorJSON(w, http.StatusBadRequest, "unsupported format: "+format+" (json|markdown)")
		}
	}
}

// renderMarkdown converts a SyncResult into a compact markdown audit report.
func renderMarkdown(id string, res *model.SyncResult) string {
	var b strings.Builder
	b.WriteString("# Migration Report\n\n")
	b.WriteString(fmt.Sprintf("- **Playlist**: %s\n", res.Title))
	b.WriteString(fmt.Sprintf("- **Direction**: %s\n", res.Direction))
	b.WriteString(fmt.Sprintf("- **Source**: %s\n", res.SourcePlaylistURL))
	b.WriteString(fmt.Sprintf("- **Destination**: %s\n", res.PlaylistURL))
	b.WriteString(fmt.Sprintf("- **Source tracks**: %d\n", res.TotalSourceTracks))
	b.WriteString(fmt.Sprintf("- **Added**: %d\n", res.AddedTracks))
	b.WriteString(fmt.Sprintf("- **Skipped**: %d\n", res.SkippedTracks))
	b.WriteString(fmt.Sprintf("- **Synced at**: %s\n", res.LastSyncedAt))

	if len(res.Skipped) > 0 {
		b.WriteString("\n## Skipped tracks\n\n")
		for _, s := range res.Skipped {
			b.WriteString(fmt.Sprintf("- %s — %s (%s)\n", s.Title, strings.Join(s.Artists, ", "), s.Reason))
		}
	}
	if len(res.AddedAfterReview) > 0 {
		b.WriteString("\n## Added tracks\n\n")
		for _, a := range res.AddedAfterReview {
			b.WriteString(fmt.Sprintf("- %s — %s -> %s\n", a.Title, strings.Join(a.Artists, ", "), a.TargetTrackID))
		}
	}
	return b.String()
}
