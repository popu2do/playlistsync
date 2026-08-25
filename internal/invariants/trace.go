package invariants

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"playlistsync/internal/config"
)

// defaultResidueTmpDir is the default temporary directory scanned for leftover
// residue (unmanaged temp files from CDP or other subprocesses).
var defaultResidueTmpDir = filepath.Join(".playlistsync", "tmp")

// managedAuthDirName is the subdirectory of the output directory that holds
// the managed credential store (output/auth/*_credentials.json, browser
// profiles, etc.). It is a legitimate, expected location for secrets, so the
// zero-trace probe skips it to avoid false positives.
const managedAuthDirName = "auth"

// maxCredentialScanSize caps the size of regular files whose contents are
// scanned for plaintext credential markers. Larger files are treated as
// binary artifacts / build output and skipped.
const maxCredentialScanSize = 64 * 1024 // 64 KiB

// credentialPatterns are the plaintext credential markers defined by spec
// vol5 §1.2.5 (AssertZeroTraceResidue): cookie/SAPISID/refresh_token/client
// secret assignments and hex Bearer tokens.
var credentialPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:client_secret|sapisid|refresh_token|cookie)\s*[:=]\s*["']?([a-zA-Z0-9_\-\.]{16,})["']?`),
	regexp.MustCompile(`Bearer\s+[a-fA-F0-9]{32,64}`),
}

// AssertZeroTraceClean probes the default known residue paths and reports
// whether the filesystem is free of unmanaged residue: the configured output
// directory must contain no pid files and no file contents carrying plaintext
// credential markers (the managed auth/ subdirectory is exempt), and the
// residue tmp directory must be empty. Any probe failure reports unclean
// (fail closed).
func AssertZeroTraceClean() bool {
	return ProbeZeroTraceClean(config.GetOutputDir(), defaultResidueTmpDir)
}

// ProbeZeroTraceClean scans outputDir for lingering pid files (recursively)
// and plaintext credential residue (recursively, skipping the managed auth/
// subdirectory), and residueDir for any leftover entries. A missing directory
// counts as clean; any other probe error (e.g. permission denied mid-walk)
// reports unclean, per the zero-trace fail-closed invariant.
func ProbeZeroTraceClean(outputDir, residueDir string) bool {
	if hasPidResidue(outputDir) {
		return false
	}
	flagged, err := ScanForPlaintextCredentials(outputDir, []string{managedAuthDirName})
	if err != nil {
		return false // probe failure: fail closed
	}
	if len(flagged) > 0 {
		return false
	}
	if !dirIsEmpty(residueDir) {
		return false
	}
	return true
}

// ScanForPlaintextCredentials walks dir recursively and returns the paths of
// regular files (up to maxCredentialScanSize bytes) whose contents match a
// plaintext credential marker from spec vol5 §1.2.5. Any directory whose base
// name is in skipDirs is skipped entirely (e.g. the managed auth/ store).
// A missing root returns no files and no error; other walk errors are
// reported. The walk never follows symlinks.
func ScanForPlaintextCredentials(dir string, skipDirs []string) ([]string, error) {
	skip := make(map[string]bool, len(skipDirs))
	for _, d := range skipDirs {
		skip[d] = true
	}

	var flagged []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == dir && errors.Is(err, fs.ErrNotExist) {
				return nil // root missing: nothing to scan
			}
			return err
		}
		if d.IsDir() {
			if skip[d.Name()] && path != dir {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // never follow symlinks or special files
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxCredentialScanSize {
			return nil // binary/artifact: skip content scan
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, re := range credentialPatterns {
			if re.Match(data) {
				flagged = append(flagged, path)
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return flagged, nil
}

// hasPidResidue walks dir and reports whether any "*.pid" file exists beneath
// it. A missing root counts as clean; other walk errors count as residue.
func hasPidResidue(dir string) bool {
	found := false
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == dir && errors.Is(err, fs.ErrNotExist) {
				return nil // root missing: nothing to scan
			}
			return err
		}
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".pid") {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return true // probe failure: fail closed
	}
	return found
}

// dirIsEmpty reports whether dir contains no entries. A missing dir counts as
// empty; any other probe error reports non-empty (fail closed).
func dirIsEmpty(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return errors.Is(err, fs.ErrNotExist)
	}
	return len(entries) == 0
}
