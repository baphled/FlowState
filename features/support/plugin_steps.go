//go:build e2e

package support

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/plugin"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/eventlogger"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/plugin/external"
	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/plugin/manifest"
	"github.com/baphled/flowstate/internal/provider"
)

// PluginStepDefinitions holds state for plugin BDD step definitions.
type PluginStepDefinitions struct {
	tmpDir      string
	registry    *plugin.Registry
	discoverer  *external.Discoverer
	manifests   []*manifest.Manifest
	discoverErr error
	bus         *eventbus.EventBus
	health      *failover.HealthManager
	hook        *failover.Hook
	logger      *eventlogger.EventLogger
	logPath     string
	lifecycle   *external.LifecycleManager
	spawner     *testSpawner
	lastReq     *provider.ChatRequest
}

// testSpawner is a mock spawner for BDD tests.
type testSpawner struct {
	mu        sync.Mutex
	processes map[string]*testProcess
}

// testProcess tracks a mock plugin process for crash simulation.
type testProcess struct {
	done     chan struct{}
	serverR  *io.PipeReader
	serverW  *io.PipeWriter
	clientR  *io.PipeReader
	clientW  *io.PipeWriter
	stopOnce sync.Once
}

// newTestSpawner creates a new testSpawner with an empty process map.
//
// Returns:
//   - A pointer to a new testSpawner.
//
// Side effects:
//   - None.
func newTestSpawner() *testSpawner {
	return &testSpawner{
		processes: make(map[string]*testProcess),
	}
}

// Spawn creates a mock plugin process with JSON-RPC support.
//
// Expected:
//   - m contains a valid manifest with a Name field.
//
// Returns:
//   - A PluginProcess backed by in-memory pipes, and nil error.
//
// Side effects:
//   - Starts a goroutine to serve mock JSON-RPC responses.
//   - Stores the process in s.processes for later crash simulation.
func (s *testSpawner) Spawn(_ context.Context, m *manifest.Manifest) (*external.PluginProcess, error) {
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()
	done := make(chan struct{})

	tp := &testProcess{
		done:    done,
		serverR: serverR,
		serverW: serverW,
		clientR: clientR,
		clientW: clientW,
	}

	go mockJSONRPCServer(serverR, serverW, done)

	s.mu.Lock()
	s.processes[m.Name] = tp
	s.mu.Unlock()

	return external.NewPluginProcess(clientR, clientW, done), nil
}

// StopProcess stops a mock plugin process by closing its pipes.
//
// Expected:
//   - name identifies the process to stop.
//   - p is the PluginProcess to kill.
//
// Returns:
//   - Error from p.Kill() if pipe closure fails, nil otherwise.
//
// Side effects:
//   - Closes the mock process pipes and done channel.
func (s *testSpawner) StopProcess(name string, p *external.PluginProcess) error {
	s.mu.Lock()
	tp, ok := s.processes[name]
	s.mu.Unlock()
	if ok {
		tp.stop()
	}
	return p.Kill()
}

// crashPlugin simulates a plugin crash by closing its done channel.
//
// Expected:
//   - name matches a previously spawned process.
//
// Side effects:
//   - Closes the done channel and server pipes for the named process.
func (s *testSpawner) crashPlugin(name string) {
	s.mu.Lock()
	tp, ok := s.processes[name]
	s.mu.Unlock()
	if ok {
		tp.stop()
	}
}

// stop closes the done channel and server pipes exactly once.
//
// Side effects:
//   - Closes tp.done, tp.serverR, and tp.serverW.
func (tp *testProcess) stop() {
	tp.stopOnce.Do(func() {
		close(tp.done)
		tp.serverR.Close()
		tp.serverW.Close()
	})
}

// mockJSONRPCServer reads JSON-RPC requests and responds with success until done closes.
//
// Expected:
//   - r provides JSON-RPC request data; w receives JSON-RPC responses.
//
// Side effects:
//   - Reads from r and writes to w in a loop until done closes or an error occurs.
func mockJSONRPCServer(r io.Reader, w io.Writer, done <-chan struct{}) {
	dec := json.NewDecoder(r)
	enc := json.NewEncoder(w)
	for {
		select {
		case <-done:
			return
		default:
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return
		}
		var req struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(raw, &req); err != nil {
			return
		}
		resp := struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int64           `json:"id"`
			Result  json.RawMessage `json:"result"`
		}{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  json.RawMessage("{}"),
		}
		if err := enc.Encode(resp); err != nil {
			return
		}
	}
}

