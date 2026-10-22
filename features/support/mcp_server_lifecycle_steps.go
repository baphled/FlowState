//go:build e2e

package support

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/app"
	appmcp "github.com/baphled/flowstate/internal/app/mcp"
	"github.com/baphled/flowstate/internal/cli"
	"github.com/baphled/flowstate/internal/config"
	mcpclient "github.com/baphled/flowstate/internal/mcp"
)

var (
	errDisabledExpectedResolvedEnabled       = errors.New("server carried an explicit enabled false but resolved to enabled")
	errEnabledExpectedResolvedDisabled       = errors.New("server was expected to resolve to enabled but resolved to disabled")
	errNoLoadedConfig                        = errors.New("no configuration has been loaded in this scenario")
	errConnectAttemptedForDisabledServer     = errors.New("a connect attempt was made for a server configured with enabled false")
	errNoDeferredApp                         = errors.New("no deferred-bootstrap application was constructed in this scenario")
	errRebuildDidNotSwapApp                  = errors.New("the bootstrap-annotated command did not rebuild the application")
	errNoRecordingClients                    = errors.New("no recording MCP clients were created during the scenario")
	errPreRebuildClientNotDisconnected       = errors.New("the pre-rebuild application's MCP manager was not disconnected at the rebuild swap")
	errPreRebuildClientStillLive             = errors.New("the pre-rebuild application still holds live MCP connections")
	errExpectedExactlyOneLiveConnectionCount = "expected exactly one live connection after the rebuild, got %d"
	errServerNotFoundInConfigFormat          = "server %q not found in the loaded configuration"
)

// MCPServerLifecycleState holds state for the MCP enablement and
// app-rebuild lifecycle scenarios.
type MCPServerLifecycleState struct {
	configPath   string
	loadedConfig *config.AppConfig
	connectLog   *countingMCPClient
	recorders    []*recordingMCPClient
	application  *app.App
	preRebuild   *app.App
	savedEnv     map[string]string
	originalCwd  string
	sandboxDir   string
}

// countingMCPClient is a no-op mcpclient.Client that records every
// Connect call's server name.
type countingMCPClient struct {
	connectNames []string
}

func (c *countingMCPClient) Connect(_ context.Context, cfg mcpclient.ServerConfig) error {
	c.connectNames = append(c.connectNames, cfg.Name)
	return nil
}

func (c *countingMCPClient) Disconnect(_ string) error { return nil }

func (c *countingMCPClient) ListTools(_ context.Context, _ string) ([]mcpclient.ToolInfo, error) {
	return nil, nil
}

func (c *countingMCPClient) CallTool(_ context.Context, _, _ string, _ map[string]any) (*mcpclient.ToolResult, error) {
	return &mcpclient.ToolResult{}, nil
}

func (c *countingMCPClient) DisconnectAll() error { return nil }

// recordingMCPClient is an mcpclient.Client that tracks live
// connections per server name plus DisconnectAll invocations. Each App
// construction draws a fresh instance so per-App managers stay
// distinguishable, mirroring production subprocess ownership.
type recordingMCPClient struct {
	liveConnections map[string]int
	disconnectAlls  int
}

func newRecordingMCPClient() *recordingMCPClient {
	return &recordingMCPClient{liveConnections: make(map[string]int)}
}

func (r *recordingMCPClient) Connect(_ context.Context, cfg mcpclient.ServerConfig) error {
	r.liveConnections[cfg.Name]++
	return nil
}

func (r *recordingMCPClient) Disconnect(name string) error {
	delete(r.liveConnections, name)
	return nil
}

func (r *recordingMCPClient) ListTools(_ context.Context, _ string) ([]mcpclient.ToolInfo, error) {
	return nil, nil
}

func (r *recordingMCPClient) CallTool(_ context.Context, _, _ string, _ map[string]any) (*mcpclient.ToolResult, error) {
	return &mcpclient.ToolResult{}, nil
}

func (r *recordingMCPClient) DisconnectAll() error {
	r.disconnectAlls++
	r.liveConnections = make(map[string]int)
	return nil
}

