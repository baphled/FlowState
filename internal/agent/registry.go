// Package agent loads and manages FlowState agent manifests.
package agent

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
)

// Registry manages a collection of agent manifests.
type Registry struct {
	manifests map[string]*Manifest
}

// NewRegistry creates a new empty agent registry.
//
// Returns:
//   - A pointer to an initialised Registry.
//
// Side effects:
//   - None.
func NewRegistry() *Registry {
	return &Registry{
		manifests: make(map[string]*Manifest),
	}
}

// Discover scans a directory for agent manifests and loads them into the registry.
//
// Expected:
//   - dir is a valid path to an existing directory.
//
// Returns:
//   - nil on success.
//   - An error if the directory cannot be read or no valid manifests are found.
//
// Side effects:
//   - Reads from the filesystem.
//   - Replaces any existing manifests in the registry.
func (r *Registry) Discover(dir string) error {
	cleanDir := filepath.Clean(dir)
	info, err := os.Stat(cleanDir)
	if err != nil {
		return fmt.Errorf("stat agent directory %q: %w", cleanDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("agent directory %q is not a directory", cleanDir)
	}

	manifestPaths, err := discoverManifestPaths(cleanDir)
	if err != nil {
		return err
	}

	r.manifests = make(map[string]*Manifest)
	for _, path := range manifestPaths {
		manifest, err := LoadManifest(path)
		if err != nil {
			log.Printf("skipping agent manifest %s: %v", path, err)
			continue
		}
		if err := manifest.Validate(); err != nil {
			log.Printf("skipping invalid agent manifest %s: %v", path, err)
			continue
		}
		r.manifests[manifest.ID] = manifest
	}

	return nil
}

// Register adds a manifest to the registry.
//
// Expected:
//   - manifest is a valid, non-nil Manifest pointer.
//
// Side effects:
//   - Modifies the registry's internal state.
func (r *Registry) Register(manifest *Manifest) {
	r.manifests[manifest.ID] = manifest
}

// Get retrieves a manifest by ID.
//
// Expected:
//   - id is a non-empty string.
//
// Returns:
//   - The manifest and true if found.
//   - nil and false if not found.
//
// Side effects:
//   - None.
func (r *Registry) Get(id string) (*Manifest, bool) {
	manifest, ok := r.manifests[id]
	return manifest, ok
}

// List returns all manifests in the registry sorted by ID.
//
// Returns:
//   - A slice of manifests sorted alphabetically by ID.
//   - nil if the registry is empty.
//
// Side effects:
//   - None.
func (r *Registry) List() []*Manifest {
	if len(r.manifests) == 0 {
		return nil
	}

	ids := make([]string, 0, len(r.manifests))
	for id := range r.manifests {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	manifests := make([]*Manifest, 0, len(ids))
	for _, id := range ids {
		manifests = append(manifests, r.manifests[id])
	}

	return manifests
}

// discoverManifestPaths discovers manifest file paths in the specified directory.
//
// Expected: dir is a valid directory path.
//
// Returns: A sorted slice of manifest file paths (*.json and *.md files) found in dir,
// or an error if globbing fails.
//
// Side effects: None. This function performs read-only filesystem operations.
func discoverManifestPaths(dir string) ([]string, error) {
	patterns := []string{
		filepath.Join(dir, "*.json"),
		filepath.Join(dir, "*.md"),
	}

	var paths []string
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("glob agent manifests with pattern %q: %w", pattern, err)
		}
		paths = append(paths, matches...)
	}

	sort.Strings(paths)
	return paths, nil
}
