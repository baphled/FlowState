// Package agent loads and manages FlowState agent manifests.
package agent

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
)

type AgentRegistry struct {
	manifests map[string]*AgentManifest
}

func NewAgentRegistry() *AgentRegistry {
	return &AgentRegistry{
		manifests: make(map[string]*AgentManifest),
	}
}

func (r *AgentRegistry) Discover(dir string) error {
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

	r.manifests = make(map[string]*AgentManifest)
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

func (r *AgentRegistry) Register(manifest *AgentManifest) {
	r.manifests[manifest.ID] = manifest
}

func (r *AgentRegistry) Get(id string) (*AgentManifest, bool) {
	manifest, ok := r.manifests[id]
	return manifest, ok
}

func (r *AgentRegistry) List() []*AgentManifest {
	if len(r.manifests) == 0 {
		return nil
	}

	ids := make([]string, 0, len(r.manifests))
	for id := range r.manifests {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	manifests := make([]*AgentManifest, 0, len(ids))
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
