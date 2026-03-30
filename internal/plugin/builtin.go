package plugin

import (
	"fmt"
	"slices"
	"sync"

	"github.com/baphled/flowstate/internal/plugin/eventbus"
)

// HealthProvider is implemented by failover.HealthManager.
//
// Expected: IsRateLimited returns true if the provider/model is rate-limited.
// Returns: true if rate-limited, false otherwise.
// Side effects: none.
type HealthProvider interface {
	// IsRateLimited returns true if the provider/model is currently rate-limited.
	//
	// Expected: provider and model are non-empty strings.
	// Returns: true if rate-limited, false otherwise.
	// Side effects: none.
	IsRateLimited(provider, model string) bool
}

// PluginsConf holds plugin-relevant config values.
//
// Expected: used to configure plugin runtime behaviour.
// Returns: struct with configuration values.
// Side effects: none.
type PluginsConf struct {
	Dir      string
	LogPath  string
	LogSize  int64
	Timeout  int
	Enabled  []string
	Disabled []string
}

// Deps holds the runtime dependencies available to builtin plugin factories
// when they are instantiated at application startup.
//
// Expected: passed to Factory functions during LoadBuiltins.
// Returns: struct containing runtime dependencies.
// Side effects: none.
type Deps struct {
	Registry      *Registry
	EventBus      *eventbus.EventBus
	HealthManager HealthProvider
	PluginsConfig PluginsConf
}

// Registration describes a builtin plugin and how it should be loaded.
// It carries the metadata needed for ordering and enable/disable decisions.
//
// Expected: Name is unique across all registered builtins.
// Returns: used by LoadBuiltins to determine load order and eligibility.
// Side effects: none.
type Registration struct {
	// Name is the unique identifier for this plugin. Used for enable/disable matching.
	Name string
	// Order controls the load order relative to other builtin plugins.
	// Lower values load first. Plugins with the same Order load in registration order.
	Order int
	// EnabledByDefault controls whether this plugin loads if not mentioned in config.
	// Set to false for optional plugins that must be explicitly enabled.
	EnabledByDefault bool
	// Factory constructs the plugin from runtime dependencies.
	Factory func(Deps) (Plugin, error)
}

var (
	builtinMu            sync.RWMutex
	builtinRegistrations []Registration
)

// ResetBuiltins clears all registered builtin factories.
// This is intended for testing only.
//
// Expected: called during test setup.
// Returns: nothing.
// Side effects: clears the global factory slice.
func ResetBuiltins() {
	builtinMu.Lock()
	defer builtinMu.Unlock()
	builtinRegistrations = nil
}

// RegisterBuiltin registers a builtin plugin with its metadata.
// Called from init() in each builtin plugin package.
//
// Expected: r is a valid Registration with non-nil Factory.
// Returns: nothing.
// Side effects: adds registration to the global registry.
func RegisterBuiltin(r Registration) {
	builtinMu.Lock()
	defer builtinMu.Unlock()
	builtinRegistrations = append(builtinRegistrations, r)
}

// isEnabled determines whether a registration should be loaded based on config.
//
// Expected: r has a stable Name and conf contains the active plugin policy.
// Returns: true when the registration should be loaded, false otherwise.
// Side effects: none.
func isEnabled(r Registration, conf PluginsConf) bool {
	if slices.Contains(conf.Enabled, r.Name) {
		return true
	}
	if slices.Contains(conf.Disabled, r.Name) {
		return false
	}
	return r.EnabledByDefault
}

// LoadBuiltins instantiates all registered builtin factories in order,
// skipping those disabled by PluginsConf.
//
// A plugin is loaded if:
//   - EnabledByDefault is true AND it is not in PluginsConf.Disabled
//   - OR it is explicitly listed in PluginsConf.Enabled (regardless of default)
//
// Expected: deps.Registry is non-nil; factories must return valid plugins.
// Returns: error if any factory fails or registration fails.
// Side effects: instantiates plugins and registers them in the registry.
func LoadBuiltins(deps Deps) error {
	builtinMu.RLock()
	registrations := make([]Registration, len(builtinRegistrations))
	copy(registrations, builtinRegistrations)
	builtinMu.RUnlock()

	slices.SortStableFunc(registrations, func(a, b Registration) int {
		return a.Order - b.Order
	})

	for _, r := range registrations {
		if !isEnabled(r, deps.PluginsConfig) {
			continue
		}
		plugin, err := r.Factory(deps)
		if err != nil {
			return fmt.Errorf("loading builtin plugin: %w", err)
		}
		if err := deps.Registry.Register(plugin); err != nil {
			return fmt.Errorf("registering builtin plugin %s: %w", plugin.Name(), err)
		}
	}

	return nil
}
