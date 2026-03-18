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
func NewRegistry() *Registry {
	return &Registry{
		manifests: make(map[string]*Manifest),
	}
}

// Discover scans a directory for agent manifests and loads them into the registry.
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
func (r *Registry) Register(manifest *Manifest) {
	r.manifests[manifest.ID] = manifest
}

// Get retrieves a manifest by ID.
func (r *Registry) Get(id string) (*Manifest, bool) {
	manifest, ok := r.manifests[id]
	return manifest, ok
}

// List returns all manifests in the registry sorted by ID.
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
