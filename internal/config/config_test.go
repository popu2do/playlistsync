package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewDefaultConfig(t *testing.T) {
	cfg := NewDefaultConfig()
	if cfg.OutputDir == "" {
		t.Fatal("expected non-empty OutputDir")
	}
	if cfg.AuthDir == "" {
		t.Fatal("expected non-empty AuthDir")
	}
	if cfg.SpotifyAuthPath == "" {
		t.Fatal("expected non-empty SpotifyAuthPath")
	}
	if cfg.YTMAuthPath == "" {
		t.Fatal("expected non-empty YTMAuthPath")
	}

	spProfile := cfg.GetSpotifyProfileDir()
	if spProfile == "" {
		t.Fatal("expected non-empty Spotify profile dir")
	}

	ytmProfile := cfg.GetYTMProfileDir()
	if ytmProfile == "" {
		t.Fatal("expected non-empty YTM profile dir")
	}
}

func TestConfig_EnvOverride(t *testing.T) {
	tempDir := t.TempDir()
	origOut := os.Getenv("PLAYLISTSYNC_OUTPUT_DIR")
	origAuth := os.Getenv("PLAYLISTSYNC_AUTH_DIR")
	origSpAuth := os.Getenv("PLAYLISTSYNC_SPOTIFY_AUTH")
	origYtmAuth := os.Getenv("PLAYLISTSYNC_YTM_AUTH")
	origProxy := os.Getenv("PLAYLISTSYNC_PROXY")
	origClean := os.Getenv("PLAYLISTSYNC_CLEAN_EXTRA")
	origConf := os.Getenv("PLAYLISTSYNC_CONFIDENCE_SCORE")

	defer func() {
		os.Setenv("PLAYLISTSYNC_OUTPUT_DIR", origOut)
		os.Setenv("PLAYLISTSYNC_AUTH_DIR", origAuth)
		os.Setenv("PLAYLISTSYNC_SPOTIFY_AUTH", origSpAuth)
		os.Setenv("PLAYLISTSYNC_YTM_AUTH", origYtmAuth)
		os.Setenv("PLAYLISTSYNC_PROXY", origProxy)
		os.Setenv("PLAYLISTSYNC_CLEAN_EXTRA", origClean)
		os.Setenv("PLAYLISTSYNC_CONFIDENCE_SCORE", origConf)
		ResetGlobalConfig()
	}()

	customAuth := filepath.Join(tempDir, "custom_auth")
	customSpotify := filepath.Join(tempDir, "custom_sp.json")
	customYTM := filepath.Join(tempDir, "custom_ytm.json")

	os.Setenv("PLAYLISTSYNC_OUTPUT_DIR", tempDir)
	os.Setenv("PLAYLISTSYNC_AUTH_DIR", customAuth)
	os.Setenv("PLAYLISTSYNC_SPOTIFY_AUTH", customSpotify)
	os.Setenv("PLAYLISTSYNC_YTM_AUTH", customYTM)
	os.Setenv("PLAYLISTSYNC_PROXY", "http://127.0.0.1:8080")
	os.Setenv("PLAYLISTSYNC_CLEAN_EXTRA", "false")
	os.Setenv("PLAYLISTSYNC_CONFIDENCE_SCORE", "85")

	cfg := NewDefaultConfig()
	if cfg.OutputDir != tempDir {
		t.Fatalf("expected OutputDir %s, got %s", tempDir, cfg.OutputDir)
	}
	if cfg.AuthDir != customAuth {
		t.Fatalf("expected AuthDir %s, got %s", customAuth, cfg.AuthDir)
	}
	if cfg.SpotifyAuthPath != customSpotify {
		t.Fatalf("expected SpotifyAuthPath %s, got %s", customSpotify, cfg.SpotifyAuthPath)
	}
	if cfg.YTMAuthPath != customYTM {
		t.Fatalf("expected YTMAuthPath %s, got %s", customYTM, cfg.YTMAuthPath)
	}
	if cfg.ProxyURL != "http://127.0.0.1:8080" {
		t.Fatalf("expected ProxyURL http://127.0.0.1:8080, got %s", cfg.ProxyURL)
	}
	if cfg.CleanExtra != false {
		t.Fatalf("expected CleanExtra false, got %v", cfg.CleanExtra)
	}
	if cfg.ConfidenceScore != 85 {
		t.Fatalf("expected ConfidenceScore 85, got %d", cfg.ConfidenceScore)
	}
}

func TestConfig_SetOutputDirAndAuthDir(t *testing.T) {
	cfg := NewDefaultConfig()
	cfg.SetOutputDir("my_custom_output")

	if cfg.OutputDir != "my_custom_output" {
		t.Errorf("expected OutputDir 'my_custom_output', got %q", cfg.OutputDir)
	}
	expectedAuth := filepath.Join("my_custom_output", DefaultAuthDirName)
	if cfg.AuthDir != expectedAuth {
		t.Errorf("expected AuthDir %q, got %q", expectedAuth, cfg.AuthDir)
	}
	expectedSpAuth := filepath.Join(expectedAuth, DefaultSpotifyAuthName)
	if cfg.SpotifyAuthPath != expectedSpAuth {
		t.Errorf("expected SpotifyAuthPath %q, got %q", expectedSpAuth, cfg.SpotifyAuthPath)
	}

	// Set custom auth dir
	cfg.SetAuthDir("my_special_auth")
	if cfg.AuthDir != "my_special_auth" {
		t.Errorf("expected AuthDir 'my_special_auth', got %q", cfg.AuthDir)
	}
	if cfg.SpotifyAuthPath != filepath.Join("my_special_auth", DefaultSpotifyAuthName) {
		t.Errorf("expected SpotifyAuthPath in my_special_auth, got %q", cfg.SpotifyAuthPath)
	}

	// Setting empty dir falls back to default
	cfg.SetOutputDir("")
	if cfg.OutputDir != DefaultOutputDir {
		t.Errorf("expected fallback to DefaultOutputDir, got %q", cfg.OutputDir)
	}
	cfg.SetAuthDir("")
	if cfg.AuthDir != filepath.Join(DefaultOutputDir, DefaultAuthDirName) {
		t.Errorf("expected fallback to DefaultAuthDir, got %q", cfg.AuthDir)
	}
}

func TestGlobalConfigHelpers(t *testing.T) {
	ResetGlobalConfig()
	origOut := GetOutputDir()
	if origOut != DefaultOutputDir {
		t.Errorf("expected default output dir %s, got %s", DefaultOutputDir, origOut)
	}

	SetOutputDir("new_global_out")
	if GetOutputDir() != "new_global_out" {
		t.Errorf("expected new_global_out, got %s", GetOutputDir())
	}

	SetAuthDir("new_global_auth")
	if GetAuthDir() != "new_global_auth" {
		t.Errorf("expected new_global_auth, got %s", GetAuthDir())
	}

	if GetConfidenceScore() != DefaultConfidenceScore {
		t.Errorf("expected confidence score %d, got %d", DefaultConfidenceScore, GetConfidenceScore())
	}

	ResetGlobalConfig()
	if GetOutputDir() != DefaultOutputDir {
		t.Errorf("expected reset output dir %s, got %s", DefaultOutputDir, GetOutputDir())
	}
}
