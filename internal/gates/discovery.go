package gates

import (
	"fmt"
	"os"
	"path/filepath"
)

const manifestFilename = "manifest.yml"

// Discover walks gatesDir one level deep and returns every gate
// manifest at <gatesDir>/<name>/manifest.yml. Files at the root and
// subdirectories without a manifest.yml are silently ignored. A
// missing gatesDir is not an error — boot proceeds with no gates.
//
// The first malformed manifest aborts the walk; the returned error is
// wrapped with the offending path so the operator can locate it. If
// load-and-skip-malformed-entries is desired later, switch this to
// accumulate via errors.Join.
func Discover(gatesDir string) ([]Manifest, error) {
	entries, err := readGatesDir(gatesDir)
	if err != nil {
		return nil, err
	}
	var out []Manifest
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		manifestPath := filepath.Join(gatesDir, e.Name(), manifestFilename)
		if _, err := os.Stat(manifestPath); err != nil {
			continue
		}
		m, err := LoadManifest(manifestPath)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// readGatesDir is the missing-dir-tolerant directory read used by
// Discover. Returns (nil, nil) when gatesDir does not exist; any other
// stat error propagates.
func readGatesDir(gatesDir string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(gatesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read gates_dir %s: %w", gatesDir, err)
	}
	return entries, nil
}