// RegisterMCPserverLifecycleSteps registers the MCP server enablement
// and app-rebuild lifecycle steps with the Godog scenario context.
//
// Expected:
//   - ctx is a valid Godog ScenarioContext.
//
// Side effects:
//   - Registers all MCP lifecycle scenario steps with the provided context.
func RegisterMCPServerLifecycleSteps(ctx *godog.ScenarioContext) {
	state := &MCPServerLifecycleState{}

	ctx.Step(`^a FlowState configuration file with MCP server "([^"]*)" set to enabled false$`, state.aConfigWithMCPServerDisabled)
	ctx.Step(`^a FlowState configuration file with MCP server "([^"]*)" and no enabled key$`, state.aConfigWithMCPServerAndNoEnabledKey)
	ctx.Step(`^FlowState loads its configuration from that file$`, state.flowstateLoadsItsConfigFromThatFile)
	ctx.Step(`^MCP server "([^"]*)" resolves to disabled$`, state.mcpServerResolvesToDisabled)
	ctx.Step(`^MCP server "([^"]*)" resolves to enabled$`, state.mcpServerResolvesToEnabled)
	ctx.Step(`^connecting the configured MCP servers makes no connection attempt to "([^"]*)"$`, state.connectingConfiguredServersSkips)
	ctx.Step(`^a deferred-bootstrap FlowState application with MCP server "([^"]*)" on a recording MCP client$`, state.aDeferredBootstrapAppWithRecordingClient)
	ctx.Step(`^a bootstrap-annotated command runs and rebuilds the application$`, state.aBootstrapAnnotatedCommandRunsAndRebuilds)
	ctx.Step(`^the pre-rebuild application's MCP connections are torn down$`, state.preRebuildConnectionsTornDown)
	ctx.Step(`^exactly one live connection to MCP server "([^"]*)" remains$`, state.exactlyOneLiveConnectionRemains)

	ctx.After(state.restoreScenarioEnvironment)
}

// aConfigWithMCPServerDisabled writes a config file whose only MCP
// server entry carries an explicit enabled false.
//
// Expected:
//   - name is the MCP server name to disable.
//
// Returns:
//   - An error when the config file cannot be created.
//
// Side effects:
//   - Writes a temporary config.yaml and records its path.
func (s *MCPServerLifecycleState) aConfigWithMCPServerDisabled(name string) error {
	content := "mcp_servers:\n  - name: " + name + "\n    command: /usr/bin/unused\n    enabled: false\n"
	return s.writeScenarioConfig(name, content)
}

// aConfigWithMCPServerAndNoEnabledKey writes a config file whose only
// MCP server entry omits the enabled key entirely.
//
// Expected:
//   - name is the MCP server name to configure.
//
// Returns:
//   - An error when the config file cannot be created.
//
// Side effects:
//   - Writes a temporary config.yaml and records its path.
func (s *MCPServerLifecycleState) aConfigWithMCPServerAndNoEnabledKey(name string) error {
	content := "mcp_servers:\n  - name: " + name + "\n    command: /usr/bin/unused\n"
	return s.writeScenarioConfig(name, content)
}

// writeScenarioConfig materialises the scenario's config.yaml plus the
// sandboxed HOME and XDG directories the heavy rebuild step needs.
//
// Expected:
//   - name is the MCP server the scenario centres on.
//   - content is the raw YAML to write.
//
// Returns:
//   - An error when the sandbox or config file cannot be created.
//
// Side effects:
//   - Creates a scenario sandbox directory tree.
//   - Points HOME and the XDG base dirs at the sandbox (restored by the
//     scenario After hook).
func (s *MCPServerLifecycleState) writeScenarioConfig(name, content string) error {
	sandbox, err := os.MkdirTemp("", "flowstate-mcp-lifecycle-")
	if err != nil {
		return err
	}
	s.sandboxDir = sandbox
	configDir := filepath.Join(sandbox, "config", "flowstate")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return err
	}
	s.configPath = filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(s.configPath, []byte(content), 0o600); err != nil {
		return err
	}
	return s.isolateScenarioEnvironment()
}

