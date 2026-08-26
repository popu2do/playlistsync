package invariants_test

import (
	"bytes"
	"os"
	"path/filepath"
	"playlistsync/internal/invariants"
	"testing"
)

// credentialLeak is a string that matches the spec vol5 §1.2.5 plaintext
// credential patterns (keyword + long alnum value).
const credentialLeak = `client_secret=abcdefabcdefabcdefabcdefabcdef12`

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
		{"credential content detected", func(t *testing.T) (string, string) {
			out := t.TempDir()
			if err := os.WriteFile(filepath.Join(out, "leak.txt"), []byte(credentialLeak), 0o644); err != nil {
				t.Fatal(err)
			}
			return out, t.TempDir()
		}, false},
		{"bearer token content detected", func(t *testing.T) (string, string) {
			out := t.TempDir()
			tok := "d41d8cd98f00b204e9800998ecf8427e" + "d41d8cd98f00b204e9800998ecf8427e"
			if err := os.WriteFile(filepath.Join(out, "session.txt"), []byte("Authorization: Bearer "+tok), 0o644); err != nil {
				t.Fatal(err)
			}
			return out, t.TempDir()
		}, false},
		{"credential in auth subdir skipped", func(t *testing.T) (string, string) {
			out := t.TempDir()
			if err := os.MkdirAll(filepath.Join(out, "auth"), 0o755); err != nil {
				t.Fatal(err)
			}
			// Plaintext form: if the auth/ skip were missing, this detectable
			// credential would trip the probe. It must not.
			if err := os.WriteFile(filepath.Join(out, "auth", "spotify_credentials.json"), []byte(credentialLeak), 0o644); err != nil {
				t.Fatal(err)
			}
			return out, t.TempDir()
		}, true},
		{"large binary skipped despite marker", func(t *testing.T) (string, string) {
			out := t.TempDir()
			content := bytes.Repeat([]byte{'x'}, 70*1024) // 70 KiB > 64 KiB cap
			copy(content[32*1024:], []byte(credentialLeak))
			if err := os.WriteFile(filepath.Join(out, "blob.bin"), content, 0o644); err != nil {
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

func TestScanForPlaintextCredentials(t *testing.T) {
	base := t.TempDir()
	leak := filepath.Join(base, "bad.txt")
	if err := os.WriteFile(leak, []byte("SAPISID: abcdefabcdefabcdefabcdefabcdef12"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "good.txt"), []byte("just some lyrics"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "auth"), 0o755); err != nil {
		t.Fatal(err)
	}
	authLeak := filepath.Join(base, "auth", "creds.json")
	// Plaintext form so the regex can detect it when auth/ is not skipped.
	if err := os.WriteFile(authLeak, []byte("refresh_token=abcdefabcdefabcdefabcdefabcdef12"), 0o644); err != nil {
		t.Fatal(err)
	}

	// With auth/ skipped only the non-managed leak is flagged.
	flagged, err := invariants.ScanForPlaintextCredentials(base, []string{"auth"})
	if err != nil {
		t.Fatalf("scan error: %v", err)
	}
	if len(flagged) != 1 || flagged[0] != leak {
		t.Fatalf("flagged = %v, want exactly %q", flagged, leak)
	}

	// Without skipDirs both leaks are flagged.
	flagged2, err := invariants.ScanForPlaintextCredentials(base, nil)
	if err != nil {
		t.Fatalf("scan error: %v", err)
	}
	if len(flagged2) != 2 {
		t.Fatalf("flagged = %v, want 2 entries", flagged2)
	}

	// A missing root is not an error and yields no flags.
	flagged3, err := invariants.ScanForPlaintextCredentials(filepath.Join(base, "nope"), nil)
	if err != nil || len(flagged3) != 0 {
		t.Fatalf("missing root: flagged=%v err=%v, want no error and no flags", flagged3, err)
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
