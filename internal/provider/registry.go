package provider

import (
	"fmt"
	"sync"
)

// Registry manages a collection of LLM providers.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewRegistry creates a new empty provider registry.
//
// Returns:
//   - A pointer to an initialised Registry.
//
// Side effects:
//   - None.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
	}
}

// Register adds a provider to the registry.
//
// Expected:
//   - p is a valid, non-nil Provider.
//
// Side effects:
//   - Modifies the registry's internal state (thread-safe).
func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Name()] = p
}

// Get retrieves a provider by name.
//
// Expected:
//   - name is a non-empty string matching a registered provider.
//
// Returns:
//   - The provider if found.
//   - An error if the provider is not registered.
//
// Side effects:
//   - None.
func (r *Registry) Get(name string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("provider %q not found", name)
	}
	return p, nil
}

// List returns the names of all registered providers.
//
// Returns:
//   - A slice of provider names.
//
// Side effects:
//   - None.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}
