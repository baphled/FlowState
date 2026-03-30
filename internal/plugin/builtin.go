package plugin

import (
	"errors"
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
	loadResults          map[*Registry]error
)

// ResetBuiltins clears all registered builtin factories and resets idempotency state.
// This is intended for testing only.
//
// Expected: called during test setup to restore a clean slate.
// Returns: nothing.
// Side effects: clears the global factory slice and resets the once guard.
func ResetBuiltins() {
	builtinMu.Lock()
	defer builtinMu.Unlock()
	builtinRegistrations = nil
	loadResults = nil
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

// safeCallFactory invokes a factory, converting any panic into an error.
//
// Expected: name is the plugin name; factory is non-nil.
// Returns: plugin and nil on success; nil and error on failure or panic.
// Side effects: may allocate resources if the factory succeeds.
func safeCallFactory(name string, factory func(Deps) (Plugin, error), deps Deps) (p Plugin, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("plugin %q factory panic: %v", name, r)
		}
	}()
	return factory(deps)
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
// skipping those disabled by PluginsConf. It is idempotent — subsequent
// calls return the result of the first call without re-running factories.
//
// A plugin is loaded if:
//   - EnabledByDefault is true AND it is not in PluginsConf.Disabled
//   - OR it is explicitly listed in PluginsConf.Enabled (regardless of default)
//
// Expected: deps.Registry is non-nil; call once per registry during application startup.
// Returns: error if any factory fails or registration fails; nil on subsequent calls for the same registry.
// Side effects: instantiates plugins and registers them in the registry on the first call per registry only.
func LoadBuiltins(deps Deps) error {
	if deps.Registry == nil {
		return errors.New("loading builtin plugins: registry is nil")
	}

	builtinMu.Lock()
	if loadResults == nil {
		loadResults = make(map[*Registry]error)
	}
	if err, ok := loadResults[deps.Registry]; ok {
		builtinMu.Unlock()
		return err
	}
	builtinMu.Unlock()

	err := doLoadBuiltins(deps)

	builtinMu.Lock()
	loadResults[deps.Registry] = err
	builtinMu.Unlock()

	return err
}

// doLoadBuiltins is the implementation called exactly once by LoadBuiltins.
//
// Expected: deps.Registry is non-nil; called only via LoadBuiltins.
// Returns: error if any factory fails, returns nil plugin, or config is invalid.
// Side effects: instantiates plugins and registers them in deps.Registry.
func doLoadBuiltins(deps Deps) error {
	for _, name := range deps.PluginsConfig.Enabled {
		if slices.Contains(deps.PluginsConfig.Disabled, name) {
			return fmt.Errorf("plugin config conflict: %q is in both Enabled and Disabled lists", name)
		}
	}

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
		plug, err := safeCallFactory(r.Name, r.Factory, deps)
		if err != nil {
			return fmt.Errorf("loading builtin plugin %q: %w", r.Name, err)
		}
		if plug == nil {
			return fmt.Errorf("loading builtin plugin %q: factory returned nil plugin", r.Name)
		}
		if err := deps.Registry.Register(plug); err != nil {
			return fmt.Errorf("registering builtin plugin %q: %w", r.Name, err)
		}
	}

	return nil
}
