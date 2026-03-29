package plugin

import "sync"

// Registry provides a concurrency-safe registry for plugins.
//
// Expected:
//   - Register adds a plugin by name; duplicate names are rejected.
//   - Get retrieves a plugin by name.
//   - List returns all registered plugins in registration order.
//
// Returns: a ready-to-use registry for plugins.
// Side effects: none.
type Registry struct {
	mu      sync.RWMutex
	plugins map[string]Plugin
	order   []string
}

// NewRegistry creates a new, empty Registry.
//
// Expected:
//   - Returns a ready-to-use registry.
//
// Returns: a pointer to a new Registry.
// Side effects: none.
func NewRegistry() *Registry {
	return &Registry{
		plugins: make(map[string]Plugin),
		order:   make([]string, 0),
	}
}

// Register adds a plugin to the registry.
//
// Expected:
//   - Returns an error if a plugin with the same name is already registered.
//   - Safe for concurrent use.
//
// Returns: error if plugin exists, nil otherwise.
// Side effects: mutates the registry's plugin map and order slice.
func (r *Registry) Register(p Plugin) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := p.Name()
	if _, exists := r.plugins[name]; exists {
		return ErrPluginExists
	}
	r.plugins[name] = p
	r.order = append(r.order, name)
	return nil
}

// Get retrieves a plugin by name.
//
// Expected:
//   - Returns the plugin and true if found, nil and false otherwise.
//   - Safe for concurrent use.
//
// Returns: (Plugin, true) if found, (nil, false) otherwise.
// Side effects: none.
func (r *Registry) Get(name string) (Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.plugins[name]
	return p, ok
}

// List returns all registered plugins in registration order.
//
// Expected:
//   - Returns a slice of plugins in registration order.
//   - Safe for concurrent use.
//
// Returns: slice of Plugin in registration order.
// Side effects: none.
func (r *Registry) List() []Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	plugins := make([]Plugin, 0, len(r.order))
	for _, name := range r.order {
		plugins = append(plugins, r.plugins[name])
	}
	return plugins
}

// ErrPluginExists is returned when a plugin with the same name is already registered.
//
// Expected: used as a sentinel error for duplicate plugin registration.
// Returns: *pluginExistsError.
// Side effects: none.
var ErrPluginExists = &pluginExistsError{}

// pluginExistsError is the error type for duplicate plugin registration.
//
// Expected: used internally for duplicate plugin registration errors.
// Returns: struct implementing error.
// Side effects: none.
type pluginExistsError struct{}

// Error returns the error message for pluginExistsError.
//
// Expected: called when error string is needed.
// Returns: error string.
// Side effects: none.
func (e *pluginExistsError) Error() string { return "plugin already registered" }
