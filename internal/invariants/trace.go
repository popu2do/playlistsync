package invariants

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"playlistsync/internal/config"
)

// defaultResidueTmpDir is the default temporary directory scanned for leftover
// residue (unmanaged temp files from CDP or other subprocesses).
var defaultResidueTmpDir = filepath.Join(".playlistsync", "tmp")

// AssertZeroTraceClean probes the default known residue paths and reports
// whether the filesystem is free of unmanaged residue: the configured output
// directory must contain no pid files, and the residue tmp directory must be
// empty. Any probe failure reports unclean (fail closed).
func AssertZeroTraceClean() bool {
	return ProbeZeroTraceClean(config.GetOutputDir(), defaultResidueTmpDir)
}

// ProbeZeroTraceClean scans outputDir for lingering pid files (recursively)
// and residueDir for any leftover entries. A missing directory counts as
// clean; any other probe error (e.g. permission denied mid-walk) reports
// unclean, per the zero-trace fail-closed invariant.
func ProbeZeroTraceClean(outputDir, residueDir string) bool {
	if hasPidResidue(outputDir) {
		return false
	}
	if !dirIsEmpty(residueDir) {
		return false
	}
	return true
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
