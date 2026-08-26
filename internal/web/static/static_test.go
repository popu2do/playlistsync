package static

import (
	"io/fs"
	"testing"
)

// TestDistEmbedded verifies the embed compiled and the dist subtree is
// reachable through DistFS (rooted at dist/).
func TestDistEmbedded(t *testing.T) {
	f, err := fs.Stat(DistFS, "index.html")
	if err != nil {
		t.Fatalf("DistFS index.html: %v", err)
	}
	if f.IsDir() {
		t.Fatal("index.html reported as a directory")
	}
	if f.Size() == 0 {
		t.Fatal("index.html is empty")
	}
}