// RegisterPluginSteps registers plugin-specific step definitions.
//
// Expected:
//   - ctx is a godog ScenarioContext.
//
// Side effects:
//   - Registers plugin step definitions with the godog context.
//   - Cleans up temporary directories after each scenario.
func RegisterPluginSteps(ctx *godog.ScenarioContext) {
	p := &PluginStepDefinitions{}

	ctx.Before(func(bctx context.Context, _ *godog.Scenario) (context.Context, error) {
		p.registry = nil
		p.discoverer = nil
		p.manifests = nil
		p.discoverErr = nil
		p.bus = nil
		p.health = nil
		p.hook = nil
		p.logger = nil
		p.logPath = ""
		p.lifecycle = nil
		p.spawner = nil
		p.lastReq = nil
		return bctx, nil
	})

	ctx.After(func(bctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if p.logger != nil {
			p.logger.Close()
		}
		if p.lifecycle != nil {
			p.lifecycle.Stop(context.Background())
		}
		if p.tmpDir != "" {
			os.RemoveAll(p.tmpDir)
			p.tmpDir = ""
		}
		return bctx, nil
	})

	ctx.Step(`^FlowState is configured with an empty plugins directory$`, p.flowstateIsConfiguredWithAnEmptyPluginsDirectory)
	ctx.Step(`^FlowState starts$`, p.flowstateStarts)
	ctx.Step(`^the plugin system initialises without error$`, p.thePluginSystemInitialisesWithoutError)
	ctx.Step(`^no external plugins are loaded$`, p.noExternalPluginsAreLoaded)
	ctx.Step(`^a valid plugin manifest exists in the plugins directory$`, p.aValidPluginManifestExistsInThePluginsDirectory)
	ctx.Step(`^the plugin is registered in the plugin registry$`, p.thePluginIsRegisteredInThePluginRegistry)
	ctx.Step(`^a malformed plugin manifest exists in the plugins directory$`, p.aMalformedPluginManifestExistsInThePluginsDirectory)
	ctx.Step(`^FlowState continues running$`, p.flowstateContinuesRunning)
	ctx.Step(`^the malformed plugin is not loaded$`, p.theMalformedPluginIsNotLoaded)
	ctx.Step(`^FlowState has started with the plugin loaded$`, p.flowstateHasStartedWithThePluginLoaded)
	ctx.Step(`^the plugin process crashes$`, p.thePluginProcessCrashes)
	ctx.Step(`^the crashed plugin is removed from the registry$`, p.theCrashedPluginIsRemovedFromTheRegistry)
	ctx.Step(`^FlowState is running with the failover plugin active$`, p.flowstateIsRunningWithTheFailoverPluginActive)
	ctx.Step(`^a provider returns a rate-limit error$`, p.aProviderReturnsARateLimitError)
	ctx.Step(`^the failover hook switches to an alternative provider$`, p.theFailoverHookSwitchesToAnAlternativeProvider)
	ctx.Step(`^FlowState is running with the event logger active$`, p.flowstateIsRunningWithTheEventLoggerActive)
	ctx.Step(`^a session is created$`, p.aSessionIsCreated)
	ctx.Step(`^an event is written to the event log file$`, p.anEventIsWrittenToTheEventLogFile)
}