// isolateScenarioEnvironment redirects HOME and the XDG base dirs into
// the scenario sandbox so config loads and the app rebuild never touch
// the operator's real locations.
//
// Returns:
//   - An error when an environment variable cannot be set.
//
// Side effects:
//   - Mutates HOME, XDG_CONFIG_HOME, XDG_DATA_HOME, and QDRANT_URL.
//   - Records the prior values for the After-hook restore.
func (s *MCPServerLifecycleState) isolateScenarioEnvironment() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	s.originalCwd = cwd
	overrides := map[string]string{
		"HOME":            filepath.Join(s.sandboxDir, "home"),
		"XDG_CONFIG_HOME": filepath.Join(s.sandboxDir, "config"),
		"XDG_DATA_HOME":   filepath.Join(s.sandboxDir, "data"),
		"QDRANT_URL":      "",
	}
	s.savedEnv = make(map[string]string, len(overrides))
	for key, value := range overrides {
		s.savedEnv[key] = os.Getenv(key)
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	return nil
}

// restoreScenarioEnvironment is the Godog After hook returning the
// process environment and working directory to their pre-scenario
// state.
//
// Expected:
//   - ctx is the scenario context.
//   - scenario is the finished scenario.
//
// Returns:
//   - The context, unchanged.
//   - An error when the working directory cannot be restored.
//
// Side effects:
//   - Restores saved environment variables and removes the sandbox.
func (s *MCPServerLifecycleState) restoreScenarioEnvironment(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
	if s.originalCwd != "" {
		if err := os.Chdir(s.originalCwd); err != nil {
			return ctx, err
		}
		s.originalCwd = ""
	}
	for key, value := range s.savedEnv {
		if value == "" {
			_ = os.Unsetenv(key)
		} else {
			_ = os.Setenv(key, value)
		}
	}
	s.savedEnv = nil
	if s.sandboxDir != "" {
		_ = os.RemoveAll(s.sandboxDir)
		s.sandboxDir = ""
	}
	return ctx, nil
}

// flowstateLoadsItsConfigFromThatFile loads the scenario config file
// through the production config loader.
//
// Returns:
//   - An error when the load fails.
//
// Side effects:
//   - Stores the loaded AppConfig on the scenario state.
func (s *MCPServerLifecycleState) flowstateLoadsItsConfigFromThatFile() error {
	cfg, err := config.LoadConfigFromPath(s.configPath)
	if err != nil {
		return err
	}
	s.loadedConfig = cfg
	return nil
}

// mcpServerResolvesToDisabled asserts the named server survived config
// load with an explicit disabled state.
//
// Expected:
//   - name is the MCP server name to check.
//
// Returns:
//   - An error when the server is missing or resolves to enabled.
//
// Side effects:
//   - None.
func (s *MCPServerLifecycleState) mcpServerResolvesToDisabled(name string) error {
	server, err := s.findLoadedServer(name)
	if err != nil {
		return err
	}
	if server.EnabledOrDefault() {
		return errDisabledExpectedResolvedEnabled
	}
	return nil
}

// mcpServerResolvesToEnabled asserts the named server resolves to
// enabled after config load.
//
// Expected:
//   - name is the MCP server name to check.
//
// Returns:
//   - An error when the server is missing or resolves to disabled.
//
// Side effects:
//   - None.
func (s *MCPServerLifecycleState) mcpServerResolvesToEnabled(name string) error {
	server, err := s.findLoadedServer(name)
	if err != nil {
		return err
	}
	if !server.EnabledOrDefault() {
		return errEnabledExpectedResolvedDisabled
	}
	return nil
}

// connectingConfiguredServersSkips drives the production ConnectServers
// wiring with a counting client and asserts no connect attempt is made
// for the named server.
//
// Expected:
//   - name is the MCP server that must not be connected.
//
// Returns:
//   - An error when a connect attempt targets the named server.
//
// Side effects:
//   - Records every connect attempt on the scenario state.
func (s *MCPServerLifecycleState) connectingConfiguredServersSkips(name string) error {
	if s.loadedConfig == nil {
		return errNoLoadedConfig
	}
	s.connectLog = &countingMCPClient{}
	_, _, _ = appmcp.ConnectServers(context.Background(), s.connectLog, s.loadedConfig.MCPServers)
	for _, connected := range s.connectLog.connectNames {
		if connected == name {
			return errConnectAttemptedForDisabledServer
		}
	}
	return nil
}

