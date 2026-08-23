package engine

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"playlistsync/internal/auth"
	"playlistsync/internal/config"
	"playlistsync/internal/model"
	"playlistsync/internal/spotify"
	"playlistsync/internal/ytmusic"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultSearchConcurrency is the default worker pool size for concurrent candidate matching
const DefaultSearchConcurrency = 6

// writeFileAtomic safely writes data to targetPath via a temporary file and atomic rename.
func writeFileAtomic(targetPath string, data []byte, perm os.FileMode) error {
	cleanTarget := filepath.Clean(targetPath)
	dir := filepath.Dir(cleanTarget)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
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

// SyncDirection defines the direction of synchronization
type SyncDirection string

const (
	DirectionSpotifyToYouTube SyncDirection = "spotify-to-youtube-music"
	DirectionYouTubeToSpotify SyncDirection = "youtube-music-to-spotify"
)

// SyncConfig holds configuration parameters for the syncer
type SyncConfig struct {
	PlaylistName     string
	PlaylistJSONPath string
	Direction        SyncDirection
	SpotifyAuthPath  string
	YTMHeadersPath   string
	ResultJSONPath   string
	FinalReportPath  string
	PlaylistID       string
	ProxyURL         string
	OutputDir        string
	CleanExtra       bool
	AutoYes          bool
	Concurrency      int
	ConfirmPrompt    func(prompt string) bool
	ReviewPrompt     ReviewPromptFunc
}

// Syncer handles the end-to-end sync workflow
type Syncer struct {
	cfg SyncConfig
	yt  *ytmusic.Client
}

// NewSyncer initializes a new Syncer instance
func NewSyncer(cfg SyncConfig) (*Syncer, error) {
	if cfg.OutputDir == "" {
		cfg.OutputDir = config.GetOutputDir()
	}
	if cfg.YTMHeadersPath == "" {
		cfg.YTMHeadersPath = config.GetYTMAuthPath()
	}
	if cfg.SpotifyAuthPath == "" {
		cfg.SpotifyAuthPath = config.GetSpotifyAuthPath()
	}
	if cfg.Direction == "" {
		cfg.Direction = DirectionSpotifyToYouTube
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = DefaultSearchConcurrency
	}

	if cfg.ProxyURL != "" {
		if _, err := url.Parse(cfg.ProxyURL); err != nil {
			return nil, fmt.Errorf("invalid proxy url: %w", err)
		}
	}

	return &Syncer{
		cfg: cfg,
	}, nil
}

// Run executes the complete sync workflow
func (s *Syncer) Run() (*model.SyncResult, error) {
	if s.cfg.Direction == DirectionYouTubeToSpotify {
		if _, err := auth.EnsureAuthenticated(auth.PlatformSpotify, s.cfg.SpotifyAuthPath, s.cfg.ProxyURL); err != nil {
			return nil, fmt.Errorf("ensure spotify authentication: %w", err)
		}
		return s.runSyncYouTubeToSpotify()
	}

	if _, err := auth.EnsureAuthenticated(auth.PlatformYouTube, s.cfg.YTMHeadersPath, s.cfg.ProxyURL); err != nil {
		return nil, fmt.Errorf("ensure youtube music authentication: %w", err)
	}

	yt, err := ytmusic.NewClient(s.cfg.YTMHeadersPath, s.cfg.ProxyURL)
	if err != nil {
		return nil, fmt.Errorf("init youtube music client: %w", err)
	}
	s.yt = yt

	return s.runSyncSpotifyToYouTube()
}

func (s *Syncer) runSyncSpotifyToYouTube() (*model.SyncResult, error) {
	var spPlaylist *model.SpotifyPlaylist
	var spPath string
	var err error

	target := strings.TrimSpace(s.cfg.PlaylistName)

	// 0. Explicit JSON path passed via config/flag
	if s.cfg.PlaylistJSONPath != "" {
		if loaded, loadErr := spotify.ReadPlaylistJSON(s.cfg.PlaylistJSONPath); loadErr == nil && loaded != nil {
			spPlaylist = loaded
			spPath = s.cfg.PlaylistJSONPath
		}
	}

	// 1. If Spotify credentials/token available, use Web API (supports full pagination >100 tracks)
	if spPlaylist == nil {
		if token, tokenErr := auth.GetSpotifyAccessToken(s.cfg.SpotifyAuthPath, s.cfg.ProxyURL); tokenErr == nil && token != "" {
			spClient := spotify.NewClient(token, s.cfg.ProxyURL)
			if fetched, fetchErr := spClient.FindPlaylist(target); fetchErr == nil && fetched != nil {
				spPlaylist = fetched
				slug := strings.ToLower(strings.TrimSpace(spPlaylist.PlaylistName))
				spPath = filepath.Join(s.cfg.OutputDir, fmt.Sprintf("%s_playlist.json", slug))
				_ = os.MkdirAll(s.cfg.OutputDir, 0755)
				_ = spotify.WritePlaylistJSON(spPath, spPlaylist)
			}
		}
	}

	// 2. Direct URL/ID check with Embed fallback
	if spPlaylist == nil && (strings.Contains(target, "open.spotify.com/playlist/") || (len(target) == 22 && isAlphanumericID(target))) {
		if fetched, fetchErr := spotify.FetchPlaylistFromEmbed(target, s.cfg.ProxyURL); fetchErr == nil && fetched != nil {
			spPlaylist = fetched
			slug := strings.ToLower(strings.TrimSpace(spPlaylist.PlaylistName))
			spPath = filepath.Join(s.cfg.OutputDir, fmt.Sprintf("%s_playlist.json", slug))
			_ = os.MkdirAll(s.cfg.OutputDir, 0755)
			_ = spotify.WritePlaylistJSON(spPath, spPlaylist)
		}
	}

	// 3. Local cached playlist file check
	if spPlaylist == nil {
		spPlaylist, spPath, err = spotify.FindPlaylistByName(s.cfg.OutputDir, s.cfg.PlaylistName)
	}

	// 4. Fallback search (prioritize Spotify Web API for full pagination, fallback to Embed)
	if spPlaylist == nil {
		fmt.Printf("Searching user's Spotify account for playlist '%s'...\n", s.cfg.PlaylistName)
		token, tokenErr := auth.GetSpotifyAccessToken(s.cfg.SpotifyAuthPath, s.cfg.ProxyURL)
		if tokenErr == nil && token != "" {
			spClient := spotify.NewClient(token, s.cfg.ProxyURL)
			if fetched, fetchErr := spClient.FindPlaylist(s.cfg.PlaylistName); fetchErr == nil && fetched != nil {
				spPlaylist = fetched
			}
		}
		if spPlaylist == nil {
			if fetched, fetchErr := spotify.FetchPlaylistFromEmbed(s.cfg.PlaylistName, s.cfg.ProxyURL); fetchErr == nil && fetched != nil {
				spPlaylist = fetched
			} else if tokenErr != nil {
				return nil, fmt.Errorf("find spotify playlist locally (%v) and failed to acquire Spotify access token: %w", err, tokenErr)
			} else {
				return nil, fmt.Errorf("find spotify playlist in account: playlist '%s' not found", s.cfg.PlaylistName)
			}
		}
		slug := strings.ToLower(strings.TrimSpace(spPlaylist.PlaylistName))
		spPath = filepath.Join(s.cfg.OutputDir, fmt.Sprintf("%s_playlist.json", slug))
		_ = os.MkdirAll(s.cfg.OutputDir, 0755)
		_ = spotify.WritePlaylistJSON(spPath, spPlaylist)
	}
	fmt.Printf("Loaded %d tracks from '%s' (%s)\n", len(spPlaylist.Tracks), spPlaylist.PlaylistName, spPath)

	playlistID := cleanYTMPlaylistID(s.cfg.PlaylistID)
	var targetTitle string

	if playlistID == "" {
		summary, findErr := s.yt.FindPlaylistByTitle(spPlaylist.PlaylistName)
		if findErr != nil {
			fmt.Printf("Warning: failed to query library playlists: %v\n", findErr)
		}
		if summary != nil {
			playlistID = summary.ID
			targetTitle = summary.Title
			fmt.Printf("Found existing YouTube Music playlist in library: '%s' (ID: %s)\n", targetTitle, playlistID)
		} else {
			newTitle := fmt.Sprintf("%s (Spotify import)", spPlaylist.PlaylistName)
			desc := fmt.Sprintf("Imported from Spotify playlist: %s", spPlaylist.PlaylistName)
			if s.cfg.ConfirmPrompt != nil && !s.cfg.ConfirmPrompt(fmt.Sprintf("Playlist '%s' not found on YouTube Music. Create new playlist '%s'?", spPlaylist.PlaylistName, newTitle)) {
				return nil, fmt.Errorf("sync aborted: playlist creation cancelled by user")
			}
			fmt.Printf("Creating new YouTube Music playlist: '%s'...\n", newTitle)
			createdID, createErr := s.yt.CreatePlaylist(newTitle, desc, "PRIVATE")
			if createErr != nil {
				return nil, fmt.Errorf("create ytm playlist: %w", createErr)
			}
			playlistID = createdID
			targetTitle = newTitle
			fmt.Printf("Created new YouTube Music playlist successfully: ID = %s\n", playlistID)
		}
	}

	ytmPlaylist, err := s.yt.GetPlaylist(playlistID)
	if err != nil {
		return nil, fmt.Errorf("fetch ytm playlist: %w", err)
	}
	fmt.Printf("YouTube Music playlist: '%s' (Existing tracks: %d)\n", ytmPlaylist.Title, len(ytmPlaylist.Tracks))

	mapping := s.loadKnownMapping(spPlaylist)
	diff := ComputeDiff(spPlaylist, ytmPlaylist, mapping)
	fmt.Printf("Diff computed: %d matched, %d missing in destination, %d extra in destination\n", len(diff.Matched), len(diff.MissingInYTM), len(diff.ExtraInYTM))

	var skippedList []model.SkippedTrack
	var toAddVideoIDs []string
	var unmappedMissing []model.SpotifyTrack

	for _, track := range diff.MissingInYTM {
		if vid, hasMap := mapping[track.Index]; hasMap && vid != "" {
			toAddVideoIDs = append(toAddVideoIDs, vid)
		} else {
			unmappedMissing = append(unmappedMissing, track)
		}
	}

	var ambiguousResolutions []ytmCandidateResolution

	if len(unmappedMissing) > 0 {
		resolutions := s.resolveMissingYTMCandidatesConcurrently(unmappedMissing, s.cfg.Concurrency)
		for _, res := range resolutions {
			if res.totalCandidates == 0 {
				fmt.Printf(" - No candidates found, skipping: %s\n", res.track.Title)
				skippedList = append(skippedList, model.SkippedTrack{
					Index:   res.track.Index,
					Title:   res.track.Title,
					Artists: res.track.Artists,
					Reason:  "unavailable on destination platform",
				})
			} else if res.bestScore >= ConfidenceThreshold && res.bestCandidate != nil {
				mapping[res.track.Index] = res.bestCandidate.VideoID
				toAddVideoIDs = append(toAddVideoIDs, res.bestCandidate.VideoID)
			} else {
				ambiguousResolutions = append(ambiguousResolutions, res)
			}
		}
	}

	var reviewedAdditions []model.AddedTrack
	if resultData, err := os.ReadFile(s.cfg.ResultJSONPath); err == nil {
		var prevResult model.SyncResult
		if err := json.Unmarshal(resultData, &prevResult); err == nil {
			reviewedAdditions = prevResult.AddedAfterReview
		}
	}

	var reviewItems []ReviewItem
	for _, res := range ambiguousResolutions {
		reviewItems = append(reviewItems, ReviewItem{
			SourceIndex:    res.track.Index,
			SourceTitle:    res.track.Title,
			SourceArtists:  res.track.Artists,
			SourceDuration: res.track.Duration,
			SourceURL:      res.track.SpotifyURL,
			SourcePlatform: "spotify",
			TargetPlatform: "youtube-music",
			Options:        res.topOptions,
		})
	}

	reviewOutcome := ExecuteReview(reviewItems, s.cfg.ReviewPrompt, s.cfg.AutoYes)
	for idx, vid := range reviewOutcome.AcceptedIDs {
		mapping[idx] = vid
		toAddVideoIDs = append(toAddVideoIDs, vid)
	}
	reviewedAdditions = append(reviewedAdditions, reviewOutcome.ReviewedAdditions...)
	skippedList = append(skippedList, reviewOutcome.SkippedTracks...)

	currentVidSet := make(map[string]bool)
	for _, t := range ytmPlaylist.Tracks {
		currentVidSet[t.VideoID] = true
	}
	var newVidsToAdd []string
	for _, vid := range toAddVideoIDs {
		if !currentVidSet[vid] {
			newVidsToAdd = append(newVidsToAdd, vid)
			currentVidSet[vid] = true
		}
	}

	if len(newVidsToAdd) > 0 {
		fmt.Printf("Adding %d missing tracks to YouTube Music playlist...\n", len(newVidsToAdd))
		if err := s.yt.AddPlaylistItems(playlistID, newVidsToAdd); err != nil {
			return nil, fmt.Errorf("add items: %w", err)
		}
	}

	finalPl, err := s.yt.GetPlaylist(playlistID)
	if err != nil {
		return nil, fmt.Errorf("fetch final playlist: %w", err)
	}

	// Eventual consistency: if tracks were just added but YTM hasn't reflected them yet,
	// retry with exponential back-off (max 3 retries: 500ms, 1s, 2s).
	expectedMinTracks := len(ytmPlaylist.Tracks) + len(newVidsToAdd)
	if len(newVidsToAdd) > 0 && len(finalPl.Tracks) < expectedMinTracks {
		if retriedPl, ok := waitForPlaylistWithTrackCount(expectedMinTracks, 3, 500*time.Millisecond, func() (*model.YTMPlaylist, error) {
			return s.yt.GetPlaylist(playlistID)
		}, func(p *model.YTMPlaylist) int {
			if p == nil {
				return 0
			}
			return len(p.Tracks)
		}); ok && retriedPl != nil {
			finalPl = retriedPl
		}
	}

	// Safe Pruning: CleanExtra executed AFTER adding new tracks and re-fetching destination state
	var removedList []model.RemovedTrack
	if s.cfg.CleanExtra {
		recomputedDiff := ComputeDiff(spPlaylist, finalPl, mapping)
		if len(recomputedDiff.ExtraInYTM) > 0 {
			fmt.Printf("Notice: Found %d extraneous track(s) in destination YouTube Music playlist:\n", len(recomputedDiff.ExtraInYTM))
			for _, t := range recomputedDiff.ExtraInYTM {
				fmt.Printf(" - %s (%s)\n", t.Title, t.VideoID)
			}
			proceed := true
			if !s.cfg.AutoYes && s.cfg.ConfirmPrompt != nil {
				proceed = s.cfg.ConfirmPrompt(fmt.Sprintf("Confirm deletion of %d extraneous track(s) from '%s'?", len(recomputedDiff.ExtraInYTM), finalPl.Title))
			}
			if proceed {
				fmt.Printf("Removing %d extra tracks from YouTube Music playlist...\n", len(recomputedDiff.ExtraInYTM))
				for _, t := range recomputedDiff.ExtraInYTM {
					removedList = append(removedList, model.RemovedTrack{
						TargetTrackID: t.VideoID,
						Title:         t.Title,
						Artists:       t.Artists,
					})
				}
				if err := s.yt.RemovePlaylistItems(playlistID, recomputedDiff.ExtraInYTM); err != nil {
					return nil, fmt.Errorf("remove extra items: %w", err)
				}
				if refreshedPl, refErr := s.yt.GetPlaylist(playlistID); refErr == nil {
					finalPl = refreshedPl
				}
			} else {
				fmt.Println("Extra track removal cancelled by user; skipping removal.")
			}
		}
	}

	for _, st := range spPlaylist.Tracks {
		if vid, hasMap := mapping[st.Index]; !hasMap || vid == "" {
			alreadyInSkip := false
			for _, sk := range skippedList {
				if sk.Index == st.Index {
					alreadyInSkip = true
					break
				}
			}
			if !alreadyInSkip {
				skippedList = append(skippedList, model.SkippedTrack{
					Index:   st.Index,
					Title:   st.Title,
					Artists: st.Artists,
					Reason:  "unresolved / low confidence on destination platform",
				})
			}
		}
	}

	sort.Slice(skippedList, func(i, j int) bool {
		return skippedList[i].Index < skippedList[j].Index
	})

	matchedOrAdded := len(spPlaylist.Tracks) - len(skippedList)
	if matchedOrAdded < 0 {
		matchedOrAdded = 0
	}

	result := &model.SyncResult{
		Direction:          "spotify-to-youtube-music",
		SourcePlatform:     "spotify",
		TargetPlatform:     "youtube-music",
		PlaylistID:         playlistID,
		PlaylistURL:        fmt.Sprintf("https://music.youtube.com/playlist?list=%s", playlistID),
		WebURL:             fmt.Sprintf("https://www.youtube.com/playlist?list=%s", playlistID),
		Title:              finalPl.Title,
		SourcePlaylistURL:  spPlaylist.SourcePlaylistURL,
		TotalSourceTracks:  len(spPlaylist.Tracks),
		AddedTracks:        matchedOrAdded,
		SkippedTracks:      len(skippedList),
		Skipped:            skippedList,
		AddedAfterReview:   reviewedAdditions,
		RemovedExtraTracks: removedList,
		LastSyncedAt:       time.Now().UTC().Format(time.RFC3339),
		Verification: &model.Verification{
			PageTitle:      finalPl.Title,
			PageTrackCount: len(finalPl.Tracks),
			Description:    finalPl.Description,
		},
	}

	saveSyncArtifacts(result, s.cfg.ResultJSONPath, s.cfg.FinalReportPath, s.cfg.OutputDir, spPlaylist.PlaylistName, "spotify", "ytmusic")

	fmt.Printf("\nSync completed: [%s] -> [%s] | Total: %d, Added: %d, Skipped: %d, Removed: %d\n",
		spPlaylist.PlaylistName, result.Title, result.TotalSourceTracks, result.AddedTracks, result.SkippedTracks, len(result.RemovedExtraTracks))
	return result, nil
}

func (s *Syncer) runSyncYouTubeToSpotify() (*model.SyncResult, error) {
	yt, err := ytmusic.NewClient(s.cfg.YTMHeadersPath, s.cfg.ProxyURL)
	if err != nil {
		return nil, fmt.Errorf("init youtube music client: %w", err)
	}
	s.yt = yt

	token, err := auth.GetSpotifyAccessToken(s.cfg.SpotifyAuthPath, s.cfg.ProxyURL)
	if err != nil {
		return nil, fmt.Errorf("acquire spotify access token: %w", err)
	}
	spClient := spotify.NewClient(token, s.cfg.ProxyURL)

	var ytmPlaylist *model.YTMPlaylist
	sourceTarget := strings.TrimSpace(s.cfg.PlaylistName)
	if sourceTarget == "" && s.cfg.PlaylistID != "" {
		sourceTarget = strings.TrimSpace(s.cfg.PlaylistID)
	}
	sourceTarget = cleanYTMPlaylistID(sourceTarget)

	if strings.HasPrefix(sourceTarget, "PL") || strings.HasPrefix(sourceTarget, "VL") || strings.HasPrefix(sourceTarget, "RD") || strings.HasPrefix(sourceTarget, "MPREb") || strings.HasPrefix(sourceTarget, "OLAK") || len(sourceTarget) >= 18 {
		ytmPlaylist, err = s.yt.GetPlaylist(sourceTarget)
	}
	if ytmPlaylist == nil {
		summary, findErr := s.yt.FindPlaylistByTitle(s.cfg.PlaylistName)
		if findErr == nil && summary != nil {
			ytmPlaylist, err = s.yt.GetPlaylist(summary.ID)
		}
	}
	if ytmPlaylist == nil && err != nil {
		return nil, fmt.Errorf("failed to find YouTube Music playlist: %w", err)
	}
	if ytmPlaylist == nil {
		return nil, fmt.Errorf("YouTube Music playlist '%s' not found", s.cfg.PlaylistName)
	}

	fmt.Printf("Loaded %d tracks from YouTube Music playlist '%s' (ID: %s)\n", len(ytmPlaylist.Tracks), ytmPlaylist.Title, ytmPlaylist.ID)

	var spPlaylist *model.SpotifyPlaylist
	var spPlaylistID string
	var spPlaylistTitle string

	// If destination playlist ID was specifically passed via PlaylistID (and PlaylistName was used for source)
	if s.cfg.PlaylistID != "" && s.cfg.PlaylistName != "" && s.cfg.PlaylistID != s.cfg.PlaylistName {
		destTarget := extractSpotifyPlaylistID(s.cfg.PlaylistID)
		if fetched, fetchErr := spClient.GetPlaylist(destTarget); fetchErr == nil && fetched != nil {
			spPlaylist = fetched
		} else if fetched, fetchErr := spClient.FindPlaylist(s.cfg.PlaylistID); fetchErr == nil && fetched != nil {
			spPlaylist = fetched
		}
	}

	if spPlaylist == nil {
		if fetched, fetchErr := spClient.FindPlaylist(ytmPlaylist.Title); fetchErr == nil && fetched != nil {
			spPlaylist = fetched
		}
	}

	if spPlaylist != nil {
		spPlaylistTitle = spPlaylist.PlaylistName
		spPlaylistID = extractSpotifyPlaylistID(spPlaylist.SourcePlaylistURL)
		if spPlaylistID == "" && len(spPlaylist.SourcePlaylistURL) == 22 && isAlphanumericID(spPlaylist.SourcePlaylistURL) {
			spPlaylistID = spPlaylist.SourcePlaylistURL
		}
		fmt.Printf("Found existing Spotify playlist: '%s' (ID: %s)\n", spPlaylistTitle, spPlaylistID)
	} else {
		newTitle := fmt.Sprintf("%s (YouTube Music import)", ytmPlaylist.Title)
		desc := fmt.Sprintf("Imported from YouTube Music playlist: %s", ytmPlaylist.Title)
		if !s.cfg.AutoYes && s.cfg.ConfirmPrompt != nil && !s.cfg.ConfirmPrompt(fmt.Sprintf("Playlist '%s' not found on Spotify. Create new playlist '%s'?", ytmPlaylist.Title, newTitle)) {
			return nil, fmt.Errorf("sync aborted: playlist creation cancelled by user")
		}
		fmt.Printf("Creating new Spotify playlist: '%s'...\n", newTitle)
		createdID, createErr := spClient.CreatePlaylist(newTitle, desc)
		if createErr != nil {
			return nil, fmt.Errorf("create spotify playlist: %w", createErr)
		}
		spPlaylistID = createdID
		spPlaylistTitle = newTitle
		spPlaylist = &model.SpotifyPlaylist{
			PlaylistName:      newTitle,
			SourcePlaylistURL: fmt.Sprintf("https://open.spotify.com/playlist/%s", createdID),
			Tracks:            []model.SpotifyTrack{},
		}
		fmt.Printf("Created new Spotify playlist successfully: ID = %s\n", spPlaylistID)
	}

	matchedExistingSpotifyIndices := make(map[int]bool)
	existingSpotifyURIs := make(map[string]bool)
	for _, st := range spPlaylist.Tracks {
		if st.SpotifyURI != "" {
			existingSpotifyURIs[st.SpotifyURI] = true
		}
		if st.ID != "" {
			existingSpotifyURIs[fmt.Sprintf("spotify:track:%s", st.ID)] = true
		}
	}

	var skippedList []model.SkippedTrack
	var matchedTracks []model.AddedTrack
	var toAddURIs []string
	var unmatchedYTM []struct {
		index int
		track model.YTMTrack
	}

	for i, yTrack := range ytmPlaylist.Tracks {
		// 1. Check if this YTM track already matches an existing track in the target Spotify playlist
		var matchedExisting *model.SpotifyTrack
		for j := range spPlaylist.Tracks {
			st := &spPlaylist.Tracks[j]
			if matchedExistingSpotifyIndices[st.Index] {
				continue
			}
			if CalculateTrackScore(yTrack.Title, yTrack.Artists, yTrack.Duration, st.Title, st.Artists, st.Duration) >= ConfidenceThreshold {
				matchedExistingSpotifyIndices[st.Index] = true
				matchedExisting = st
				break
			}
		}

		if matchedExisting != nil {
			matchedTracks = append(matchedTracks, model.AddedTrack{
				Index:            i + 1,
				Title:            yTrack.Title,
				Artists:          yTrack.Artists,
				TargetTrackID:    matchedExisting.ID,
				DestinationTitle: matchedExisting.Title,
			})
			continue
		}

		unmatchedYTM = append(unmatchedYTM, struct {
			index int
			track model.YTMTrack
		}{index: i + 1, track: yTrack})
	}

	var skippedUnavailable []model.SkippedTrack
	var ambiguousSpotify []spCandidateResolution

	if len(unmatchedYTM) > 0 {
		resolutions := resolveMissingSpotifyCandidatesConcurrently(spClient, unmatchedYTM, s.cfg.Concurrency)
		for _, res := range resolutions {
			if res.totalCandidates == 0 {
				skippedUnavailable = append(skippedUnavailable, model.SkippedTrack{
					Index:   res.index,
					Title:   res.track.Title,
					Artists: res.track.Artists,
					Reason:  "unavailable on destination platform",
				})
			} else if res.bestScore >= ConfidenceThreshold && res.bestCandidate != nil {
				matchedTracks = append(matchedTracks, model.AddedTrack{
					Index:            res.index,
					Title:            res.track.Title,
					Artists:          res.track.Artists,
					TargetTrackID:    res.bestCandidate.ID,
					DestinationTitle: res.bestCandidate.Title,
				})
				candURI := res.bestCandidate.SpotifyURI
				if candURI == "" && res.bestCandidate.ID != "" {
					candURI = fmt.Sprintf("spotify:track:%s", res.bestCandidate.ID)
				}
				if candURI != "" && !existingSpotifyURIs[candURI] {
					toAddURIs = append(toAddURIs, candURI)
					existingSpotifyURIs[candURI] = true
				}
			} else {
				ambiguousSpotify = append(ambiguousSpotify, res)
			}
		}
	}

	var spReviewItems []ReviewItem
	for _, res := range ambiguousSpotify {
		spReviewItems = append(spReviewItems, ReviewItem{
			SourceIndex:    res.index,
			SourceTitle:    res.track.Title,
			SourceArtists:  res.track.Artists,
			SourceDuration: res.track.Duration,
			SourceURL:      fmt.Sprintf("https://music.youtube.com/watch?v=%s", res.track.VideoID),
			SourcePlatform: "youtube-music",
			TargetPlatform: "spotify",
			Options:        res.topOptions,
		})
	}

	spReviewOutcome := ExecuteReview(spReviewItems, s.cfg.ReviewPrompt, s.cfg.AutoYes)
	for _, added := range spReviewOutcome.ReviewedAdditions {
		matchedTracks = append(matchedTracks, added)
		uri := fmt.Sprintf("spotify:track:%s", added.TargetTrackID)
		if !existingSpotifyURIs[uri] {
			toAddURIs = append(toAddURIs, uri)
			existingSpotifyURIs[uri] = true
		}
	}
	skippedList = append(skippedList, spReviewOutcome.SkippedTracks...)
	skippedList = append(skippedList, skippedUnavailable...)

	if len(toAddURIs) > 0 {
		fmt.Printf("Adding %d tracks to Spotify playlist...\n", len(toAddURIs))
		if err := spClient.AddTracksToPlaylist(spPlaylistID, toAddURIs); err != nil {
			return nil, fmt.Errorf("add tracks to spotify playlist: %w", err)
		}
	}

	finalPl, err := spClient.GetPlaylist(spPlaylistID)
	if err != nil {
		return nil, fmt.Errorf("fetch destination spotify playlist post-sync (%s): %w", spPlaylistID, err)
	}

	// Eventual consistency: retry with exponential back-off if tracks not yet reflected.
	expectedMinSPTracks := len(spPlaylist.Tracks) + len(toAddURIs)
	if len(toAddURIs) > 0 && len(finalPl.Tracks) < expectedMinSPTracks {
		if retriedPl, ok := waitForPlaylistWithTrackCount(expectedMinSPTracks, 3, 500*time.Millisecond, func() (*model.SpotifyPlaylist, error) {
			return spClient.GetPlaylist(spPlaylistID)
		}, func(p *model.SpotifyPlaylist) int {
			if p == nil {
				return 0
			}
			return len(p.Tracks)
		}); ok && retriedPl != nil {
			finalPl = retriedPl
		}
	}

	// Safe Pruning: CleanExtra executed AFTER adding new tracks and re-fetching destination state
	var removedList []model.RemovedTrack
	if s.cfg.CleanExtra {
		var extraTracks []model.SpotifyTrack
		for _, st := range finalPl.Tracks {
			matched := false
			for _, yt := range ytmPlaylist.Tracks {
				if CalculateTrackScore(st.Title, st.Artists, st.Duration, yt.Title, yt.Artists, yt.Duration) >= ConfidenceThreshold {
					matched = true
					break
				}
			}
			if !matched {
				extraTracks = append(extraTracks, st)
			}
		}

		if len(extraTracks) > 0 {
			fmt.Printf("Notice: Found %d extraneous track(s) in destination Spotify playlist:\n", len(extraTracks))
			for _, t := range extraTracks {
				fmt.Printf(" - %s (%s)\n", t.Title, t.ID)
			}
			proceed := true
			if !s.cfg.AutoYes && s.cfg.ConfirmPrompt != nil {
				proceed = s.cfg.ConfirmPrompt(fmt.Sprintf("Confirm deletion of %d extraneous track(s) from '%s'?", len(extraTracks), finalPl.PlaylistName))
			}
			if proceed {
				fmt.Printf("Removing %d extra tracks from Spotify playlist...\n", len(extraTracks))
				var extraURIs []string
				for _, t := range extraTracks {
					uri := t.SpotifyURI
					if uri == "" && t.ID != "" {
						uri = fmt.Sprintf("spotify:track:%s", t.ID)
					}
					if uri != "" {
						extraURIs = append(extraURIs, uri)
					}
					removedList = append(removedList, model.RemovedTrack{
						TargetTrackID: t.ID,
						Title:         t.Title,
						Artists:       t.Artists,
					})
				}
				if err := spClient.RemoveTracksFromPlaylist(spPlaylistID, extraURIs); err != nil {
					return nil, fmt.Errorf("remove extra tracks from spotify playlist: %w", err)
				}
				if refreshedPl, refErr := spClient.GetPlaylist(spPlaylistID); refErr == nil {
					finalPl = refreshedPl
				}
			} else {
				fmt.Println("Extra track removal cancelled by user; skipping removal.")
			}
		}
	}

	sort.Slice(skippedList, func(i, j int) bool {
		return skippedList[i].Index < skippedList[j].Index
	})

	matchedOrAdded := len(ytmPlaylist.Tracks) - len(skippedList)
	if matchedOrAdded < 0 {
		matchedOrAdded = 0
	}

	result := &model.SyncResult{
		Direction:          "youtube-music-to-spotify",
		SourcePlatform:     "youtube-music",
		TargetPlatform:     "spotify",
		PlaylistID:         spPlaylistID,
		PlaylistURL:        fmt.Sprintf("https://open.spotify.com/playlist/%s", spPlaylistID),
		WebURL:             fmt.Sprintf("https://open.spotify.com/playlist/%s", spPlaylistID),
		Title:              finalPl.PlaylistName,
		SourcePlaylistURL:  fmt.Sprintf("https://music.youtube.com/playlist?list=%s", ytmPlaylist.ID),
		TotalSourceTracks:  len(ytmPlaylist.Tracks),
		AddedTracks:        matchedOrAdded,
		SkippedTracks:      len(skippedList),
		Skipped:            skippedList,
		AddedAfterReview:   spReviewOutcome.ReviewedAdditions,
		RemovedExtraTracks: removedList,
		LastSyncedAt:       time.Now().UTC().Format(time.RFC3339),
		Verification: &model.Verification{
			PageTitle:      finalPl.PlaylistName,
			PageTrackCount: len(finalPl.Tracks),
			Description:    ytmPlaylist.Description,
		},
	}

	saveSyncArtifacts(result, s.cfg.ResultJSONPath, s.cfg.FinalReportPath, s.cfg.OutputDir, ytmPlaylist.Title, "ytmusic", "spotify")

	fmt.Printf("\nSync completed: [%s] -> [%s] | Total: %d, Added: %d, Skipped: %d, Removed: %d\n",
		ytmPlaylist.Title, result.Title, result.TotalSourceTracks, result.AddedTracks, result.SkippedTracks, len(result.RemovedExtraTracks))
	return result, nil
}

func cleanYTMPlaylistID(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "list=") {
		parts := strings.Split(raw, "list=")
		if len(parts) > 1 {
			return strings.Split(parts[1], "&")[0]
		}
	}
	return raw
}

