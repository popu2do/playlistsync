package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"playlistsync/internal/auth"
	"playlistsync/internal/config"
	"playlistsync/internal/engine"
	"playlistsync/internal/report"
	"playlistsync/internal/spotify"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

const (
	DefaultYTMAuthPath     = auth.DefaultYTMAuthPath
	DefaultSpotifyAuthPath = auth.DefaultSpotifyAuthPath
)

var (
	stdinReader      io.Reader = os.Stdin
	illegalPathChars           = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]`)
	ytVideoIDRegex             = regexp.MustCompile(`^[a-zA-Z0-9_-]{11}$`)
	spTrackIDRegex             = regexp.MustCompile(`^[a-zA-Z0-9]{22}$`)
	customIDRegex              = regexp.MustCompile(`^[a-zA-Z0-9_-]{10,64}$`)
	isTerminalFunc             = func() bool {
		stat, err := os.Stdin.Stat()
		if err != nil {
			return false
		}
		return (stat.Mode() & os.ModeCharDevice) != 0
	}
)

// extractTargetID parses raw IDs or full URLs from YouTube / YouTube Music / Spotify
func extractTargetID(input string) (string, bool) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return "", false
	}

	// 1. Direct platform ID matches
	if ytVideoIDRegex.MatchString(raw) {
		return raw, true
	}
	if spTrackIDRegex.MatchString(raw) {
		return raw, true
	}

	// 2. Spotify URI match: spotify:track:4cOdK2wGLETKBW3PvgPWqT
	if strings.HasPrefix(raw, "spotify:track:") {
		id := strings.TrimPrefix(raw, "spotify:track:")
		if spTrackIDRegex.MatchString(id) {
			return id, true
		}
		return "", false
	}

	// 3. URL match (YouTube / YTM / Spotify)
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		if strings.Contains(u.Host, "youtube.com") {
			v := u.Query().Get("v")
			if ytVideoIDRegex.MatchString(v) {
				return v, true
			}
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			for i, p := range parts {
				if (p == "shorts" || p == "v" || p == "embed") && i+1 < len(parts) {
					id := strings.Split(parts[i+1], "?")[0]
					if ytVideoIDRegex.MatchString(id) {
						return id, true
					}
				}
			}
		}
		if strings.Contains(u.Host, "youtu.be") {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(parts) > 0 && ytVideoIDRegex.MatchString(parts[0]) {
				return parts[0], true
			}
		}
		if strings.Contains(u.Host, "spotify.com") {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			for i, p := range parts {
				if p == "track" && i+1 < len(parts) {
					trackID := strings.Split(parts[i+1], "?")[0]
					if spTrackIDRegex.MatchString(trackID) {
						return trackID, true
					}
				}
			}
		}
	}

	// 4. Custom/Mock target track ID fallback
	if customIDRegex.MatchString(raw) {
		return raw, true
	}

	return "", false
}

func formatArtistList(artists []string) string {
	if len(artists) == 0 {
		return "Unknown Artist"
	}
	return strings.Join(artists, ", ")
}

func sanitizeSlug(name string) string {
	cleaned := strings.TrimSpace(name)
	cleaned = filepath.Base(cleaned)
	cleaned = strings.TrimSuffix(strings.TrimSuffix(cleaned, ".json"), "_playlist")
	cleaned = illegalPathChars.ReplaceAllString(cleaned, "_")
	cleaned = strings.ReplaceAll(cleaned, "..", "_")
	cleaned = strings.TrimSpace(strings.ToLower(cleaned))
	if cleaned == "" || cleaned == "." || cleaned == ".." {
		return "unnamed_playlist"
	}
	return cleaned
}

func promptConfirm(prompt string, autoYes bool) bool {
	if autoYes || !isTerminalFunc() {
		return true
	}
	fmt.Printf("%s [y/N]: ", prompt)
	reader := bufio.NewReader(stdinReader)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes"
}

func promptReview(item engine.ReviewItem) (string, bool, bool) {
	fmt.Printf("\n--- [Interactive Review (#%d)] -------------------------------------------\n", item.SourceIndex)
	durStr := ""
	if item.SourceDuration != "" {
		durStr = fmt.Sprintf(" [%s]", item.SourceDuration)
	}
	fmt.Printf("Source: 《%s》 - %s%s\n", item.SourceTitle, formatArtistList(item.SourceArtists), durStr)
	if item.SourceURL != "" {
		icon := "🎧 Source"
		if item.SourcePlatform == "spotify" {
			icon = "🎧 Spotify"
		} else if item.SourcePlatform == "youtube-music" || item.SourcePlatform == "ytmusic" {
			icon = "🎧 YouTube Music"
		}
		fmt.Printf("%s: %s\n", icon, item.SourceURL)
	}

	fmt.Println("\nCandidates:")
	if len(item.Options) == 0 {
		fmt.Println("  (No candidates available)")
	} else {
		for i, opt := range item.Options {
			dur := opt.Duration
			if dur == "" {
				dur = "--:--"
			}
			fmt.Printf("  [%d] %s - %s [%s] (Score: %d)\n",
				i+1, opt.Title, formatArtistList(opt.Artists), dur, opt.Score)
			if opt.TargetURL != "" {
				fmt.Printf("      🔗 %s\n", opt.TargetURL)
			}
		}
	}

	reader := bufio.NewReader(stdinReader)

	// Robust input validation loop: reprompt on invalid input instead of silent fallthrough
	for {
		if len(item.Options) > 0 {
			fmt.Printf("\nAction: [1-%d] Select | [s] Skip (default) | [c] Custom ID | [a] Skip all: ", len(item.Options))
		} else {
			fmt.Print("\nAction: [s] Skip (default) | [c] Custom ID | [a] Skip all: ")
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			return "", false, true
		}
		line = strings.TrimSpace(line)

		// 1. Default action / Skip
		if line == "" || strings.EqualFold(line, "s") || strings.EqualFold(line, "skip") {
			return "", false, false
		}

		// 2. Skip all / Abort
		if strings.EqualFold(line, "a") || strings.EqualFold(line, "all") || strings.EqualFold(line, "q") || strings.EqualFold(line, "quit") {
			return "", false, true
		}

		// 3. Custom ID workflow with sub-loop & cancellation
		if strings.EqualFold(line, "c") || strings.EqualFold(line, "custom") {
			for {
				fmt.Print("Enter custom Track/Video ID or URL (or press Enter/'cancel' to return): ")
				customLine, cErr := reader.ReadString('\n')
				if cErr != nil {
					return "", false, true
				}
				customLine = strings.TrimSpace(customLine)
				if customLine == "" || strings.EqualFold(customLine, "cancel") || strings.EqualFold(customLine, "back") {
					fmt.Println("Cancelled custom entry.")
					break
				}
				if id, ok := extractTargetID(customLine); ok {
					return id, true, false
				}
				fmt.Printf("⚠️  Invalid ID/URL format: %q. Please enter a valid ID or link.\n", customLine)
			}
			continue
		}

		// 4. Candidate selection [1..N]
		if len(item.Options) > 0 {
			if choice, parseErr := strconv.Atoi(line); parseErr == nil {
				if choice >= 1 && choice <= len(item.Options) {
					return item.Options[choice-1].TargetID, true, false
				}
			}
		}

		// 5. Invalid Input notification -> Loop and prompt again
		if len(item.Options) > 0 {
			fmt.Printf("⚠️  Invalid choice %q. Please enter 1-%d, 's' to skip, 'c' for custom ID, or 'a' to skip all.\n", line, len(item.Options))
		} else {
			fmt.Printf("⚠️  Invalid choice %q. Please enter 's' to skip, 'c' for custom ID, or 'a' to skip all.\n", line)
		}
	}
}

func normalizePlatform(input string) string {
	return string(auth.NormalizePlatform(input))
}

type playlistSpec struct {
	Platform string
	Resource string
}

func parsePlaylistSpec(arg string) (playlistSpec, bool) {
	s := strings.TrimSpace(arg)
	if s == "" {
		return playlistSpec{}, false
	}

	// 1. Check URL patterns
	if strings.Contains(s, "open.spotify.com/playlist/") || strings.Contains(s, "spotify:playlist:") {
		return playlistSpec{Platform: "spotify", Resource: s}, true
	}
	if strings.Contains(s, "music.youtube.com/playlist") || strings.Contains(s, "youtube.com/playlist") {
		return playlistSpec{Platform: "youtube-music", Resource: s}, true
	}

	// 2. Check platform prefix notation (e.g. spotify:"My List", sp:37i9..., ytm:, youtube-music:PLxxx)
	if idx := strings.Index(s, ":"); idx != -1 {
		prefix := strings.ToLower(s[:idx])
		val := strings.Trim(strings.TrimSpace(s[idx+1:]), "\"'")
		norm := normalizePlatform(prefix)
		if norm == "spotify" || norm == "youtube-music" {
			return playlistSpec{Platform: norm, Resource: val}, true
		}
	}

	return playlistSpec{}, false
}

func resolveArtifactPaths(name string, customOutDir ...string) (spPath, resPath, repPath string) {
	slug := sanitizeSlug(name)
	outDir := config.GetOutputDir()
	if len(customOutDir) > 0 && customOutDir[0] != "" {
		outDir = customOutDir[0]
	}
	if outDir == "" {
		outDir = config.DefaultOutputDir
	}

	if _, foundPath, err := spotify.FindPlaylistByName(outDir, slug); err == nil {
		spPath = foundPath
	} else {
		spPath = filepath.Join(outDir, fmt.Sprintf("spotify_%s_source.json", slug))
	}

	// Dynamic discovery: search for any directional result matching "*_to_*_<slug>_result.json"
	targetSuffix := fmt.Sprintf("_%s_result.json", slug)
	if entries, err := os.ReadDir(outDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			fname := entry.Name()
			if strings.Contains(fname, "_to_") && strings.HasSuffix(strings.ToLower(fname), strings.ToLower(targetSuffix)) {
				res := filepath.Join(outDir, fname)
				repName := strings.TrimSuffix(fname, "_result.json") + "_report.json"
				rep := filepath.Join(outDir, repName)
				return spPath, res, rep
			}
		}
	}

	// Default canonical path: spotify_to_ytmusic
	defaultRes := filepath.Join(outDir, fmt.Sprintf("spotify_to_ytmusic_%s_result.json", slug))
	defaultRep := filepath.Join(outDir, fmt.Sprintf("spotify_to_ytmusic_%s_report.json", slug))
	return spPath, defaultRes, defaultRep
}

func printUsage() {
	fmt.Println("============================================================")
	fmt.Println("  playlistsync - Universal Playlist Synchronization CLI")
	fmt.Println("============================================================")
	fmt.Println("\nUsage:")
	fmt.Println("  playlistsync <command> [arguments] [options]")
	fmt.Println("\nCommands:")
	fmt.Println("  sync [source_spec] [target_spec]   Synchronize playlist across platforms")
	fmt.Println("                                     Supported formats: specifier (spotify:\"name\" ytm:), URL, or flags")
	fmt.Println("                                     Flags: --from, --to, --source, --target, --sync-order, --clean-extra, --concurrency, -y, --proxy")
	fmt.Println("                                     Supported aliases: spotify/spo/sp, youtube-music/ytmusic/youtube/ytm/yt")
	fmt.Println("  login [platform]                   Authenticate platform credentials (spotify | youtube-music | all)")
	fmt.Println("  inspect <playlist_name>            Display migration summary and track status (alias: status, summary)")
	fmt.Println("  verify <playlist_name>             Run invariant integrity checks (alias: validate)")
	fmt.Println("  report <playlist_name>             Regenerate canonical migration report")
	fmt.Println("\nExamples:")
	fmt.Println("  playlistsync sync spotify:\"My Playlist\" ytm:")
	fmt.Println("  playlistsync sync ytm:\"My Playlist\" spotify:")
	fmt.Println("  playlistsync sync https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M ytm:")
	fmt.Println("  playlistsync sync <playlist_name> --from=spotify --to=youtube-music")
	fmt.Println("  playlistsync sync <playlist_name> --from=youtube-music --to=spotify")
	fmt.Println("  playlistsync sync spotify:\"My Playlist\" ytm: -y --proxy=http://127.0.0.1:7890")
	fmt.Println("  playlistsync login all")
	fmt.Println("  playlistsync inspect <playlist_name>")
	fmt.Println("  playlistsync verify <playlist_name>")
}

func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "playlistsync",
		Short: "Universal Playlist Synchronization CLI",
		Long: `============================================================
  playlistsync - Universal Playlist Synchronization CLI
