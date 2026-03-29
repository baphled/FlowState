package external

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/plugin"
	"github.com/baphled/flowstate/internal/plugin/manifest"
)

// killProcess kills a plugin process, ignoring any error.
// This is used during cleanup when the process is already in a failed state.
//
// Expected: p must be a valid PluginProcess.
//
// Side effects: Closes stdin/stdout pipes of the process.
func killProcess(p *PluginProcess) {
	if err := p.Kill(); err != nil {
		slog.Debug("kill process error", "error", err)
	}
}

// SpawnIface is the interface for spawning plugin processes.
type SpawnIface interface {
	// Spawn starts a plugin process from the given manifest.
	Spawn(ctx context.Context, m *manifest.Manifest) (*PluginProcess, error)
	// StopProcess stops a running plugin process.
	StopProcess(name string, p *PluginProcess) error
}

// LifecycleManager manages the start, stop, and crash recovery of external plugins.
type LifecycleManager struct {
	spawner  SpawnIface
	registry *plugin.Registry
	mu       sync.Mutex
	active   []*activePlugin
}

// activePlugin tracks a running external plugin process.
type activePlugin struct {
	name    string
	process *PluginProcess
	cancel  context.CancelFunc
}

// NewLifecycleManager returns a new LifecycleManager.
//
// Expected:
//   - spawner: interface for spawning plugin processes
//   - registry: plugin registry to register plugins with
//
// Returns: a new LifecycleManager instance.
//
// Side effects: None.
func NewLifecycleManager(spawner SpawnIface, registry *plugin.Registry) *LifecycleManager {
	return &LifecycleManager{
		spawner:  spawner,
		registry: registry,
	}
}

// Start spawns each plugin, runs initialize RPC, and registers it.
//
// Expected:
//   - ctx: context passed to each plugin's spawn and initialization
//   - manifests: slice of plugin manifests to start
//
// Returns: an error if any plugin fails to start, nil otherwise.
//
// Side effects: Starts multiple OS processes and registers plugins.
func (m *LifecycleManager) Start(ctx context.Context, manifests []*manifest.Manifest) error {
	var errs []error
	for _, mfst := range manifests {
		if err := m.startOne(ctx, mfst); err != nil {
			errs = append(errs, fmt.Errorf("plugin %q: %w", mfst.Name, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("lifecycle start errors: %v", errs)
	}
	return nil
}

// startOne spawns and initializes a single plugin.
//
// Expected:
//   - ctx: context for spawn and init operations
//   - mfst: manifest for the plugin to start
//
// Returns: an error if spawning or initialization fails, nil otherwise.
//
// Side effects: Starts an OS process and registers the plugin.
func (m *LifecycleManager) startOne(ctx context.Context, mfst *manifest.Manifest) error {
	process, err := m.spawner.Spawn(ctx, mfst)
	if err != nil {
		return fmt.Errorf("spawn: %w", err)
	}

	timeout := time.Duration(mfst.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	initCtx, initCancel := context.WithTimeout(ctx, timeout)
	defer initCancel()

	client := NewJSONRPCClient(process)
	_, initErr := client.Call(initCtx, "initialize", nil)
	if initErr != nil {
		killProcess(process)
		if initCtx.Err() != nil {
			return fmt.Errorf("timeout initialising plugin %q", mfst.Name)
		}
		return fmt.Errorf("initialize: %w", initErr)
	}

	ext := &ProcessPlugin{
		name:    mfst.Name,
		version: mfst.Version,
		client:  client,
		hooks:   convertManifestHooks(mfst.Hooks),
	}
	if regErr := m.registry.Register(ext); regErr != nil {
		killProcess(process)
		return fmt.Errorf("register: %w", regErr)
	}

	watchCtx, watchCancel := context.WithCancel(ctx)
	ap := &activePlugin{name: mfst.Name, process: process, cancel: watchCancel}

	m.mu.Lock()
	m.active = append(m.active, ap)
	m.mu.Unlock()

	go func() {
		defer watchCancel()
		select {
		case <-watchCtx.Done():
		case <-process.Done():
			slog.Warn("external plugin crashed", "plugin", mfst.Name)
			m.registry.Remove(mfst.Name)
			m.removeActive(mfst.Name)
		}
	}()

	return nil
}

// convertManifestHooks converts a slice of hook names to HookType values.
//
// Expected: hooks is a slice of string hook names from the manifest.
// Returns: slice of plugin.HookType.
// Side effects: None.
func convertManifestHooks(hooks []string) []plugin.HookType {
	hookTypes := make([]plugin.HookType, len(hooks))
	for i, h := range hooks {
		hookTypes[i] = plugin.HookType(h)
	}
	return hookTypes
}

// removeActive removes an active plugin from the tracking slice.
//
// Expected: name of the plugin to remove.
// Returns: None.
// Side effects: Modifies the active slice.
func (m *LifecycleManager) removeActive(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	updated := m.active[:0]
	for _, ap := range m.active {
		if ap.name != name {
			updated = append(updated, ap)
		}
	}
	m.active = updated
}

// Stop sends shutdown RPC to each plugin in reverse order, then kills them.
//
// Expected: ctx is unused but kept for interface compatibility.
// Returns: an error if any plugin fails to stop, nil otherwise.
// Side effects: Stops and kills multiple plugin processes.
func (m *LifecycleManager) Stop(_ context.Context) error {
	m.mu.Lock()
	active := make([]*activePlugin, len(m.active))
	copy(active, m.active)
	m.active = nil
	m.mu.Unlock()

	var errs []error
	for i := len(active) - 1; i >= 0; i-- {
		ap := active[i]
		ap.cancel()
		if stopErr := m.spawner.StopProcess(ap.name, ap.process); stopErr != nil {
			errs = append(errs, fmt.Errorf("stop %q: %w", ap.name, stopErr))
		}
		m.registry.Remove(ap.name)
	}
	if len(errs) > 0 {
		return fmt.Errorf("lifecycle stop errors: %v", errs)
	}
	return nil
}

// ProcessPlugin wraps an external plugin process.
type ProcessPlugin struct {
	name    string
	version string
	client  *JSONRPCClient
	hooks   []plugin.HookType
}

// Name returns the plugin's name.
//
// Expected: None.
// Returns: the plugin's name.
// Side effects: None.
func (p *ProcessPlugin) Name() string { return p.name }

// Version returns the plugin's version.
//
// Expected: None.
// Returns: the plugin's version.
// Side effects: None.
func (p *ProcessPlugin) Version() string { return p.version }

// Init initializes the plugin.
//
// Expected: None.
// Returns: nil as external plugins are initialized via RPC.
// Side effects: None.
func (p *ProcessPlugin) Init() error {
	return nil
}

// Hooks returns the plugin's hooks.
//
// Expected: Returns a map of hook types to implementations from the hooks field.
// Returns: map[plugin.HookType]interface{}.
// Side effects: None.
func (p *ProcessPlugin) Hooks() map[plugin.HookType]interface{} {
	m := make(map[plugin.HookType]interface{}, len(p.hooks))
	for _, h := range p.hooks {
		m[h] = p.client
	}
	return m
}

// Shutdown shuts down the plugin.
//
// Expected: None.
// Returns: nil as external plugins are shut down via lifecycle manager.
// Side effects: None.
func (p *ProcessPlugin) Shutdown() error { return nil }