func extractSpotifyPlaylistID(target string) string {
	target = strings.TrimSpace(target)
	if strings.Contains(target, "playlist/") {
		parts := strings.Split(target, "playlist/")
		if len(parts) > 1 {
			return strings.Split(parts[1], "?")[0]
		}
	}
	return target
}

// generateCandidateQueries generates prioritized fallback search queries for track matching
func generateCandidateQueries(rawTitle string, artists []string, customQuery string) []string {
	var queries []string
	seen := make(map[string]bool)

	add := func(q string) {
		q = strings.TrimSpace(q)
		if q != "" && !seen[q] {
			seen[q] = true
			queries = append(queries, q)
		}
	}

	if customQuery != "" {
		add(customQuery)
	}

	rawTitle = strings.TrimSpace(rawTitle)
	cleanTitle := strings.TrimSpace(stripNoiseBrackets(rawTitle))

	var primaryArtist string
	if len(artists) > 0 {
		primaryArtist = strings.TrimSpace(artists[0])
	}
	allArtists := strings.TrimSpace(strings.Join(artists, " "))

	// 1. Primary query: Cleaned title + Primary Artist
	if cleanTitle != "" && primaryArtist != "" {
		add(fmt.Sprintf("%s %s", cleanTitle, primaryArtist))
	}
	// 2. Multi-artist query
	if cleanTitle != "" && allArtists != "" && len(artists) > 1 {
		add(fmt.Sprintf("%s %s", cleanTitle, allArtists))
	}
	// 3. Raw title query (in case strip was too aggressive)
	if rawTitle != "" && rawTitle != cleanTitle && primaryArtist != "" {
		add(fmt.Sprintf("%s %s", rawTitle, primaryArtist))
	}
	if rawTitle != "" && rawTitle != cleanTitle && allArtists != "" && len(artists) > 1 {
		add(fmt.Sprintf("%s %s", rawTitle, allArtists))
	}

	// 4. Dash / hyphen prefix decomposition (always with artist)
	for _, dash := range []string{" — ", " - ", " – "} {
		if strings.Contains(cleanTitle, dash) {
			for _, part := range strings.Split(cleanTitle, dash) {
				p := strings.TrimSpace(part)
				if len([]rune(p)) >= 3 && primaryArtist != "" {
					add(fmt.Sprintf("%s %s", p, primaryArtist))
				}
			}
		}
	}

	// 5. Bracket subtitle decomposition (always with artist)
	for _, p := range bracketPairs {
		openIdx := strings.IndexRune(rawTitle, p.open)
		closeIdx := strings.IndexRune(rawTitle, p.close)
		if openIdx >= 0 && closeIdx > openIdx {
			inner := strings.TrimSpace(rawTitle[openIdx+1 : closeIdx])
			if inner != "" && !isNoiseContent(inner) && primaryArtist != "" {
				add(fmt.Sprintf("%s %s", inner, primaryArtist))
			}
		}
	}

	// 6. Title only fallback (for cross-lingual artists where artist name is unrecognizable)
	if cleanTitle != "" {
		add(cleanTitle)
	}
	if rawTitle != "" && rawTitle != cleanTitle {
		add(rawTitle)
	}

	return queries
}