// aDeferredBootstrapAppWithRecordingClient constructs the pre-bootstrap
// App exactly as the serve startup path does, with every MCP client
// drawn from a recording factory.
//
// Expected:
//   - name is the configured MCP server name.
//
// Returns:
//   - An error when the App cannot be constructed.
//
// Side effects:
//   - Creates sandbox directories and sets HOME/XDG env (After-hook
//     restored).
//   - Stores the App and its recorders on the scenario state.
func (s *MCPServerLifecycleState) aDeferredBootstrapAppWithRecordingClient(name string) error {
	sandbox, err := os.MkdirTemp("", "flowstate-mcp-lifecycle-")
	if err != nil {
		return err
	}
	s.sandboxDir = sandbox
	if err := s.isolateScenarioEnvironment(); err != nil {
		return err
	}
	cfg := config.DefaultConfig()
	cfg.MCPServers = []config.MCPServerConfig{
		{Name: name, Command: "unused-with-recording-client"},
	}
	application, err := app.NewWithOptions(cfg, app.NewOptions{
		SkipBootstrap: true,
		MCPClientFactory: func() mcpclient.Client {
			recorder := newRecordingMCPClient()
			s.recorders = append(s.recorders, recorder)
			return recorder
		},
	})
	if err != nil {
		return err
	}
	s.application = application
	s.preRebuild = application
	return nil
}

// aBootstrapAnnotatedCommandRunsAndRebuilds executes a bootstrap-
// annotated command through the real cli root, mirroring a single
// serve-style invocation: the root's PersistentPreRunE fires
// app.Bootstrap then reconstructs the App.
//
// Returns:
//   - An error when the command fails to execute.
//
// Side effects:
//   - Seeds the sandboxed XDG layout via app.Bootstrap.
//   - Swaps s.application to the rebuilt App.
func (s *MCPServerLifecycleState) aBootstrapAnnotatedCommandRunsAndRebuilds() error {
	if s.application == nil {
		return errNoDeferredApp
	}
	root := cli.NewRootCmdFromAppPointer(&s.application)
	root.SetArgs([]string{"tools", "list"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	return root.Execute()
}

// preRebuildConnectionsTornDown asserts the bootstrap rebuild
// disconnected the pre-rebuild App's MCP manager.
//
// Returns:
//   - An error when the pre-rebuild client was not torn down.
//
// Side effects:
//   - None.
func (s *MCPServerLifecycleState) preRebuildConnectionsTornDown() error {
	if s.application == s.preRebuild {
		return errRebuildDidNotSwapApp
	}
	if len(s.recorders) == 0 {
		return errNoRecordingClients
	}
	if s.recorders[0].disconnectAlls != 1 {
		return errPreRebuildClientNotDisconnected
	}
	if len(s.recorders[0].liveConnections) != 0 {
		return errPreRebuildClientStillLive
	}
	return nil
}

// exactlyOneLiveConnectionRemains asserts that across every App
// constructed by the invocation, exactly one live connection to the
// named server survives the rebuild.
//
// Expected:
//   - name is the configured MCP server name.
//
// Returns:
//   - An error when the live-connection count differs from one.
//
// Side effects:
//   - None.
func (s *MCPServerLifecycleState) exactlyOneLiveConnectionRemains(name string) error {
	total := 0
	for _, recorder := range s.recorders {
		total += recorder.liveConnections[name]
	}
	if total != 1 {
		return fmt.Errorf(errExpectedExactlyOneLiveConnectionCount, total)
	}
	return nil
}

// findLoadedServer returns the loaded config's entry for name.
//
// Expected:
//   - name is the MCP server name to look up.
//
// Returns:
//   - The matching MCPServerConfig.
//   - An error when the loaded config is absent or lacks the server.
//
// Side effects:
//   - None.
func (s *MCPServerLifecycleState) findLoadedServer(name string) (config.MCPServerConfig, error) {
	if s.loadedConfig == nil {
		return config.MCPServerConfig{}, errNoLoadedConfig
	}
	for _, server := range s.loadedConfig.MCPServers {
		if server.Name == name {
			return server, nil
		}
	}
	return config.MCPServerConfig{}, fmt.Errorf(errServerNotFoundInConfigFormat, name)
}
