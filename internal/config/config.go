package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Default paths and values
const (
	DefaultOutputDir       = "output"
	DefaultAuthDirName     = "auth"
	DefaultSpotifyAuthName = "spotify_credentials.json"
	DefaultYTMAuthName     = "ytmusic_credentials.json"
	DefaultCDPPort         = 9222
	DefaultTimeout         = 30 * time.Second
	DefaultLoginTimeout    = 3 * time.Minute
	DefaultBatchDelay      = 300 * time.Millisecond
	DefaultBatchSize       = 20
	DefaultConfidenceScore = 70
)

var (
	configMu sync.RWMutex
	// GlobalConfig is the singleton configuration instance
	GlobalConfig = NewDefaultConfig()
)

// AppConfig represents global application configuration
type AppConfig struct {
	OutputDir       string        `json:"outputDir"`
	AuthDir         string        `json:"authDir"`
	SpotifyAuthPath string        `json:"spotifyAuthPath"`
	YTMAuthPath     string        `json:"ytmAuthPath"`
	ProxyURL        string        `json:"proxyUrl"`
	CleanExtra      bool          `json:"cleanExtra"`
	ConfidenceScore int           `json:"confidenceScore"`
	RequestTimeout  time.Duration `json:"requestTimeout"`
	BatchDelay      time.Duration `json:"batchDelay"`
	BatchSize       int           `json:"batchSize"`
}

func envOrDefault(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func envBoolOrDefault(key string, fallback bool) bool {
	if val := os.Getenv(key); val != "" {
		if parsed, err := strconv.ParseBool(val); err == nil {
			return parsed
		}
	}
	return fallback
}

func envIntOrDefault(key string, fallback, min, max int) int {
	if val := os.Getenv(key); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed >= min && parsed <= max {
			return parsed
		}
	}
	return fallback
}

// NewDefaultConfig initializes configuration with environment variable fallbacks
func NewDefaultConfig() *AppConfig {
	outputDir := envOrDefault("PLAYLISTSYNC_OUTPUT_DIR", DefaultOutputDir)
	authDir := envOrDefault("PLAYLISTSYNC_AUTH_DIR", filepath.Join(outputDir, DefaultAuthDirName))
	spotifyAuth := envOrDefault("PLAYLISTSYNC_SPOTIFY_AUTH", filepath.Join(authDir, DefaultSpotifyAuthName))
	ytmAuth := envOrDefault("PLAYLISTSYNC_YTM_AUTH", filepath.Join(authDir, DefaultYTMAuthName))

	proxyURL := envOrDefault("PLAYLISTSYNC_PROXY", envOrDefault("HTTPS_PROXY", os.Getenv("HTTP_PROXY")))
	cleanExtra := envBoolOrDefault("PLAYLISTSYNC_CLEAN_EXTRA", true)
	confidenceScore := envIntOrDefault("PLAYLISTSYNC_CONFIDENCE_SCORE", DefaultConfidenceScore, 0, 100)

	return &AppConfig{
		OutputDir:       outputDir,
		AuthDir:         authDir,
		SpotifyAuthPath: spotifyAuth,
		YTMAuthPath:     ytmAuth,
		ProxyURL:        proxyURL,
		CleanExtra:      cleanExtra,
		ConfidenceScore: confidenceScore,
		RequestTimeout:  DefaultTimeout,
		BatchDelay:      DefaultBatchDelay,
		BatchSize:       DefaultBatchSize,
	}
}

// SetOutputDir updates output directory and adjusts dependent default paths if not explicitly customized
func (c *AppConfig) SetOutputDir(dir string) {
	if dir == "" {
		dir = DefaultOutputDir
	}
	wasDerived := c.AuthDir == "" || c.AuthDir == filepath.Join(c.OutputDir, DefaultAuthDirName)
	c.OutputDir = dir
	if wasDerived {
		c.AuthDir = filepath.Join(dir, DefaultAuthDirName)
		c.SpotifyAuthPath = filepath.Join(c.AuthDir, DefaultSpotifyAuthName)
		c.YTMAuthPath = filepath.Join(c.AuthDir, DefaultYTMAuthName)
	}
}

// SetAuthDir updates authentication directory and adjusts default credential paths
func (c *AppConfig) SetAuthDir(dir string) {
	if dir == "" {
		dir = filepath.Join(c.OutputDir, DefaultAuthDirName)
	}
	c.AuthDir = dir
	c.SpotifyAuthPath = filepath.Join(dir, DefaultSpotifyAuthName)
	c.YTMAuthPath = filepath.Join(dir, DefaultYTMAuthName)
}

// GetSpotifyProfileDir returns browser user-data-dir for Spotify login
func (c *AppConfig) GetSpotifyProfileDir() string {
	return filepath.Join(c.AuthDir, ".chrome_spotify")
}

// GetYTMProfileDir returns browser user-data-dir for YouTube Music login
func (c *AppConfig) GetYTMProfileDir() string {
	return filepath.Join(c.AuthDir, ".chrome_ytmusic")
}

func (c *AppConfig) formatPath(platform, name, kind string) string {
	slug := strings.ToLower(strings.TrimSpace(name))
	plat := strings.ToLower(strings.TrimSpace(platform))
	if plat == "" {
		plat = "spotify"
	}
	if kind == "source" || kind == "playlist" {
		return filepath.Join(c.OutputDir, fmt.Sprintf("%s_%s_source.json", plat, slug))
	}
	return filepath.Join(c.OutputDir, fmt.Sprintf("%s_%s_%s.json", plat, slug, kind))
}

// GetSourcePath returns the standardized source playlist JSON path for a platform and playlist name
func (c *AppConfig) GetSourcePath(platform, name string) string {
	return c.formatPath(platform, name, "source")
}