func generateYTMToSpotifyQueries(track model.YTMTrack) []string {
	return generateCandidateQueries(track.Title, track.Artists, "")
}

// GenerateSearchQueries generates prioritized fallback search queries for track matching
func GenerateSearchQueries(track model.SpotifyTrack) []string {
	return generateCandidateQueries(track.Title, track.Artists, track.Query)
}

func (s *Syncer) loadKnownMapping(sp *model.SpotifyPlaylist) map[int]string {
	mapping := make(map[int]string)

	slug := strings.ToLower(strings.TrimSpace(sp.PlaylistName))
	candidates := []string{
		s.cfg.ResultJSONPath,
		filepath.Join(s.cfg.OutputDir, fmt.Sprintf("spotify_to_ytmusic_%s_result.json", slug)),
		filepath.Join(s.cfg.OutputDir, fmt.Sprintf("ytmusic_%s_result.json", slug)),
	}

	for _, p := range candidates {
		if p == "" {
			continue
		}
		if data, err := os.ReadFile(p); err == nil {
			var prevResult model.SyncResult
			if err := json.Unmarshal(data, &prevResult); err == nil {
				for _, item := range prevResult.AddedAfterReview {
					if item.Index > 0 && item.TargetTrackID != "" {
						mapping[item.Index] = item.TargetTrackID
					}
				}
			}
		}
	}

	return mapping
}

