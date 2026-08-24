package fileutil_test

import (
	"os"
	"path/filepath"
	"playlistsync/internal/fileutil"
	"testing"
)

func TestWriteFileAtomic_Success(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "test.json")
	data := []byte("{\"hello\": \"world\"}\n")

	if err := fileutil.WriteFileAtomic(targetPath, data, 0644); err != nil {
		t.Fatalf("WriteFileAtomic failed: %v", err)
	}

	readData, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}

	if string(readData) != string(data) {
		t.Errorf("content mismatch: got %q, want %q", string(readData), string(data))
	}
}

func TestWriteFileAtomic_InvalidDirectory(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "non_existent_subdir", "sub", "test.json")
	data := []byte("data")

	if err := fileutil.WriteFileAtomic(targetPath, data, 0644); err == nil {
		t.Errorf("expected error for non-existent directory, got nil")
	}
}