// ensureTmpDir creates a temporary directory if one does not already exist.
//
// Returns:
//   - nil on success, error if directory creation fails.
//
// Side effects:
//   - Sets p.tmpDir.
func (p *PluginStepDefinitions) ensureTmpDir() error {
	if p.tmpDir != "" {
		return nil
	}
	dir, err := os.MkdirTemp("", "plugin-bdd-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	p.tmpDir = dir
	return nil
}

// ensureRegistry initialises the plugin registry if not already set.
//
// Side effects:
//   - Sets p.registry.
func (p *PluginStepDefinitions) ensureRegistry() {
	if p.registry == nil {
		p.registry = plugin.NewRegistry()
	}
}

// flowstateIsConfiguredWithAnEmptyPluginsDirectory creates an empty temp plugins directory.
//
// Returns:
//   - nil on success, error if temp dir creation fails.
//
// Side effects:
//   - Sets p.tmpDir, p.registry, and p.discoverer.
func (p *PluginStepDefinitions) flowstateIsConfiguredWithAnEmptyPluginsDirectory() error {
	if err := p.ensureTmpDir(); err != nil {
		return err
	}
	p.ensureRegistry()
	cfg := config.PluginsConfig{Dir: p.tmpDir}
	p.discoverer = external.NewDiscoverer(cfg)
	return nil
}

// flowstateStarts runs plugin discovery on the configured plugins directory.
//
// Returns:
//   - nil (always; discovery errors stored in p.discoverErr).
//
// Side effects:
//   - Sets p.manifests and p.discoverErr.
func (p *PluginStepDefinitions) flowstateStarts() error {
	p.manifests, p.discoverErr = p.discoverer.Discover(p.tmpDir)
	return nil
}

// thePluginSystemInitialisesWithoutError verifies discovery completed without error.
//
// Returns:
//   - nil if no error, error with discovery failure otherwise.
//
// Side effects:
//   - None.
func (p *PluginStepDefinitions) thePluginSystemInitialisesWithoutError() error {
	if p.discoverErr != nil {
		return fmt.Errorf("expected no error, got: %w", p.discoverErr)
	}
	return nil
}

// noExternalPluginsAreLoaded verifies no manifests were discovered and registry is empty.
//
// Returns:
//   - nil if both conditions met, error otherwise.
//
// Side effects:
//   - None.
func (p *PluginStepDefinitions) noExternalPluginsAreLoaded() error {
	if len(p.manifests) != 0 {
		return fmt.Errorf("expected 0 manifests, got %d", len(p.manifests))
	}
	if len(p.registry.Names()) != 0 {
		return fmt.Errorf("expected empty registry, got %d plugins", len(p.registry.Names()))
	}
	return nil
}

// aValidPluginManifestExistsInThePluginsDirectory writes a valid manifest to the temp dir.
//
// Returns:
//   - nil on success, error if manifest creation fails.
//
// Side effects:
//   - Creates a plugin subdirectory with manifest.json in p.tmpDir.
func (p *PluginStepDefinitions) aValidPluginManifestExistsInThePluginsDirectory() error {
	if err := p.ensureTmpDir(); err != nil {
		return err
	}
	p.ensureRegistry()
	if p.discoverer == nil {
		cfg := config.PluginsConfig{Dir: p.tmpDir}
		p.discoverer = external.NewDiscoverer(cfg)
	}
	return writeValidManifest(p.tmpDir, "test-plugin")
}

// thePluginIsRegisteredInThePluginRegistry registers discovered manifests and verifies presence.
//
// Returns:
//   - nil if test-plugin found in registry, error otherwise.
//
// Side effects:
//   - Registers discovered plugins in p.registry.
func (p *PluginStepDefinitions) thePluginIsRegisteredInThePluginRegistry() error {
	if len(p.manifests) == 0 {
		return errors.New("no manifests discovered")
	}
	for _, m := range p.manifests {
		tp := &testPlugin{name: m.Name, version: m.Version}
		if err := p.registry.Register(tp); err != nil {
			return fmt.Errorf("registering plugin %q: %w", m.Name, err)
		}
	}
	if _, ok := p.registry.Get("test-plugin"); !ok {
		return errors.New("plugin 'test-plugin' not found in registry")
	}
	return nil
}

// aMalformedPluginManifestExistsInThePluginsDirectory writes invalid JSON to the temp dir.
//
// Returns:
//   - nil on success, error if file creation fails.
//
// Side effects:
//   - Creates a plugin subdirectory with malformed manifest.json in p.tmpDir.
func (p *PluginStepDefinitions) aMalformedPluginManifestExistsInThePluginsDirectory() error {
	if err := p.ensureTmpDir(); err != nil {
		return err
	}
	p.ensureRegistry()
	if p.discoverer == nil {
		cfg := config.PluginsConfig{Dir: p.tmpDir}
		p.discoverer = external.NewDiscoverer(cfg)
	}
	return writeMalformedManifest(p.tmpDir, "bad-plugin")
}

// flowstateContinuesRunning verifies the registry is operational by registering and removing a plugin.
//
// Returns:
//   - nil if registry operations succeed, error otherwise.
//
// Side effects:
//   - Temporarily registers then removes a health-check plugin.
func (p *PluginStepDefinitions) flowstateContinuesRunning() error {
	p.ensureRegistry()
	tp := &testPlugin{name: "health-check", version: "1.0.0"}
	if err := p.registry.Register(tp); err != nil {
		return fmt.Errorf("registry health check failed: %w", err)
	}
	p.registry.Remove("health-check")
	return nil
}

// theMalformedPluginIsNotLoaded verifies the malformed plugin was not discovered.
//
// Returns:
//   - nil if bad-plugin absent from manifests, error otherwise.
//
// Side effects:
//   - None.
func (p *PluginStepDefinitions) theMalformedPluginIsNotLoaded() error {
	if p.discoverErr != nil {
		return fmt.Errorf("discovery returned error: %w", p.discoverErr)
	}
	for _, m := range p.manifests {
		if m.Name == "bad-plugin" {
			return errors.New("malformed plugin should not have been discovered")
		}
	}
	return nil
}

// flowstateHasStartedWithThePluginLoaded starts the lifecycle manager with a mock spawner.
//
// Returns:
//   - nil on success, error if lifecycle start fails.
//
// Side effects:
//   - Sets p.spawner and p.lifecycle; spawns mock plugin processes.
func (p *PluginStepDefinitions) flowstateHasStartedWithThePluginLoaded() error {
	p.spawner = newTestSpawner()
	p.lifecycle = external.NewLifecycleManager(p.spawner, p.registry)
	return p.lifecycle.Start(context.Background(), p.manifests)
}

// thePluginProcessCrashes simulates a plugin crash by closing the mock process done channel.
//
// Returns:
//   - nil (always).
//
// Side effects:
//   - Closes the mock process done channel and waits for the crash watcher goroutine.
func (p *PluginStepDefinitions) thePluginProcessCrashes() error {
	p.spawner.crashPlugin("test-plugin")
	time.Sleep(100 * time.Millisecond)
	return nil
}

// theCrashedPluginIsRemovedFromTheRegistry verifies the crashed plugin is no longer registered.
//
// Returns:
//   - nil if plugin absent, error if still present.
//
// Side effects:
//   - None.
func (p *PluginStepDefinitions) theCrashedPluginIsRemovedFromTheRegistry() error {
	if _, ok := p.registry.Get("test-plugin"); ok {
		return errors.New("crashed plugin 'test-plugin' still in registry")
	}
	return nil
}

// flowstateIsRunningWithTheFailoverPluginActive sets up the failover chain and hook.
//
// Returns:
//   - nil (always).
//
// Side effects:
//   - Sets p.health and p.hook with a three-tier failover chain.
func (p *PluginStepDefinitions) flowstateIsRunningWithTheFailoverPluginActive() error {
	p.health = failover.NewHealthManager()
	chain := failover.NewFallbackChain([]failover.ProviderModel{
		{Provider: "anthropic", Model: "claude-sonnet-4-20250514"},
		{Provider: "openai", Model: "gpt-4o"},
		{Provider: "ollama", Model: "llama3.2"},
	}, map[string]string{
		"claude-sonnet-4-20250514": failover.Tier0,
		"gpt-4o":                   failover.Tier1,
		"llama3.2":                 failover.Tier2,
	})
	p.hook = failover.NewHook(chain, p.health)
	return nil
}

// aProviderReturnsARateLimitError marks the primary provider as rate-limited.
//
// Returns:
//   - nil on success, error if persisting rate-limit state fails.
//
// Side effects:
//   - Marks anthropic/claude-sonnet-4-20250514 as rate-limited in p.health.
func (p *PluginStepDefinitions) aProviderReturnsARateLimitError() error {
	retryAfter := time.Now().Add(1 * time.Hour)
	p.health.MarkRateLimited("anthropic", "claude-sonnet-4-20250514", retryAfter)
	return nil
}

// theFailoverHookSwitchesToAnAlternativeProvider verifies the hook selects a different provider.
//
// Returns:
//   - nil if provider was switched, error if still using the rate-limited provider.
//
// Side effects:
//   - Sets p.lastReq.
func (p *PluginStepDefinitions) theFailoverHookSwitchesToAnAlternativeProvider() error {
	req := &provider.ChatRequest{
		Provider: "anthropic",
		Model:    "claude-sonnet-4-20250514",
	}
	if err := p.hook.Apply(context.Background(), req); err != nil {
		return fmt.Errorf("failover hook Apply failed: %w", err)
	}
	if req.Provider == "anthropic" && req.Model == "claude-sonnet-4-20250514" {
		return errors.New("provider was not switched after rate limit")
	}
	p.lastReq = req
	return nil
}

// flowstateIsRunningWithTheEventLoggerActive sets up an event logger writing to a temp file.
//
// Returns:
//   - nil on success, error if temp dir or logger start fails.
//
// Side effects:
//   - Sets p.bus, p.logger, and p.logPath; opens a JSONL log file.
func (p *PluginStepDefinitions) flowstateIsRunningWithTheEventLoggerActive() error {
	if err := p.ensureTmpDir(); err != nil {
		return err
	}
	p.logPath = filepath.Join(p.tmpDir, "events.jsonl")
	p.bus = eventbus.NewEventBus()
	p.logger = eventlogger.New(p.logPath, 1024*1024)
	return p.logger.Start(p.bus)
}

// aSessionIsCreated publishes a session event to the event bus.
//
// Returns:
//   - nil (always).
//
// Side effects:
//   - Publishes a session event and waits briefly for the logger to flush.
func (p *PluginStepDefinitions) aSessionIsCreated() error {
	event := events.NewSessionEvent(events.SessionEventData{
		SessionID: "test-session-001",
		UserID:    "test-user",
		Action:    "created",
	})
	p.bus.Publish("session.created", event)
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		data, err := os.ReadFile(p.logPath)
		if err == nil && len(data) > 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("event log file is empty")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// anEventIsWrittenToTheEventLogFile verifies the JSONL log contains the session event.
//
// Returns:
//   - nil if log file contains the expected session ID, error otherwise.
//
// Side effects:
//   - None.
func (p *PluginStepDefinitions) anEventIsWrittenToTheEventLogFile() error {
	data, err := os.ReadFile(p.logPath)
	if err != nil {
		return fmt.Errorf("reading event log: %w", err)
	}
	if len(data) == 0 {
		return errors.New("event log file is empty")
	}
	if !strings.Contains(string(data), "test-session-001") {
		return fmt.Errorf("expected session ID in log, got: %s", string(data))
	}
	return nil
}

// testPlugin is a minimal Plugin implementation for BDD tests.
type testPlugin struct {
	name    string
	version string
}

// Init initialises the test plugin.
//
// Returns:
//   - nil (always).
//
// Side effects:
//   - None.
func (tp *testPlugin) Init() error { return nil }

// Name returns the test plugin's name.
//
// Returns:
//   - The plugin name string.
//
// Side effects:
//   - None.
func (tp *testPlugin) Name() string { return tp.name }

// Version returns the test plugin's version.
//
// Returns:
//   - The plugin version string.
//
// Side effects:
//   - None.
func (tp *testPlugin) Version() string { return tp.version }

// writeValidManifest creates a plugin subdirectory with a valid manifest.json.
//
// Expected:
//   - baseDir is an existing directory; name is a valid directory name.
//
// Returns:
//   - nil on success, error if directory or file creation fails.
//
// Side effects:
//   - Creates directories and files on disk.
func writeValidManifest(baseDir, name string) error {
	dir := filepath.Join(baseDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating plugin dir: %w", err)
	}
	m := manifest.Manifest{
		Name:    name,
		Version: "1.0.0",
		Command: "echo",
		Args:    []string{"plugin"},
		Hooks:   []string{"event"},
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling manifest: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o600)
}

// writeMalformedManifest creates a plugin subdirectory with invalid JSON in manifest.json.
//
// Expected:
//   - baseDir is an existing directory; name is a valid directory name.
//
// Returns:
//   - nil on success, error if directory or file creation fails.
//
// Side effects:
//   - Creates directories and files on disk.
func writeMalformedManifest(baseDir, name string) error {
	dir := filepath.Join(baseDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating plugin dir: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("{not valid json!!!"), 0o600)
}