func isAlphanumericID(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// waitForPlaylistWithTrackCount repeatedly polls fetch() with exponential back-off
// until the returned playlist has at least expectedMin tracks or maxAttempts is reached.
func waitForPlaylistWithTrackCount[T any](expectedMin int, maxAttempts int, initialDelay time.Duration, fetch func() (T, error), getLen func(T) int) (T, bool) {
	var empty T
	delay := initialDelay
	for attempt := 0; attempt < maxAttempts; attempt++ {
		time.Sleep(delay)
		delay *= 2
		if pl, err := fetch(); err == nil && getLen(pl) >= expectedMin {
			return pl, true
		}
	}
	return empty, false
}

// saveSyncArtifacts saves the canonical sync result and report JSON files atomically
func saveSyncArtifacts(result *model.SyncResult, resultPath, reportPath, outputDir, fallbackSlug, fromPlatform, toPlatform string) {
	slug := strings.ToLower(strings.TrimSpace(fallbackSlug))
	if slug == "" {
		slug = "playlist"
	}
	from := strings.ToLower(strings.TrimSpace(fromPlatform))
	to := strings.ToLower(strings.TrimSpace(toPlatform))
	if from == "" {
		from = "spotify"
	}
	if to == "" {
		to = "ytmusic"
	}
	if resultPath == "" {
		resultPath = filepath.Join(outputDir, fmt.Sprintf("%s_to_%s_%s_result.json", from, to, slug))
	}
	if reportPath == "" {
		reportPath = filepath.Join(outputDir, fmt.Sprintf("%s_to_%s_%s_report.json", from, to, slug))
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	_ = writeFileAtomic(resultPath, append(data, '\n'), 0644)
	_ = writeFileAtomic(reportPath, append(data, '\n'), 0644)
}

type ytmCandidateResolution struct {
	track             model.SpotifyTrack
	bestCandidate     *model.YTMSearchResult
	bestScore         int
	totalCandidates   int
	topCandidateTitle string
	topCandidateScore int
	topOptions        []ReviewOption
}

func (s *Syncer) resolveYTMCandidate(track model.SpotifyTrack) ytmCandidateResolution {
	queries := GenerateSearchQueries(track)
	var allCandidates []model.YTMSearchResult
	var bestCandidate *model.YTMSearchResult
	bestScore := 0
	seenVideoIDs := make(map[string]bool)

	for _, q := range queries {
		cands, err := s.yt.SearchSong(q)
		if err != nil || len(cands) == 0 {
			continue
		}
		for i := range cands {
			if seenVideoIDs[cands[i].VideoID] {
				continue
			}
			seenVideoIDs[cands[i].VideoID] = true
			score := CalculateScore(track, cands[i])
			cands[i].Score = score
			allCandidates = append(allCandidates, cands[i])
			if score > bestScore {
				bestScore = score
				candCopy := cands[i]
				bestCandidate = &candCopy
			}
		}
		// Tier 1: Confidence threshold met (>= 70) -> stop searching immediately to save API calls
		if bestScore >= ConfidenceThreshold {
			break
		}
	}

	topTitle := ""
	topScore := 0
	if bestCandidate != nil {
		topTitle = bestCandidate.Title
		topScore = bestScore
	} else if len(allCandidates) > 0 {
		topTitle = allCandidates[0].Title
		topScore = CalculateScore(track, allCandidates[0])
	}

	var topOptions []ReviewOption
	if bestScore < ConfidenceThreshold && len(allCandidates) > 0 {
		sortedCands := append([]model.YTMSearchResult(nil), allCandidates...)
		sortCandidatesByScore(sortedCands)
		limit := 3
		if len(sortedCands) < limit {
			limit = len(sortedCands)
		}
		for i := 0; i < limit; i++ {
			topOptions = append(topOptions, ReviewOption{
				TargetID:  sortedCands[i].VideoID,
				TargetURI: sortedCands[i].VideoID,
				Title:     sortedCands[i].Title,
				Artists:   sortedCands[i].Artists,
				Duration:  sortedCands[i].Duration,
				Score:     sortedCands[i].Score,
				TargetURL: fmt.Sprintf("https://music.youtube.com/watch?v=%s", sortedCands[i].VideoID),
			})
		}
	}

	return ytmCandidateResolution{
		track:             track,
		bestCandidate:     bestCandidate,
		bestScore:         bestScore,
		totalCandidates:   len(allCandidates),
		topCandidateTitle: topTitle,
		topCandidateScore: topScore,
		topOptions:        topOptions,
	}
}

func sortCandidatesByScore(cands []model.YTMSearchResult) {
	for i := 0; i < len(cands)-1; i++ {
		for j := i + 1; j < len(cands); j++ {
			if cands[j].Score > cands[i].Score {
				cands[i], cands[j] = cands[j], cands[i]
			}
		}
	}
}

func (s *Syncer) resolveMissingYTMCandidatesConcurrently(missingTracks []model.SpotifyTrack, concurrency int) []ytmCandidateResolution {
	if len(missingTracks) == 0 {
		return nil
	}
	if concurrency <= 0 {
		concurrency = DefaultSearchConcurrency
	}
	if concurrency > len(missingTracks) {
		concurrency = len(missingTracks)
	}

	results := make([]ytmCandidateResolution, len(missingTracks))
	type workItem struct {
		slot  int
		track model.SpotifyTrack
	}

	workCh := make(chan workItem, len(missingTracks))
	for i, tr := range missingTracks {
		workCh <- workItem{slot: i, track: tr}
	}
	close(workCh)

	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range workCh {
				results[item.slot] = s.resolveYTMCandidate(item.track)
			}
		}()
	}
	wg.Wait()
	return results
}

