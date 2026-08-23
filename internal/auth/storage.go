package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ParseCookie normalizes cookie values
func ParseCookie(input string) string {
	input = strings.TrimSpace(input)
	if !strings.Contains(input, "=") && len(input) > 20 {
		return fmt.Sprintf("sp_dc=%s", input)
	}
	return input
}

// LoadCookie loads cookie from JSON or plaintext file
func LoadCookie(path string) (string, error) {
	cleanPath := filepath.Clean(path)
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("credentials file missing at %s: %w", cleanPath, err)
		}
		return "", fmt.Errorf("failed to read credentials at %s: %w", cleanPath, err)
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		return "", fmt.Errorf("credentials file is empty at %s", cleanPath)
	}
	if strings.HasPrefix(content, "{") {
		var m map[string]string
		if err := json.Unmarshal(data, &m); err == nil {
			for _, k := range []string{"Cookie", "cookie", "sp_dc"} {
				if v, ok := m[k]; ok && v != "" {
					return ParseCookie(v), nil
				}
			}
		} else {
			return "", fmt.Errorf("credentials corrupted at %s: %w", cleanPath, err)
		}
	}
	return content, nil
}

// writeRestrictedJSON writes data as indented JSON with restricted permissions (0600) using atomic replace
func writeRestrictedJSON(path string, v any) error {
	cleanPath := filepath.Clean(path)
	dir := filepath.Dir(cleanPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create auth dir: %w", err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(dir, "auth_tmp_*.json")
	if err != nil {
		return fmt.Errorf("create temp auth file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}()

	if _, err := tmpFile.Write(append(data, '\n')); err != nil {
		return err
	}
	_ = tmpFile.Chmod(0600)
	if err := tmpFile.Sync(); err != nil {
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}

	_ = os.Remove(cleanPath)
	if err := os.Rename(tmpPath, cleanPath); err != nil {
		return fmt.Errorf("atomic save credentials: %w", err)
	}
	_ = os.Chmod(cleanPath, 0600)
	return nil
}

// SaveCookie saves cookie to path with restricted file permissions (0600)
func SaveCookie(path string, cookie string) error {
	if path == "" {
		path = DefaultSpotifyAuthPath
	}
	return writeRestrictedJSON(path, map[string]string{
		"Cookie": ParseCookie(cookie),
	})
}

// SaveRawCookieMap saves headers mapping with restricted file permissions (0600)
func SaveRawCookieMap(path string, cookie string) error {
	if path == "" {
		path = DefaultYTMAuthPath
	}
	return writeRestrictedJSON(path, map[string]string{
		"Cookie":     cookie,
		"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	})
}
