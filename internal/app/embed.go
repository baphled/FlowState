// Package app provides the main application container and initialization.
package app

import (
	"io/fs"
	"os"
	"path/filepath"
)

// BundledAgentsDir returns an fs.FS for bundled agents if running from the development tree,
// or from the installed binary location otherwise.
//
// This provides a fallback mechanism:
// 1. If agents/ exists relative to the binary in the development tree, use it
// 2. Otherwise return os.DirFS of the discovered agents directory
// 3. If not found anywhere, return an error indicating agents directory not found
//
// Returns:
//   - An fs.FS containing bundled agents
//   - An error if the agents directory cannot be found
//
// Side effects:
//   - None.
func BundledAgentsDir() (fs.FS, error) {
	ex, err := os.Executable()
	if err != nil {
		return nil, err
	}

	bundledPath := filepath.Join(filepath.Dir(ex), "..", "..", "agents")
	if _, err := os.Stat(bundledPath); err == nil {
		return os.DirFS(bundledPath), nil
	}

	bundledPath = filepath.Join(filepath.Dir(ex), "agents")
	if _, err := os.Stat(bundledPath); err == nil {
		return os.DirFS(bundledPath), nil
	}

	return nil, fs.ErrNotExist
}
