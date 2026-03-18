package tool

import (
	"fmt"
	"sync"
)

// Registry holds registered tools and provides lookup functionality.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry creates a new empty tool registry.
//
// Returns:
//   - A pointer to an initialised Registry.
//
// Side effects:
//   - None.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool to the registry.
//
// Expected:
//   - t is a valid, non-nil Tool.
//
// Side effects:
//   - Modifies the registry's internal state (thread-safe).
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

// Get retrieves a tool by name from the registry.
//
// Expected:
//   - name is a non-empty string matching a registered tool.
//
// Returns:
//   - The tool if found.
//   - An error if the tool is not registered.
//
// Side effects:
//   - None.
func (r *Registry) Get(name string) (Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("tool %q not found", name)
	}
	return t, nil
}

// List returns all registered tools.
//
// Returns:
//   - A slice of all registered tools.
//
// Side effects:
//   - None.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tools := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		tools = append(tools, t)
	}
	return tools
}

// CheckPermission returns the permission level for a tool (currently always Allow).
//
// Expected:
//   - toolName is a tool identifier (currently ignored).
//
// Returns:
//   - The permission level for the tool.
//
// Side effects:
//   - None.
func (r *Registry) CheckPermission(_ string) Permission {
	return Allow
}