// GetPlaylistPath returns the standardized source playlist JSON path for a playlist name (defaults to spotify)
func (c *AppConfig) GetPlaylistPath(name string) string {
	return c.GetSourcePath("spotify", name)
}

// GetSpotifySourcePath returns standard Spotify source playlist path
func (c *AppConfig) GetSpotifySourcePath(name string) string {
	return c.GetSourcePath("spotify", name)
}

// GetYTMSourcePath returns standard YouTube Music source playlist path
func (c *AppConfig) GetYTMSourcePath(name string) string {
	return c.GetSourcePath("ytmusic", name)
}

// GetSyncResultPath returns the directional result JSON path across platforms
func (c *AppConfig) GetSyncResultPath(fromPlatform, toPlatform, name string) string {
	slug := strings.ToLower(strings.TrimSpace(name))
	from := strings.ToLower(strings.TrimSpace(fromPlatform))
	to := strings.ToLower(strings.TrimSpace(toPlatform))
	if from == "" {
		from = "spotify"
	}
	if to == "" {
		to = "ytmusic"
	}
	return filepath.Join(c.OutputDir, fmt.Sprintf("%s_to_%s_%s_result.json", from, to, slug))
}

// GetSyncReportPath returns the directional report JSON path across platforms
func (c *AppConfig) GetSyncReportPath(fromPlatform, toPlatform, name string) string {
	slug := strings.ToLower(strings.TrimSpace(name))
	from := strings.ToLower(strings.TrimSpace(fromPlatform))
	to := strings.ToLower(strings.TrimSpace(toPlatform))
	if from == "" {
		from = "spotify"
	}
	if to == "" {
		to = "ytmusic"
	}
	return filepath.Join(c.OutputDir, fmt.Sprintf("%s_to_%s_%s_report.json", from, to, slug))
}

// GetResultPath returns the standardized result JSON path for a target platform and playlist name
func (c *AppConfig) GetResultPath(platform, name string) string {
	plat := strings.ToLower(strings.TrimSpace(platform))
	if plat == "spotify" {
		return c.GetSyncResultPath("ytmusic", "spotify", name)
	}
	return c.GetSyncResultPath("spotify", "ytmusic", name)
}

// GetReportPath returns the standardized report JSON path for a target platform and playlist name
func (c *AppConfig) GetReportPath(platform, name string) string {
	plat := strings.ToLower(strings.TrimSpace(platform))
	if plat == "spotify" {
		return c.GetSyncReportPath("ytmusic", "spotify", name)
	}
	return c.GetSyncReportPath("spotify", "ytmusic", name)
}

// GetSpotifyResultPath returns directional result path with Spotify as destination (from YouTube Music)
func (c *AppConfig) GetSpotifyResultPath(name string) string {
	return c.GetSyncResultPath("ytmusic", "spotify", name)
}

// GetSpotifyReportPath returns directional report path with Spotify as destination (from YouTube Music)
func (c *AppConfig) GetSpotifyReportPath(name string) string {
	return c.GetSyncReportPath("ytmusic", "spotify", name)
}

// GetYTMResultPath returns directional result path with YouTube Music as destination (from Spotify)
func (c *AppConfig) GetYTMResultPath(name string) string {
	return c.GetSyncResultPath("spotify", "ytmusic", name)
}

// GetYTMReportPath returns directional report path with YouTube Music as destination (from Spotify)
func (c *AppConfig) GetYTMReportPath(name string) string {
	return c.GetSyncReportPath("spotify", "ytmusic", name)
}

// Global accessor and modifier helpers

// GetOutputDir returns the global output directory
func GetOutputDir() string {
	configMu.RLock()
	defer configMu.RUnlock()
	if GlobalConfig == nil || GlobalConfig.OutputDir == "" {
		return DefaultOutputDir
	}
	return GlobalConfig.OutputDir
}

// SetOutputDir sets the global output directory
func SetOutputDir(dir string) {
	configMu.Lock()
	defer configMu.Unlock()
	if GlobalConfig == nil {
		GlobalConfig = NewDefaultConfig()
	}
	GlobalConfig.SetOutputDir(dir)
}

// GetAuthDir returns the global auth directory
func GetAuthDir() string {
	configMu.RLock()
	defer configMu.RUnlock()
	if GlobalConfig == nil || GlobalConfig.AuthDir == "" {
		return filepath.Join(DefaultOutputDir, DefaultAuthDirName)
	}
	return GlobalConfig.AuthDir
}

// SetAuthDir sets the global auth directory
func SetAuthDir(dir string) {
	configMu.Lock()
	defer configMu.Unlock()
	if GlobalConfig == nil {
		GlobalConfig = NewDefaultConfig()
	}
	GlobalConfig.SetAuthDir(dir)
}

// GetSpotifyAuthPath returns the global Spotify auth path
func GetSpotifyAuthPath() string {
	configMu.RLock()
	defer configMu.RUnlock()
	if GlobalConfig == nil || GlobalConfig.SpotifyAuthPath == "" {
		return filepath.Join(GetAuthDir(), DefaultSpotifyAuthName)
	}
	return GlobalConfig.SpotifyAuthPath
}

// GetYTMAuthPath returns the global YouTube Music auth path
func GetYTMAuthPath() string {
	configMu.RLock()
	defer configMu.RUnlock()
	if GlobalConfig == nil || GlobalConfig.YTMAuthPath == "" {
		return filepath.Join(GetAuthDir(), DefaultYTMAuthName)
	}
	return GlobalConfig.YTMAuthPath
}

// ResetGlobalConfig re-initializes GlobalConfig from defaults and environment variables
func ResetGlobalConfig() {
	configMu.Lock()
	defer configMu.Unlock()
	GlobalConfig = NewDefaultConfig()
}
