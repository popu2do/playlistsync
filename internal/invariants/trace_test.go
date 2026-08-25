package invariants_test

import (
	"os"
	"path/filepath"
	"playlistsync/internal/invariants"
	"testing"
)

func TestProbeZeroTraceClean(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T) (outputDir, residueDir string)
		want  bool
	}{
		{"clean empty dirs", func(t *testing.T) (string, string) {
			return t.TempDir(), t.TempDir()
		}, true},
		{"absent dirs are clean", func(t *testing.T) (string, string) {
			base := t.TempDir()
			return filepath.Join(base, "no_output"), filepath.Join(base, "no_tmp")
		}, true},
		{"pid file residue", func(t *testing.T) (string, string) {
			out := t.TempDir()
			if err := os.WriteFile(filepath.Join(out, "web.pid"), []byte("12345"), 0o644); err != nil {
				t.Fatal(err)
			}
			return out, t.TempDir()
		}, false},
		{"nested pid file residue", func(t *testing.T) (string, string) {
			base := t.TempDir()
			out := filepath.Join(base, "output")
			if err := os.MkdirAll(filepath.Join(out, "sub"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(out, "sub", "sync.pid"), []byte("1"), 0o644); err != nil {
				t.Fatal(err)
			}
			return out, t.TempDir()
		}, false},
		{"uppercase pid suffix detected", func(t *testing.T) (string, string) {
			out := t.TempDir()
			if err := os.WriteFile(filepath.Join(out, "WEB.PID"), []byte("1"), 0o644); err != nil {
				t.Fatal(err)
			}
			return out, t.TempDir()
		}, false},
		{"non pid files are fine", func(t *testing.T) (string, string) {
			out := t.TempDir()
			if err := os.WriteFile(filepath.Join(out, "report.json"), []byte("{}"), 0o644); err != nil {
				t.Fatal(err)
			}
			return out, t.TempDir()
		}, true},
		{"tmp file residue", func(t *testing.T) (string, string) {
			residue := t.TempDir()
			if err := os.WriteFile(filepath.Join(residue, "leftover.tmp"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			return t.TempDir(), residue
		}, false},
		{"tmp subdir residue", func(t *testing.T) (string, string) {
			residue := t.TempDir()
			if err := os.MkdirAll(filepath.Join(residue, "cdp-profile"), 0o755); err != nil {
				t.Fatal(err)
			}
			return t.TempDir(), residue
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outputDir, residueDir := tt.setup(t)
			if got := invariants.ProbeZeroTraceClean(outputDir, residueDir); got != tt.want {
				t.Fatalf("ProbeZeroTraceClean(%q, %q) = %v, want %v", outputDir, residueDir, got, tt.want)
			}
		})
	}
}

func TestAssertZeroTraceCleanSmoke(t *testing.T) {
	// Smoke test only: probes the ambient test working directory (cwd) for
	// the default package-relative residue paths ("output",
	// ".playlistsync/tmp"), which are absent here, so it must report clean.
	// This assertion is environment-coupled (the web server's runtime
	// directory may differ from the test cwd) and is not a hermetic unit
	// test; hermetic probing is covered by TestProbeZeroTraceClean.
	if !invariants.AssertZeroTraceClean() {
		t.Fatal("AssertZeroTraceClean() = false, want true (default residue paths absent)")
	}
}
