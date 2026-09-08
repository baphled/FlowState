package gates

import (
	"errors"
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
// Malformed manifests are skipped-and-collected: every valid
// sibling still registers, and each malformed manifest surfaces as a
// per-directory error joined into the returned error so one bad
// manifest cannot silently unregister every ext gate. The returned
// manifests are usable even when the error is non-nil.
//
// Expected: parameters for Discover.
// Returns: result of Discover.
// Side effects: None.
func Discover(gatesDir string) ([]Manifest, error) {
	entries, err := readGatesDir(gatesDir)
	if err != nil {
		return nil, err
	}
	var out []Manifest
	var skipped []error
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
			skipped = append(skipped, fmt.Errorf("gate %q: %w", e.Name(), err))
			continue
		}
		out = append(out, m)
	}
	return out, errors.Join(skipped...)
}

// readGatesDir is the missing-dir-tolerant directory read used by
// Discover. Returns (nil, nil) when gatesDir does not exist; any other
// stat error propagates.
//
// Expected: parameters for readGatesDir.
// Returns: result of readGatesDir.
// Side effects: None.
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
