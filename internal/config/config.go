package config

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// Default paths and values
const (
	DefaultOutputDir       = "output"
	DefaultAuthDirName     = "auth"
	DefaultSpotifyAuthName = "spotify_credentials.json"
	DefaultYTMAuthName     = "ytmusic_credentials.json"
	DefaultConfidenceScore = 70
)

var (
	configMu sync.RWMutex
	// GlobalConfig is the singleton configuration instance
	GlobalConfig = NewDefaultConfig()
)

// AppConfig represents global application configuration
type AppConfig struct {
	OutputDir       string `json:"outputDir"`
	AuthDir         string `json:"authDir"`
	SpotifyAuthPath string `json:"spotifyAuthPath"`
	YTMAuthPath     string `json:"ytmAuthPath"`
	ProxyURL        string `json:"proxyUrl"`
	CleanExtra      bool   `json:"cleanExtra"`
	ConfidenceScore int    `json:"confidenceScore"`
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

// GetConfidenceScore returns the configured confidence threshold
func GetConfidenceScore() int {
	configMu.RLock()
	defer configMu.RUnlock()
	if GlobalConfig == nil || GlobalConfig.ConfidenceScore <= 0 {
		return DefaultConfidenceScore
	}
	return GlobalConfig.ConfidenceScore
}

// ResetGlobalConfig re-initializes GlobalConfig from defaults and environment variables
func ResetGlobalConfig() {
	configMu.Lock()
	defer configMu.Unlock()
	GlobalConfig = NewDefaultConfig()
}