type spCandidateResolution struct {
	track           model.YTMTrack
	index           int
	bestCandidate   *model.SpotifyTrack
	bestScore       int
	totalCandidates int
	topOptions      []ReviewOption
}

func resolveSpotifyCandidate(spClient *spotify.Client, yTrack model.YTMTrack, index int) spCandidateResolution {
	queries := generateYTMToSpotifyQueries(yTrack)
	var bestCand *model.SpotifyTrack
	bestScore := 0
	var allCandidates []model.SpotifyTrack
	seenURIs := make(map[string]bool)

	for _, q := range queries {
		cands, searchErr := spClient.SearchTrack(q)
		if searchErr != nil || len(cands) == 0 {
			continue
		}
		for _, cand := range cands {
			trackKey := cand.ID
			if trackKey == "" {
				trackKey = cand.SpotifyURI
			}
			if trackKey != "" && seenURIs[trackKey] {
				continue
			}
			if trackKey != "" {
				seenURIs[trackKey] = true
			}
			score := CalculateTrackScore(yTrack.Title, yTrack.Artists, yTrack.Duration, cand.Title, cand.Artists, cand.Duration)
			allCandidates = append(allCandidates, cand)
			if score > bestScore {
				bestScore = score
				cCopy := cand
				bestCand = &cCopy
			}
		}
		// Tier 1: Confidence threshold met (>= 70) -> stop searching
		if bestScore >= ConfidenceThreshold {
			break
		}
	}

	var topOptions []ReviewOption
	if bestScore < ConfidenceThreshold && len(allCandidates) > 0 {
		type scoredSpotify struct {
			track model.SpotifyTrack
			score int
		}
		var scoredList []scoredSpotify
		for _, c := range allCandidates {
			s := CalculateTrackScore(yTrack.Title, yTrack.Artists, yTrack.Duration, c.Title, c.Artists, c.Duration)
			scoredList = append(scoredList, scoredSpotify{track: c, score: s})
		}
		for i := 0; i < len(scoredList)-1; i++ {
			for j := i + 1; j < len(scoredList); j++ {
				if scoredList[j].score > scoredList[i].score {
					scoredList[i], scoredList[j] = scoredList[j], scoredList[i]
				}
			}
		}
		limit := 3
		if len(scoredList) < limit {
			limit = len(scoredList)
		}
		for i := 0; i < limit; i++ {
			optTrack := scoredList[i].track
			uri := optTrack.SpotifyURI
			if uri == "" && optTrack.ID != "" {
				uri = fmt.Sprintf("spotify:track:%s", optTrack.ID)
			}
			topOptions = append(topOptions, ReviewOption{
				TargetID:  optTrack.ID,
				TargetURI: uri,
				Title:     optTrack.Title,
				Artists:   optTrack.Artists,
				Duration:  optTrack.Duration,
				Score:     scoredList[i].score,
				TargetURL: fmt.Sprintf("https://open.spotify.com/track/%s", optTrack.ID),
			})
		}
	}

	return spCandidateResolution{
		track:           yTrack,
		index:           index,
		bestCandidate:   bestCand,
		bestScore:       bestScore,
		totalCandidates: len(allCandidates),
		topOptions:      topOptions,
	}
}

func resolveMissingSpotifyCandidatesConcurrently(spClient *spotify.Client, unmatchedTracks []struct {
	index int
	track model.YTMTrack
}, concurrency int) []spCandidateResolution {
	if len(unmatchedTracks) == 0 {
		return nil
	}
	if concurrency <= 0 {
		concurrency = DefaultSearchConcurrency
	}
	if concurrency > len(unmatchedTracks) {
		concurrency = len(unmatchedTracks)
	}

	results := make([]spCandidateResolution, len(unmatchedTracks))
	type workItem struct {
		slot  int
		index int
		track model.YTMTrack
	}

	workCh := make(chan workItem, len(unmatchedTracks))
	for slot, item := range unmatchedTracks {
		workCh <- workItem{slot: slot, index: item.index, track: item.track}
	}
	close(workCh)

	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range workCh {
				results[item.slot] = resolveSpotifyCandidate(spClient, item.track, item.index)
			}
		}()
	}
	wg.Wait()
	return results
}
