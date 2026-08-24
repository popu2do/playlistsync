package spotify

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"playlistsync/internal/config"
	"playlistsync/internal/fileutil"
	"playlistsync/internal/model"
	"strconv"
	"strings"
)

// SpotifyReader defines the contract for reading, finding, and persisting Spotify playlist files.
type SpotifyReader interface {
	ReadPlaylistJSON(filePath string) (*model.SpotifyPlaylist, error)
	WritePlaylistJSON(filePath string, pl *model.SpotifyPlaylist) error
	WritePlaylistCSV(filePath string, pl *model.SpotifyPlaylist) error
	FindPlaylistByName(searchDir string, name string) (*model.SpotifyPlaylist, string, error)
}

type defaultSpotifyReader struct{}

// NewSpotifyReader returns a default SpotifyReader implementation.
func NewSpotifyReader() SpotifyReader {
	return &defaultSpotifyReader{}
}

func (r *defaultSpotifyReader) ReadPlaylistJSON(filePath string) (*model.SpotifyPlaylist, error) {
	return ReadPlaylistJSON(filePath)
}

func (r *defaultSpotifyReader) WritePlaylistJSON(filePath string, pl *model.SpotifyPlaylist) error {
	return WritePlaylistJSON(filePath, pl)
}

func (r *defaultSpotifyReader) WritePlaylistCSV(filePath string, pl *model.SpotifyPlaylist) error {
	return WritePlaylistCSV(filePath, pl)
}

func (r *defaultSpotifyReader) FindPlaylistByName(searchDir string, name string) (*model.SpotifyPlaylist, string, error) {
	return FindPlaylistByName(searchDir, name)
}

var _ SpotifyReader = (*defaultSpotifyReader)(nil)

// ReadPlaylistJSON loads a Spotify playlist from a JSON file
func ReadPlaylistJSON(filePath string) (*model.SpotifyPlaylist, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("spotify playlist file not found: %s", filePath)
		}
		return nil, fmt.Errorf("failed to read spotify playlist file %s: %w", filePath, err)
	}

	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))

	var pl model.SpotifyPlaylist
	if err := json.Unmarshal(data, &pl); err != nil {
		return nil, fmt.Errorf("corrupted spotify playlist json in %s: %w", filePath, err)
	}

	return &pl, nil
}

// FindPlaylistByName searches for a playlist file matching the name in search directory
func FindPlaylistByName(searchDir string, name string) (*model.SpotifyPlaylist, string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return nil, "", fmt.Errorf("playlist name cannot be empty")
	}

	if fi, err := os.Stat(trimmed); err == nil && !fi.IsDir() {
		if pl, err := ReadPlaylistJSON(trimmed); err == nil {
			return pl, trimmed, nil
		}
	}

	dir := searchDir
	defaultDir := config.GetOutputDir()
	if dir == "" {
		dir = defaultDir
	}

	dirs := []string{dir}
	if dir != defaultDir {
		dirs = append(dirs, defaultDir)
	}

	// Pass 1: Match by filename
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			fname := entry.Name()
			if !strings.HasSuffix(strings.ToLower(fname), ".json") || isIgnoredJSONFile(fname) {
				continue
			}
			base := strings.TrimSuffix(fname, filepath.Ext(fname))
			baseClean := strings.TrimSuffix(base, "_source")
			baseClean = strings.TrimPrefix(strings.TrimPrefix(baseClean, "spotify_"), "ytmusic_")

			if strings.EqualFold(fname, trimmed) ||
				strings.EqualFold(fname, trimmed+".json") ||
				strings.EqualFold(fname, "spotify_"+trimmed+"_source.json") ||
				strings.EqualFold(fname, trimmed+"_source.json") ||
				strings.EqualFold(base, trimmed) ||
				strings.EqualFold(baseClean, trimmed) {
				fullPath := filepath.Join(d, fname)
				pl, err := ReadPlaylistJSON(fullPath)
				if err != nil {
					return nil, "", err
				}
				return pl, fullPath, nil
			}
		}
	}

	// Pass 2: Match by playlist content (pl.PlaylistName)
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			fname := entry.Name()
			if isIgnoredJSONFile(fname) {
				continue
			}
			fullPath := filepath.Join(d, fname)
			pl, err := ReadPlaylistJSON(fullPath)
			if err == nil && pl != nil && strings.EqualFold(strings.TrimSpace(pl.PlaylistName), trimmed) {
				return pl, fullPath, nil
			}
		}
	}

	return nil, "", buildPlaylistNotFoundError(dirs, dir, name, trimmed)
}