============================================================

Synchronize playlists between Spotify and YouTube Music.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			printUsage()
			return nil
		},
	}

	// 1. sync command
	var (
		syncName           string
		syncJSONPath       string
		syncFrom           string
		syncTo             string
		syncProxy          string
		syncCleanExtra     bool
		syncSyncOrder      bool
		syncToID           string
		syncTargetID       string
		syncTarget         string
		syncFromID         string
		syncSourceID       string
		syncSource         string
		syncPlaylistID     string
		syncID             string
		syncAutoYes        bool
		syncNonInteractive bool
		syncOutputDir      string
		syncConcurrency    int
	)

	syncCmd := &cobra.Command{
		Use:   "sync [source_spec] [target_spec]",
		Short: "Synchronize playlist across platforms",
		Long: `Synchronize playlist across platforms.
Supported formats:
  - Specifier notation: playlistsync sync spotify:"My Playlist" ytm:
  - URL notation:       playlistsync sync https://open.spotify.com/playlist/xxx ytm:
  - Flag notation:      playlistsync sync "My Playlist" --from=spotify --to=youtube-music
Supported aliases: spotify/spo/sp, youtube-music/ytmusic/youtube/ytm/yt`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var srcSpec, dstSpec playlistSpec
			var srcParsed, dstParsed bool

			if len(args) > 0 {
				srcSpec, srcParsed = parsePlaylistSpec(args[0])
			}
			if len(args) > 1 {
				dstSpec, dstParsed = parsePlaylistSpec(args[1])
			}

			// Resolve source platform and resource
			finalFrom := syncFrom
			finalSource := syncSource
			if finalSource == "" {
				finalSource = syncSourceID
			}
			if finalSource == "" {
				finalSource = syncFromID
			}
			if finalSource == "" {
				finalSource = syncName
			}

			if srcParsed {
				if finalFrom == "" {
					finalFrom = srcSpec.Platform
				}
				if finalSource == "" {
					finalSource = srcSpec.Resource
				}
			} else if len(args) > 0 && finalSource == "" {
				finalSource = strings.TrimSpace(args[0])
			}

			// Resolve target platform and resource
			finalTo := syncTo
			finalTarget := syncTarget
			if finalTarget == "" {
				finalTarget = syncToID
			}
			if finalTarget == "" {
				finalTarget = syncTargetID
			}
			if finalTarget == "" {
				finalTarget = syncPlaylistID
			}
			if finalTarget == "" {
				finalTarget = syncID
			}

			if dstParsed {
				if finalTo == "" {
					finalTo = dstSpec.Platform
				}
				if finalTarget == "" {
					finalTarget = dstSpec.Resource
				}
			} else if len(args) > 1 && finalTarget == "" {
				finalTarget = strings.TrimSpace(args[1])
			}

			playlistName := finalSource
			if strings.TrimSpace(playlistName) == "" && syncJSONPath == "" {
				fmt.Fprintln(os.Stderr, "Error: missing required playlist name or source.")
				fmt.Fprintln(os.Stderr, "Usage examples:")
				fmt.Fprintln(os.Stderr, "  playlistsync sync spotify:\"My Playlist\" ytm:")
				fmt.Fprintln(os.Stderr, "  playlistsync sync \"My Playlist\" --from=spotify --to=youtube-music")
				return errors.New("missing required playlist name")
			}

			if strings.TrimSpace(finalFrom) == "" || strings.TrimSpace(finalTo) == "" {
				fmt.Fprintln(os.Stderr, "Error: missing required platforms --from and --to.")
				fmt.Fprintln(os.Stderr, "Both source and destination platforms must be specified (via flags or specifier notation).")
				fmt.Fprintln(os.Stderr, "Examples:")
				fmt.Fprintln(os.Stderr, "  playlistsync sync spotify:\"My Playlist\" ytm:")
				fmt.Fprintln(os.Stderr, "  playlistsync sync \"My Playlist\" --from=spotify --to=youtube-music")
				fmt.Fprintln(os.Stderr, "  playlistsync sync \"My Playlist\" --from=youtube-music --to=spotify")
				return errors.New("missing required --from and --to platforms")
			}

			proxyURL := syncProxy
			if proxyURL == "" {
				proxyURL = auth.DetectSystemProxy()
			}

			autoYes := syncAutoYes || syncNonInteractive

			normFrom := normalizePlatform(finalFrom)
			normTo := normalizePlatform(finalTo)

			var dir engine.SyncDirection
			switch {
			case normFrom == "spotify" && normTo == "youtube-music":
				dir = engine.DirectionSpotifyToYouTube
			case normFrom == "youtube-music" && normTo == "spotify":
				dir = engine.DirectionYouTubeToSpotify
			case normFrom == normTo:
				fmt.Fprintf(os.Stderr, "Error: source and destination platforms cannot be identical (%s)\n", normFrom)
				return errors.New("source and destination platforms cannot be identical")
			default:
				fmt.Fprintf(os.Stderr, "Error: unsupported sync direction: %s -> %s (supported: spotify <-> youtube-music)\n", normFrom, normTo)
				return fmt.Errorf("unsupported sync direction: %s -> %s", normFrom, normTo)
			}

			targetPlaylistID := finalTarget

			outDir := config.GetOutputDir()
			if syncOutputDir != "" {
				outDir = syncOutputDir
			}

			ytmAuthPath := filepath.Join(outDir, config.DefaultAuthDirName, config.DefaultYTMAuthName)
			if _, err := os.Stat(ytmAuthPath); err != nil {
				if _, defErr := os.Stat(auth.DefaultYTMAuthPath); defErr == nil {
					ytmAuthPath = auth.DefaultYTMAuthPath
				}
			}
			spAuthPath := filepath.Join(outDir, config.DefaultAuthDirName, config.DefaultSpotifyAuthName)
			if _, err := os.Stat(spAuthPath); err != nil {
				if _, defErr := os.Stat(auth.DefaultSpotifyAuthPath); defErr == nil {
					spAuthPath = auth.DefaultSpotifyAuthPath
				}
			}

			cleanExtra := config.GlobalConfig.CleanExtra
			if cmd.Flags().Changed("clean-extra") {
				cleanExtra = syncCleanExtra
			}

			var reviewPrompt engine.ReviewPromptFunc
			if !autoYes && isTerminalFunc() {
				reviewPrompt = promptReview
			}

			cfg := engine.SyncConfig{
				PlaylistName:     playlistName,
				Direction:        dir,
				PlaylistID:       targetPlaylistID,
				OutputDir:        outDir,
				YTMHeadersPath:   ytmAuthPath,
				SpotifyAuthPath:  spAuthPath,
				ProxyURL:         proxyURL,
				CleanExtra:       cleanExtra,
				SyncOrder:        syncSyncOrder,
				AutoYes:          autoYes,
				Concurrency:      syncConcurrency,
				PlaylistJSONPath: syncJSONPath,
				ConfirmPrompt: func(prompt string) bool {
					return promptConfirm(prompt, autoYes)
				},
				ReviewPrompt: reviewPrompt,
			}

			syncer, err := engine.NewSyncer(cfg)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Initialization error: %s\n", auth.SanitizeSensitive(err.Error()))
				return err
			}

			res, err := syncer.Run()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Sync execution failed: %s\n", auth.SanitizeSensitive(err.Error()))
				return err
			}

			fmt.Printf("\n[RESULT REPORT] Playlist: %s | Total: %d | Added: %d | Skipped: %d | Extra Removed: %d | URL: %s\n",
				res.Title, res.TotalSourceTracks, res.AddedTracks, res.SkippedTracks, len(res.RemovedExtraTracks), res.PlaylistURL)
			return nil
		},
	}

	syncCmd.Flags().StringVar(&syncJSONPath, "json", "", "Load Spotify playlist directly from JSON file")
	syncCmd.Flags().StringVar(&syncName, "name", "", "Playlist name")
	syncCmd.Flags().StringVar(&syncSource, "source", "", "Source playlist name, ID, or URL")
	syncCmd.Flags().StringVar(&syncTarget, "target", "", "Target playlist name, ID, or URL")
	syncCmd.Flags().StringVar(&syncFrom, "from", "", "Source platform: spotify | youtube-music")
	syncCmd.Flags().StringVar(&syncTo, "to", "", "Target platform: youtube-music | spotify")
	syncCmd.Flags().StringVar(&syncProxy, "proxy", "", "Proxy URL (defaults to system proxy)")
	syncCmd.Flags().BoolVar(&syncCleanExtra, "clean-extra", true, "Remove extraneous tracks in destination")
	syncCmd.Flags().BoolVar(&syncSyncOrder, "sync-order", true, "Synchronize track sequence/order between source and target playlists")
	syncCmd.Flags().StringVar(&syncToID, "to-id", "", "Destination playlist ID (alias for --target)")
	syncCmd.Flags().StringVar(&syncTargetID, "target-id", "", "Destination playlist ID (alias for --target)")
	syncCmd.Flags().StringVar(&syncFromID, "from-id", "", "Source playlist ID (alias for --source)")
	syncCmd.Flags().StringVar(&syncSourceID, "source-id", "", "Source playlist ID (alias for --source)")
	syncCmd.Flags().StringVar(&syncPlaylistID, "playlist-id", "", "Destination playlist ID (legacy alias for --target)")
	syncCmd.Flags().StringVar(&syncID, "id", "", "Destination playlist ID (short legacy alias for --target)")
	syncCmd.Flags().BoolVarP(&syncAutoYes, "yes", "y", false, "Automatic yes to prompts")
	syncCmd.Flags().BoolVar(&syncNonInteractive, "non-interactive", false, "Run in non-interactive mode")
	syncCmd.Flags().IntVarP(&syncConcurrency, "concurrency", "c", engine.DefaultSearchConcurrency, "Number of concurrent search workers")
	syncCmd.Flags().StringVar(&syncOutputDir, "output-dir", "", "Custom output directory")

	rootCmd.AddCommand(syncCmd)

	// 2. login command
	var (
		loginForce bool
		loginProxy string
	)
	loginCmd := &cobra.Command{
		Use:           "login [platform]",
		Short:         "Authenticate platform credentials (spotify | youtube-music | all)",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			targetPlatform := auth.PlatformAll
			if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
				norm := auth.NormalizePlatform(args[0])
				if norm != auth.PlatformSpotify && norm != auth.PlatformYouTube && norm != auth.PlatformAll {
					fmt.Fprintf(os.Stderr, "[AUTH FAILED] unsupported platform: %s\n", args[0])
					return fmt.Errorf("unsupported platform: %s", args[0])
				}
				targetPlatform = norm
			}

			proxy := loginProxy
			if proxy == "" {
				proxy = auth.DetectSystemProxy()
			}

			opts := []auth.Option{
				auth.WithForce(loginForce),
			}
			if proxy != "" {
				opts = append(opts, auth.WithProxy(proxy))
			}

			if _, err := auth.LoginPlatform(targetPlatform, opts...); err != nil {
				return err
			}
			return nil
		},
	}
	loginCmd.Flags().BoolVar(&loginForce, "force", false, "Force re-authentication bypassing cached validation")
	loginCmd.Flags().StringVar(&loginProxy, "proxy", "", "Proxy URL")
	rootCmd.AddCommand(loginCmd)

	// 3. inspect command
	var inspectOutputDir string
	inspectCmd := &cobra.Command{
		Use:           "inspect <playlist_name>",
		Aliases:       []string{"status", "summary"},
		Short:         "Display migration summary and track status",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 || strings.TrimSpace(args[0]) == "" {
				fmt.Fprintln(os.Stderr, "Error: missing playlist name.")
				fmt.Fprintln(os.Stderr, "Usage: playlistsync inspect <playlist_name>")
				return errors.New("missing playlist name")
			}
			spPath, resPath, _ := resolveArtifactPaths(args[0], inspectOutputDir)
			if err := report.Summarize(spPath, resPath); err != nil {
				fmt.Fprintf(os.Stderr, "Inspect error: %v\n", err)
				return err
			}
			return nil
		},
	}
	inspectCmd.Flags().StringVar(&inspectOutputDir, "output-dir", "", "Custom output directory")
	rootCmd.AddCommand(inspectCmd)

	// 4. verify command
	var verifyOutputDir string
	verifyCmd := &cobra.Command{
		Use:           "verify <playlist_name>",
		Aliases:       []string{"validate"},
		Short:         "Run invariant integrity checks",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 || strings.TrimSpace(args[0]) == "" {
				fmt.Fprintln(os.Stderr, "Error: missing playlist name.")
				fmt.Fprintln(os.Stderr, "Usage: playlistsync verify <playlist_name>")
				return errors.New("missing playlist name")
			}
			spPath, resPath, repPath := resolveArtifactPaths(args[0], verifyOutputDir)
			if err := report.Validate(spPath, resPath, repPath); err != nil {
				fmt.Fprintf(os.Stderr, "Verification error: %v\n", err)
				return err
			}
			return nil
		},
	}
	verifyCmd.Flags().StringVar(&verifyOutputDir, "output-dir", "", "Custom output directory")
	rootCmd.AddCommand(verifyCmd)

	// 5. report command
	var reportOutputDir string
	reportCmd := &cobra.Command{
		Use:           "report <playlist_name>",
		Short:         "Regenerate canonical migration report",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 || strings.TrimSpace(args[0]) == "" {
				fmt.Fprintln(os.Stderr, "Error: missing playlist name.")
				fmt.Fprintln(os.Stderr, "Usage: playlistsync report <playlist_name>")
				return errors.New("missing playlist name")
			}
			_, resPath, repPath := resolveArtifactPaths(args[0], reportOutputDir)
			if err := report.GenerateReport(resPath, repPath); err != nil {
				fmt.Fprintf(os.Stderr, "Report error: %v\n", err)
				return err
			}
			return nil
		},
	}
	reportCmd.Flags().StringVar(&reportOutputDir, "output-dir", "", "Custom output directory")
	rootCmd.AddCommand(reportCmd)

	return rootCmd
}

func run(args []string) int {
	if len(args) == 0 {
		printUsage()
		return 0
	}

	first := strings.ToLower(args[0])
	if first == "help" || first == "--help" || first == "-h" {
		printUsage()
		return 0
	}

	rootCmd := newRootCmd()
	rootCmd.SetArgs(args)
	if err := rootCmd.Execute(); err != nil {
		return 1
	}
	return 0
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}
	os.Exit(run(os.Args[1:]))
}
