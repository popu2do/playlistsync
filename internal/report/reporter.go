package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"playlistsync/internal/model"
	"runtime"
	"strings"
)

// writeFileAtomic safely writes data to targetPath via a temporary file and atomic rename.
func writeFileAtomic(targetPath string, data []byte, perm os.FileMode) error {
	cleanTarget := filepath.Clean(targetPath)
	dir := filepath.Dir(cleanTarget)

	if stat, err := os.Stat(dir); err != nil || !stat.IsDir() {
		return fmt.Errorf("target directory does not exist: %s", dir)
	}

	tmpFile, err := os.CreateTemp(dir, "tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
	}()

	if _, err := tmpFile.Write(data); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if runtime.GOOS == "windows" {
		_ = os.Remove(cleanTarget)
	}
	if err := os.Rename(tmpName, cleanTarget); err != nil {
		return fmt.Errorf("atomic rename %s -> %s: %w", tmpName, cleanTarget, err)
	}
	return os.Chmod(cleanTarget, perm)
}

// readJSON reads and unmarshals a JSON file into target T, formatting consistent errors.
func readJSON[T any](path, missingErr, corruptErr string) (*T, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf(missingErr, path)
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var val T
	if err := json.Unmarshal(data, &val); err != nil {
		return nil, fmt.Errorf(corruptErr, path, err)
	}
	return &val, nil
}

// Reporter defines the contract for summarizing, validating, and generating migration reports.
type Reporter interface {
	Summarize(spotifyPath, resultPath string) error
	Validate(spotifyPath, resultPath, reportPath string) error
	GenerateReport(resultPath, reportPath string) error
}

type defaultReporter struct{}

// NewReporter returns a default Reporter implementation.
func NewReporter() Reporter {
	return &defaultReporter{}
}

func (r *defaultReporter) Summarize(spotifyPath, resultPath string) error {
	return Summarize(spotifyPath, resultPath)
}

func (r *defaultReporter) Validate(spotifyPath, resultPath, reportPath string) error {
	return Validate(spotifyPath, resultPath, reportPath)
}

func (r *defaultReporter) GenerateReport(resultPath, reportPath string) error {
	return GenerateReport(resultPath, reportPath)
}

var _ Reporter = (*defaultReporter)(nil)

// Summarize prints human-readable migration summary
func Summarize(spotifyPath, resultPath string) error {
	result, err := readJSON[model.SyncResult](
		resultPath,
		"migration result not found at %s. Run 'playlistsync sync <playlist_name>' first",
		"corrupted migration result file %s: %w",
	)
	if err != nil {
		return err
	}

	if result.Direction != "" {
		fmt.Printf("Direction: %s\n", result.Direction)
	}
	fmt.Printf("Source: %s\n", result.SourcePlaylistURL)
	fmt.Printf("Destination: %s\n", result.PlaylistURL)
	fmt.Printf("Title: %s\n", result.Title)
	fmt.Printf("Source tracks: %d\n", result.TotalSourceTracks)
	fmt.Printf("Added: %d\n", result.AddedTracks)
	fmt.Printf("Skipped: %d\n", result.SkippedTracks)

	if len(result.Skipped) > 0 {
		fmt.Println("Skipped tracks:")
		for _, item := range result.Skipped {
			fmt.Printf("- %s - %s\n", item.Title, strings.Join(item.Artists, ", "))
		}
	}

	if len(result.AddedAfterReview) > 0 {
		fmt.Println("Reviewed additions:")
		for _, item := range result.AddedAfterReview {
			fmt.Printf("- %s - %s -> %s\n", item.Title, strings.Join(item.Artists, ", "), item.TargetTrackID)
		}
	}

	return nil
}