func isIgnoredJSONFile(fname string) bool {
	lower := strings.ToLower(fname)
	return !strings.HasSuffix(lower, ".json") ||
		strings.Contains(lower, "_result") ||
		strings.Contains(lower, "_report") ||
		strings.Contains(lower, "credentials") ||
		lower == "browser.json"
}

func buildPlaylistNotFoundError(dirs []string, dir string, name string, trimmed string) error {
	availableMap := make(map[string]bool)
	var available []string

	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			fname := entry.Name()
			if isIgnoredJSONFile(fname) {
				continue
			}
			base := strings.TrimSuffix(fname, filepath.Ext(fname))
			candidateName := strings.TrimSuffix(base, "_source")
			candidateName = strings.TrimPrefix(strings.TrimPrefix(candidateName, "spotify_"), "ytmusic_")
			if candidateName != "" && !availableMap[candidateName] {
				availableMap[candidateName] = true
				available = append(available, candidateName)
			}
		}
	}

	target := strings.ToLower(trimmed)
	var bestSuggestion string
	bestDistance := 999

	for _, cand := range available {
		candLower := strings.ToLower(cand)
		dist := levenshteinDistance(candLower, target)
		if strings.HasPrefix(candLower, target) || strings.HasPrefix(target, candLower) {
			if dist > 1 {
				dist = 1
			}
		} else if strings.Contains(candLower, target) || strings.Contains(target, candLower) {
			if dist > 2 {
				dist = 2
			}
		}
		if dist < bestDistance {
			bestDistance = dist
			bestSuggestion = cand
		}
	}

	threshold := 3
	if len(trimmed) > 6 {
		threshold = len(trimmed)/2 + 1
	}

	errMsg := fmt.Sprintf("playlist '%s' not found in %s directory (searched 'spotify_%s_source.json', '%s_source.json', '%s.json')", name, dir, target, target, target)
	if bestSuggestion != "" && bestDistance <= threshold {
		errMsg += fmt.Sprintf(". Did you mean '%s'?", bestSuggestion)
	}
	if len(available) > 0 {
		errMsg += fmt.Sprintf(" Available playlists: [%s]", strings.Join(available, ", "))
	}

	return errors.New(errMsg)
}

func levenshteinDistance(s, t string) int {
	sRunes := []rune(s)
	tRunes := []rune(t)
	d := make([][]int, len(sRunes)+1)
	for i := range d {
		d[i] = make([]int, len(tRunes)+1)
		d[i][0] = i
	}
	for j := 0; j <= len(tRunes); j++ {
		d[0][j] = j
	}
	for i := 1; i <= len(sRunes); i++ {
		for j := 1; j <= len(tRunes); j++ {
			cost := 1
			if sRunes[i-1] == tRunes[j-1] {
				cost = 0
			}
			d[i][j] = minInt(d[i-1][j]+1, minInt(d[i][j-1]+1, d[i-1][j-1]+cost))
		}
	}
	return d[len(sRunes)][len(tRunes)]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// WritePlaylistJSON saves a Spotify playlist to a JSON file
func WritePlaylistJSON(filePath string, pl *model.SpotifyPlaylist) error {
	if pl != nil && pl.Platform == "" {
		pl.Platform = "spotify"
	}
	data, err := json.MarshalIndent(pl, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal spotify json: %w", err)
	}

	return fileutil.WriteFileAtomic(filePath, append(data, '\n'), 0644)
}

// WritePlaylistCSV exports a Spotify playlist to a CSV file atomically
func WritePlaylistCSV(filePath string, pl *model.SpotifyPlaylist) error {
	var buf bytes.Buffer
	if _, err := buf.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return fmt.Errorf("write utf-8 bom: %w", err)
	}

	writer := csv.NewWriter(&buf)
	if err := writer.Write([]string{"index", "title", "artists", "album", "duration", "query", "spotifyUrl"}); err != nil {
		return err
	}

	for _, t := range pl.Tracks {
		row := []string{
			strconv.Itoa(t.Index),
			t.Title,
			strings.Join(t.Artists, ", "),
			t.Album,
			t.Duration,
			t.Query,
			t.SpotifyURL,
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return fmt.Errorf("flush csv writer: %w", err)
	}

	return fileutil.WriteFileAtomic(filePath, buf.Bytes(), 0644)
}
