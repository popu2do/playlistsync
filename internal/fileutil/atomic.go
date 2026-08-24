package fileutil

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// WriteFileAtomic safely writes data to targetPath via a temporary file and atomic rename.
func WriteFileAtomic(targetPath string, data []byte, perm os.FileMode) error {
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