// Validate checks integrity of migration artifacts
func Validate(spotifyPath, resultPath, reportPath string) error {
	var sourceTotal int
	sp, err := readJSON[model.SpotifyPlaylist](
		spotifyPath,
		"source playlist file not found at %s",
		"corrupted playlist file %s: %w",
	)
	if err == nil && sp != nil {
		sourceTotal = len(sp.Tracks)
	} else {
		// Attempt parsing as YouTube Music source playlist for bidirectional migrations
		ytm, ytmErr := readJSON[model.YTMPlaylist](
			spotifyPath,
			"source playlist file not found at %s",
			"corrupted playlist file %s: %w",
		)
		if ytmErr != nil {
			return fmt.Errorf("read source playlist at %s: %w", spotifyPath, err)
		}
		sourceTotal = len(ytm.Tracks)
	}

	res, err := readJSON[model.SyncResult](
		resultPath,
		"sync result file not found at %s. Run 'playlistsync sync <playlist_name>' first",
		"corrupted sync result file %s: %w",
	)
	if err != nil {
		return err
	}

	rep, err := readJSON[model.SyncResult](
		reportPath,
		"sync report file not found at %s. Run 'playlistsync report <playlist_name>' first",
		"corrupted sync report file %s: %w",
	)
	if err != nil {
		return err
	}

	var failures []string
	check := func(cond bool, format string, args ...any) {
		if !cond {
			failures = append(failures, fmt.Sprintf(format, args...))
		}
	}

	sourceCount := res.TotalSourceTracks

	check(sourceCount == sourceTotal, "source_total mismatch (source tracks: %d in %s != result TotalTracks: %d)", sourceTotal, spotifyPath, sourceCount)
	check(res.AddedTracks >= 0 && res.SkippedTracks >= 0, "negative track counts in result (added: %d, skipped: %d)", res.AddedTracks, res.SkippedTracks)
	check(res.TotalSourceTracks == res.AddedTracks+res.SkippedTracks,
		"Invariant 1 violation (total conservation): TotalSourceTracks (%d) != AddedTracks (%d) + SkippedTracks (%d)",
		res.TotalSourceTracks, res.AddedTracks, res.SkippedTracks)
	if len(res.Skipped) > 0 {
		check(res.SkippedTracks == len(res.Skipped), "skipped track count mismatch (SkippedTracks: %d != len(Skipped): %d)", res.SkippedTracks, len(res.Skipped))
	}
	check(rep.TotalSourceTracks == res.TotalSourceTracks && rep.AddedTracks == res.AddedTracks && rep.SkippedTracks == res.SkippedTracks,
		"report track metrics do not match result (report: total=%d, added=%d, skipped=%d vs result: total=%d, added=%d, skipped=%d)",
		rep.TotalSourceTracks, rep.AddedTracks, rep.SkippedTracks, res.TotalSourceTracks, res.AddedTracks, res.SkippedTracks)
	check(strings.TrimSpace(res.PlaylistURL) != "", "result playlist_url is empty")
	check(rep.PlaylistURL == res.PlaylistURL, "report playlist_url (%q) does not match result playlist_url (%q)", rep.PlaylistURL, res.PlaylistURL)

	// Invariant 2: Target capacity equivalence when verification snapshot is present
	if res.Verification != nil && res.Verification.PageTrackCount > 0 {
		check(res.Verification.PageTrackCount == res.AddedTracks,
			"target verification count mismatch (verification: %d != added: %d)",
			res.Verification.PageTrackCount, res.AddedTracks)
	}

	// Invariant 3: Skipped track reason attribution non-empty and valid 1-based index
	for i, s := range res.Skipped {
		check(strings.TrimSpace(s.Reason) != "", "skipped track at index %d (title %q) has empty reason", s.Index, s.Title)
		check(s.Index >= 1, "skipped track [%d] has invalid non-positive index %d", i, s.Index)
	}

	// Invariant 4: Added track video/resource ID and title validity
	for i, a := range res.AddedAfterReview {
		check(strings.TrimSpace(a.TargetTrackID) != "", "added track at index %d (title %q) has empty targetTrackId/resourceId", a.Index, a.Title)
		check(strings.TrimSpace(a.Title) != "", "added track [%d] has empty title", i)
		check(a.Index >= 1, "added track [%d] has invalid non-positive index %d", i, a.Index)
	}

	if len(failures) > 0 {
		return fmt.Errorf("validation failed: %s", strings.Join(failures, "; "))
	}

	fmt.Println("Validation passed")
	return nil
}

// GenerateReport ensures the final report matches the sync result with atomic write
func GenerateReport(resultPath, reportPath string) error {
	data, err := os.ReadFile(resultPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("cannot generate report: sync result not found at %s. Run 'playlistsync sync <playlist_name>' first", resultPath)
		}
		return fmt.Errorf("read result file %s: %w", resultPath, err)
	}
	var res model.SyncResult
	if err := json.Unmarshal(data, &res); err != nil {
		return fmt.Errorf("corrupted sync result at %s: %w", resultPath, err)
	}
	if err := writeFileAtomic(reportPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write migration report to %s: %w", reportPath, err)
	}
	fmt.Printf("Wrote %s\n", reportPath)
	return nil
}
