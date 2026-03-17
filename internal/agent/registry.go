package agent

import (
	"fmt"
	"path/filepath"
	"sync"
)

// AgentRegistry manages discovered agent manifests.
type AgentRegistry struct {
	mu     sync.RWMutex
	agents map[string]*AgentManifest
}

// NewAgentRegistry creates a new empty agent registry.
func NewAgentRegistry() *AgentRegistry {
	return &AgentRegistry{
		agents: make(map[string]*AgentManifest),
	}
}

// Discover scans a directory for agent manifests (*.json and *.md files).
// Invalid manifests are skipped gracefully.
func (r *AgentRegistry) Discover(dir string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.agents = make(map[string]*AgentManifest)

	patterns := []string{
		filepath.Join(dir, "*.json"),
		filepath.Join(dir, "*.md"),
	}

	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return fmt.Errorf("scanning directory %s: %w", dir, err)
		}

		for _, path := range matches {
			manifest, err := LoadManifest(path)
			if err != nil {
				continue
			}

			if err := manifest.Validate(); err != nil {
				continue
			}

			r.agents[manifest.ID] = manifest
		}
	}

	return nil
}

// Get retrieves an agent manifest by ID.
func (r *AgentRegistry) Get(id string) (*AgentManifest, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	manifest, ok := r.agents[id]
	return manifest, ok
}

// List returns all registered agent manifests.
func (r *AgentRegistry) List() []*AgentManifest {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*AgentManifest, 0, len(r.agents))
	for _, m := range r.agents {
		result = append(result, m)
	}
	return result
}

// Count returns the number of registered agents.
func (r *AgentRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.agents)
}
