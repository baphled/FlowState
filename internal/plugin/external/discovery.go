package external

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/plugin/manifest"
)

// Discoverer scans a plugin directory for valid plugin manifests.
type Discoverer struct {
	config config.PluginsConfig
}

// NewDiscoverer creates a Discoverer with the given plugin configuration.
//
// Expected:
//   - cfg contains the plugin directory, enabled, and disabled filters.
//
// Returns:
//   - A pointer to an initialised Discoverer.
//
// Side effects:
//   - None.
func NewDiscoverer(cfg config.PluginsConfig) *Discoverer {
	return &Discoverer{config: cfg}
}

// Discover scans dir for subdirectories containing manifest.json files,
// loads and validates each manifest, and applies enabled/disabled filters.
//
// Expected:
//   - dir is a path where plugins may reside; created automatically if absent.
//
// Returns:
//   - A slice of valid, filtered manifests.
//   - An error if the directory cannot be created or read.
//
// Side effects:
//   - Creates the directory tree if it does not exist.
//   - Reads from the filesystem.
//   - Logs warnings for invalid manifests.
func (d *Discoverer) Discover(dir string) ([]*manifest.Manifest, error) {
	cleanDir := filepath.Clean(dir)
	if err := os.MkdirAll(cleanDir, 0o755); err != nil {
		return nil, fmt.Errorf("create plugin directory %q: %w", cleanDir, err)
	}
	entries, err := os.ReadDir(cleanDir)
	if err != nil {
		return nil, fmt.Errorf("read plugin directory %q: %w", cleanDir, err)
	}

	enabled := toSet(d.config.Enabled)
	disabled := toSet(d.config.Disabled)

	var results []*manifest.Manifest
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		manifestPath := filepath.Join(cleanDir, entry.Name(), "manifest.json")
		if _, statErr := os.Stat(manifestPath); statErr != nil {
			continue
		}

		m, loadErr := manifest.LoadManifest(manifestPath)
		if loadErr != nil {
			log.Printf("skipping plugin %s: %v", entry.Name(), loadErr)
			continue
		}

		if !isIncluded(m.Name, enabled, disabled) {
			continue
		}

		results = append(results, m)
	}

	return results, nil
}

// toSet converts a string slice to a set for O(1) lookups.
//
// Expected:
//   - items is a string slice (may be nil or empty).
//
// Returns:
//   - A map suitable for membership checks.
//
// Side effects:
//   - None.
func toSet(items []string) map[string]struct{} {
	s := make(map[string]struct{}, len(items))
	for _, item := range items {
		s[item] = struct{}{}
	}
	return s
}

// isIncluded checks whether a plugin name passes the enabled/disabled filters.
//
// Expected:
//   - name is a non-empty plugin name.
//   - enabled and disabled are sets built by toSet.
//
// Returns:
//   - true if the plugin should be included, false otherwise.
//
// Side effects:
//   - None.
func isIncluded(name string, enabled, disabled map[string]struct{}) bool {
	if len(enabled) > 0 {
		_, ok := enabled[name]
		return ok
	}
	if len(disabled) > 0 {
		_, ok := disabled[name]
		return !ok
	}
	return true
}
