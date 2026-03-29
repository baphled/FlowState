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

type activePlugin struct {
	name    string
	process *PluginProcess
	cancel  context.CancelFunc
}

// NewLifecycleManager returns a new LifecycleManager.
func NewLifecycleManager(spawner SpawnIface, registry *plugin.Registry) *LifecycleManager {
	return &LifecycleManager{
		spawner:  spawner,
		registry: registry,
	}
}

// Start spawns each plugin, runs initialize RPC, and registers it.
//
// The ctx parameter is passed to each plugin's spawn and initialization.
//
//go:context(ctx)
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
// The ctx parameter is used for spawn and init operations.
//
//go:context(ctx)
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

	ext := &externalPlugin{
		name:    mfst.Name,
		version: mfst.Version,
		client:  client,
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
// The ctx parameter is unused but kept for interface compatibility.
//
//go:context(ctx)
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

// externalPlugin is a Plugin wrapping an external process.
type externalPlugin struct {
	name    string
	version string
	client  *JSONRPCClient
}

// Name returns the plugin's name.
func (p *externalPlugin) Name() string { return p.name }

// Version returns the plugin's version.
func (p *externalPlugin) Version() string { return p.version }

// Init initializes the plugin.
func (p *externalPlugin) Init() error {
	return nil
}

// Hooks returns the plugin's hooks.
func (p *externalPlugin) Hooks() map[plugin.HookType]interface{} {
	return map[plugin.HookType]interface{}{
		plugin.EventType: p.client,
	}
}

// Shutdown shuts down the plugin.
func (p *externalPlugin) Shutdown() error { return nil }
