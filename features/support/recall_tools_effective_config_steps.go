//go:build e2e

package support

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/engine"
	mcpclient "github.com/baphled/flowstate/internal/mcp"
)

var (
	errRecallToolsNoEngine        = errors.New("the wired FlowState application has no engine")
	errRecallToolsNoConfig        = errors.New("no FlowState configuration has been prepared in this scenario")
	errRecallToolsConfigURLFormat = "the config-file qdrant.url is %q; the scenario requires it empty"
	errRecallToolsWireFormat      = "wiring the FlowState engine failed: %w"
	errRecallToolsMissingFormat   = "tool %q is absent from the agent's registered tools; registered: %v"
	errRecallToolsPresentFormat   = "tool %q is present in the agent's registered tools; registered: %v"
	errRecallToolsSandboxFormat   = "preparing the sandboxed FlowState environment: %w"

	errRecallToolsNoConstruction   = errors.New("no engine construction ran in this scenario")
	errRecallToolsNoDelegates      = errors.New("no delegate engines were constructed; the scenario requires a second agent")
	errRecallToolsLoggedFormat     = "engine construction logged a recall registration failure; log: %s"
	errRecallToolsDelegateFormat   = "delegate engine %q did not register the recall factory tools (search_context)"
	errRecallToolsDelegateMemFmt   = "delegate engine %q does not advertise %q; advertised: %v"
	errRecallToolsDelegateLeakFmt  = "delegate engine %q advertised %q with the gate closed; advertised: %v"
	errRecallToolsPrimaryFactFmt   = "the primary engine did not register the recall factory tools (search_context)"
	errRecallToolsDelegateWriteFmt = "writing the recall-delegate-probe manifest: %w"
)

// recallProbeManifest is the on-disk agent manifest declaring the native
// recall tools so engine tool registration is observable through
// Engine.ToolSchemas without any MCP subprocess.
const recallProbeManifest = `---
schema_version: "1.0.0"
id: recall-probe
name: Recall Probe
complexity: low
capabilities:
  tools:
    - bash
    - mcp_memory_search_nodes
    - mcp_memory_open_nodes
    - mcp_vault-rag_query_vault
delegation:
  can_delegate: false
---

# Recall Probe

Declares the native recall tools so engine tool registration is observable.
`

// recallDelegateProbeManifest is the second on-disk agent manifest the
// propagation scenarios register alongside the recall probe. It gives
// the app a delegation target whose delegate-engine construction the
// "the app constructs engines for its agents" step can observe.
const recallDelegateProbeManifest = `---
schema_version: "1.0.0"
id: recall-delegate-probe
name: Recall Delegate Probe
complexity: low
capabilities:
  tools:
    - bash
    - mcp_memory_search_nodes
    - mcp_memory_open_nodes
    - mcp_vault-rag_query_vault
delegation:
  can_delegate: false
---

# Recall Delegate Probe

Declares the native recall tools so delegate engine construction is observable.
`

// recallToolsEffectiveConfigState holds per-scenario state for the
// effective Qdrant configuration scenarios: a sandboxed environment, the
// prepared AppConfig, the tool names the wired engine surfaced, and the
// app-wide construction artefacts (application, delegate engines, and
// the captured construction log) the propagation scenarios assert on.
type recallToolsEffectiveConfigState struct {
	sandboxDir string
	savedEnv   map[string]string
	cfg        *config.AppConfig
	toolNames  map[string]bool
	// qdrantServer hosts the fake reachable Qdrant endpoint the
	// propagation scenario wires QDRANT_URL at.
	qdrantServer *httptest.Server
	// application is the constructed FlowState app; engine is reached
	// through application.Engine.
	application *app.App
	// delegateEngines holds one engine per non-default agent, exactly
	// as the app's delegate construction path builds them.
	delegateEngines map[string]*engine.Engine
	// logBuf captures slog output for the duration of the engine
	// construction so missing-dependency errors are observable.
	logBuf     *bytes.Buffer
	prevLogger *slog.Logger
}

// RegisterRecallToolsEffectiveConfigSteps registers the recall tool
// registration steps with the Godog scenario context.
//
// Expected:
//   - ctx is a valid Godog ScenarioContext.
//
// Side effects:
//   - Registers all recall tool registration steps with the provided
//     context and an After hook restoring the environment.
func RegisterRecallToolsEffectiveConfigSteps(ctx *godog.ScenarioContext) {
	state := &recallToolsEffectiveConfigState{}

	ctx.Step(`^a sandboxed FlowState environment with an agent "([^"]*)" that declares the recall tools$`, state.sandboxedEnvironmentWithRecallAgent)
	ctx.Step(`^no Qdrant URL is set in the FlowState configuration file$`, state.noQdrantURLInConfigFile)
	ctx.Step(`^FlowState wires its engine tools with QDRANT_URL set to "([^"]*)"$`, state.wiresEngineToolsWithEnvURL)
	ctx.Step(`^FlowState wires its engine tools with QDRANT_URL unset$`, state.wiresEngineToolsWithQdrantEnvUnset)
	ctx.Step(`^the "([^"]*)" tool is registered for the agent$`, state.toolIsRegistered)
	ctx.Step(`^the "([^"]*)" tool is not registered for the agent$`, state.toolIsNotRegistered)
	ctx.Step(`^a config with a reachable qdrant url and collection$`, state.configWithReachableQdrantURLAndCollection)
	ctx.Step(`^a config without a qdrant url$`, state.configWithoutAQdrantURL)
	ctx.Step(`^the app constructs engines for its agents$`, state.appConstructsEnginesForItsAgents)
	ctx.Step(`^every constructed engine registers recall tools$`, state.everyConstructedEngineRegistersRecallTools)
	ctx.Step(`^no "recall tools not registered" error is logged$`, state.noRecallToolsNotRegisteredErrorIsLogged)
	ctx.Step(`^no engine registers recall tools$`, state.noEngineRegistersRecallTools)

	ctx.After(state.restoreScenarioEnvironment)
}

// sandboxedEnvironmentWithRecallAgent materialises the sandbox HOME/XDG
// layout, writes the recall-probe agent manifest, and prepares the
// production AppConfig the wiring step constructs the App from.
//
// Expected:
//   - agentID is the manifest id the configuration selects as default.
//
// Returns:
//   - An error when the sandbox or manifest cannot be created, or an
//     environment variable cannot be set.
//
// Side effects:
//   - Creates a scenario sandbox directory tree.
//   - Points HOME and the XDG base dirs at the sandbox (restored by the
//     scenario After hook) and unsets QDRANT_URL.
func (s *recallToolsEffectiveConfigState) sandboxedEnvironmentWithRecallAgent(agentID string) error {
	sandbox, err := os.MkdirTemp("", "flowstate-recall-tools-")
	if err != nil {
		return fmt.Errorf(errRecallToolsSandboxFormat, err)
	}
	s.sandboxDir = sandbox
	agentsDir := filepath.Join(sandbox, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		return fmt.Errorf(errRecallToolsSandboxFormat, err)
	}
	manifestPath := filepath.Join(agentsDir, agentID+".md")
	if err := os.WriteFile(manifestPath, []byte(recallProbeManifest), 0o600); err != nil {
		return fmt.Errorf(errRecallToolsSandboxFormat, err)
	}
	if err := s.isolateScenarioEnvironment(); err != nil {
		return fmt.Errorf(errRecallToolsSandboxFormat, err)
	}
	cfg := config.DefaultConfig()
	cfg.AgentDir = agentsDir
	cfg.DataDir = filepath.Join(sandbox, "data")
	cfg.DefaultAgent = agentID
	cfg.Qdrant.URL = ""
	cfg.MCPServers = nil
	s.cfg = cfg
	s.toolNames = nil
	return nil
}

// isolateScenarioEnvironment redirects HOME and the XDG base dirs into
// the scenario sandbox and clears QDRANT_URL so no operator setting
// leaks into the scenario.
//
// Returns:
//   - An error when an environment variable cannot be set or unset.
//
// Side effects:
//   - Mutates HOME, XDG_CONFIG_HOME, XDG_DATA_HOME, and QDRANT_URL.
//   - Records the prior values for the After-hook restore.
func (s *recallToolsEffectiveConfigState) isolateScenarioEnvironment() error {
	overrides := map[string]string{
		"HOME":            filepath.Join(s.sandboxDir, "home"),
		"XDG_CONFIG_HOME": filepath.Join(s.sandboxDir, "config"),
		"XDG_DATA_HOME":   filepath.Join(s.sandboxDir, "data"),
	}
	s.savedEnv = make(map[string]string, len(overrides)+1)
	for key, value := range overrides {
		s.savedEnv[key] = os.Getenv(key)
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	s.savedEnv[config.QdrantURLEnv] = os.Getenv(config.QdrantURLEnv)
	return os.Unsetenv(config.QdrantURLEnv)
}

// restoreScenarioEnvironment is the Godog After hook returning the
// process environment to its pre-scenario state and dropping the
// sandbox.
//
// Expected:
//   - ctx is the scenario context.
//   - scenario is the finished scenario.
//
// Returns:
//   - The context, unchanged.
//   - Nil error.
//
// Side effects:
//   - Restores saved environment variables and removes the sandbox.
func (s *recallToolsEffectiveConfigState) restoreScenarioEnvironment(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
	if s.prevLogger != nil {
		slog.SetDefault(s.prevLogger)
		s.prevLogger = nil
	}
	if s.qdrantServer != nil {
		s.qdrantServer.Close()
		s.qdrantServer = nil
	}
	s.application = nil
	s.delegateEngines = nil
	s.logBuf = nil
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
	s.cfg = nil
	s.toolNames = nil
	return ctx, nil
}

// noQdrantURLInConfigFile asserts the prepared configuration carries no
// config-file Qdrant URL, so any effective URL must come from the
// environment.
//
// Returns:
//   - An error when no configuration has been prepared or the
//     config-file qdrant.url is populated.
//
// Side effects:
//   - None.
func (s *recallToolsEffectiveConfigState) noQdrantURLInConfigFile() error {
	if s.cfg == nil {
		return errRecallToolsNoConfig
	}
	if s.cfg.Qdrant.URL != "" {
		return fmt.Errorf(errRecallToolsConfigURLFormat, s.cfg.Qdrant.URL)
	}
	return nil
}

// wiresEngineToolsWithEnvURL sets QDRANT_URL to the given value and
// constructs the FlowState application, recording the tool names the
// wired engine surfaced for the default agent manifest.
//
// Expected:
//   - url is the QDRANT_URL value the scenario wires with.
//
// Returns:
//   - An error when the App or its engine cannot be constructed.
//
// Side effects:
//   - Sets the QDRANT_URL environment variable for the duration of the
//     wiring (restored by the scenario After hook).
//   - Constructs a full FlowState application with a no-op MCP client
//     factory so no MCP subprocess is spawned.
func (s *recallToolsEffectiveConfigState) wiresEngineToolsWithEnvURL(url string) error {
	if err := os.Setenv(config.QdrantURLEnv, url); err != nil {
		return err
	}
	return s.wireEngineTools()
}

// wiresEngineToolsWithQdrantEnvUnset clears QDRANT_URL and constructs
// the FlowState application, recording the surfaced tool names.
//
// Returns:
//   - An error when the App or its engine cannot be constructed.
//
// Side effects:
//   - Unsets the QDRANT_URL environment variable.
//   - Constructs a full FlowState application with a no-op MCP client
//     factory so no MCP subprocess is spawned.
func (s *recallToolsEffectiveConfigState) wiresEngineToolsWithQdrantEnvUnset() error {
	if err := os.Unsetenv(config.QdrantURLEnv); err != nil {
		return err
	}
	return s.wireEngineTools()
}

// wireEngineTools constructs the production App from the prepared
// configuration and collects the engine's surfaced tool names.
//
// Returns:
//   - An error when App construction fails or the engine is absent.
//
// Side effects:
//   - Replaces the scenario's recorded tool names.
func (s *recallToolsEffectiveConfigState) wireEngineTools() error {
	if s.cfg == nil {
		return errRecallToolsNoConfig
	}
	application, err := app.NewWithOptions(s.cfg, app.NewOptions{
		SkipBootstrap: true,
		MCPClientFactory: func() mcpclient.Client {
			return &countingMCPClient{}
		},
	})
	if err != nil {
		return fmt.Errorf(errRecallToolsWireFormat, err)
	}
	if application.Engine == nil {
		return errRecallToolsNoEngine
	}
	s.toolNames = make(map[string]bool)
	for _, t := range application.Engine.ToolSchemas() {
		s.toolNames[t.Name] = true
	}
	return nil
}

// toolIsRegistered asserts the named tool was surfaced by the wired
// engine for the agent manifest.
//
// Expected:
//   - name is the tool name the scenario asserts presence of.
//
// Returns:
//   - An error when the tool is absent.
//
// Side effects:
//   - None.
func (s *recallToolsEffectiveConfigState) toolIsRegistered(name string) error {
	if !s.toolNames[name] {
		return fmt.Errorf(errRecallToolsMissingFormat, name, s.registeredToolNames())
	}
	return nil
}

// toolIsNotRegistered asserts the named tool was not surfaced by the
// wired engine for the agent manifest.
//
// Expected:
//   - name is the tool name the scenario asserts absence of.
//
// Returns:
//   - An error when the tool is present.
//
// Side effects:
//   - None.
func (s *recallToolsEffectiveConfigState) toolIsNotRegistered(name string) error {
	if s.toolNames[name] {
		return fmt.Errorf(errRecallToolsPresentFormat, name, s.registeredToolNames())
	}
	return nil
}

// registeredToolNames renders the surfaced tool names for failure
// messages.
//
// Returns:
//   - The recorded tool names, or nil when no wiring ran.
//
// Side effects:
//   - None.
func (s *recallToolsEffectiveConfigState) registeredToolNames() []string {
	if len(s.toolNames) == 0 {
		return nil
	}
	names := make([]string, 0, len(s.toolNames))
	for name := range s.toolNames {
		names = append(names, name)
	}
	return names
}

// writeDelegateProbeManifest drops the recall-delegate-probe manifest
// next to the primary probe so the app has a second registered agent
// whose delegate-engine construction is observable.
//
// Returns:
//   - An error when the manifest cannot be written.
//
// Side effects:
//   - Creates recall-delegate-probe.md under the sandbox agents dir.
func (s *recallToolsEffectiveConfigState) writeDelegateProbeManifest() error {
	manifestPath := filepath.Join(s.cfg.AgentDir, "recall-delegate-probe.md")
	if err := os.WriteFile(manifestPath, []byte(recallDelegateProbeManifest), 0o600); err != nil {
		return fmt.Errorf(errRecallToolsDelegateWriteFmt, err)
	}
	return nil
}

// configWithReachableQdrantURLAndCollection points QDRANT_URL at a
// local fake Qdrant endpoint that accepts connections, names the
// canonical collection, and registers the delegate probe agent.
//
// Returns:
//   - An error when no configuration has been prepared, the manifest
//     cannot be written, or the environment variable cannot be set.
//
// Side effects:
//   - Starts an httptest server retained for the scenario lifetime.
//   - Sets the QDRANT_URL environment variable (restored by the
//     scenario After hook).
//   - Sets the config's qdrant.collection.
func (s *recallToolsEffectiveConfigState) configWithReachableQdrantURLAndCollection() error {
	if s.cfg == nil {
		return errRecallToolsNoConfig
	}
	if err := s.writeDelegateProbeManifest(); err != nil {
		return err
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	s.qdrantServer = server
	if err := os.Setenv(config.QdrantURLEnv, server.URL); err != nil {
		return err
	}
	s.cfg.Qdrant.Collection = "flowstate-recall"
	return nil
}

// configWithoutAQdrantURL asserts the effective Qdrant URL is empty and
// registers the delegate probe agent so both gate scenarios construct
// the same engine set.
//
// Returns:
//   - An error when no configuration has been prepared, the config-file
//     URL is populated, the manifest cannot be written, or the
//     environment variable cannot be unset.
//
// Side effects:
//   - Unsets the QDRANT_URL environment variable.
//   - Creates recall-delegate-probe.md under the sandbox agents dir.
func (s *recallToolsEffectiveConfigState) configWithoutAQdrantURL() error {
	if s.cfg == nil {
		return errRecallToolsNoConfig
	}
	if s.cfg.Qdrant.URL != "" {
		return fmt.Errorf(errRecallToolsConfigURLFormat, s.cfg.Qdrant.URL)
	}
	if err := s.writeDelegateProbeManifest(); err != nil {
		return err
	}
	return os.Unsetenv(config.QdrantURLEnv)
}

// appConstructsEnginesForItsAgents constructs the production FlowState
// app (which builds the primary engine for the default agent) and then
// one delegate engine per other registered agent, mirroring the app's
// delegation construction path. slog output is captured for the
// duration so registration failures are observable.
//
// Returns:
//   - An error when the app or any engine cannot be constructed, or no
//     delegate engine was built.
//
// Side effects:
//   - Replaces the default slog logger for the scenario (restored by
//     the scenario After hook).
//   - Constructs a full FlowState application with a no-op MCP client
//     factory so no MCP subprocess is spawned.
//   - Replaces the scenario's recorded tool names and delegate engines.
func (s *recallToolsEffectiveConfigState) appConstructsEnginesForItsAgents() error {
	if s.cfg == nil {
		return errRecallToolsNoConfig
	}
	s.prevLogger = slog.Default()
	s.logBuf = &bytes.Buffer{}
	slog.SetDefault(slog.New(slog.NewTextHandler(s.logBuf, nil)))
	application, err := app.NewWithOptions(s.cfg, app.NewOptions{
		SkipBootstrap: true,
		MCPClientFactory: func() mcpclient.Client {
			return &countingMCPClient{}
		},
	})
	if err != nil {
		return fmt.Errorf(errRecallToolsWireFormat, err)
	}
	if application.Engine == nil {
		return errRecallToolsNoEngine
	}
	s.application = application
	s.toolNames = make(map[string]bool)
	for _, t := range application.Engine.ToolSchemas() {
		s.toolNames[t.Name] = true
	}
	s.delegateEngines = application.BuildDelegateEnginesForTest(s.cfg.DefaultAgent)
	if len(s.delegateEngines) == 0 {
		return errRecallToolsNoDelegates
	}
	return nil
}

// everyConstructedEngineRegistersRecallTools asserts the primary engine
// advertises the Qdrant-backed recall tools and that every constructed
// engine registered the recall factory tools through the engine
// configuration gate.
//
// Returns:
//   - An error when any engine is missing a recall tool registration.
//
// Side effects:
//   - None.
func (s *recallToolsEffectiveConfigState) everyConstructedEngineRegistersRecallTools() error {
	if s.application == nil {
		return errRecallToolsNoConstruction
	}
	for _, name := range []string{"mcp_vault-rag_query_vault", "mcp_memory_search_nodes"} {
		if !s.toolNames[name] {
			return fmt.Errorf(errRecallToolsMissingFormat, name, s.registeredToolNames())
		}
	}
	if !s.application.Engine.HasTool("search_context") {
		return errors.New(errRecallToolsPrimaryFactFmt)
	}
	for id, eng := range s.delegateEngines {
		if !eng.HasTool("search_context") {
			return fmt.Errorf(errRecallToolsDelegateFormat, id)
		}
		if err := delegateAdvertisesTool(eng, id, "mcp_memory_search_nodes"); err != nil {
			return err
		}
	}
	return nil
}

// noRecallToolsNotRegisteredErrorIsLogged asserts the captured
// construction log carries neither of the two recall registration
// failure messages the engine emits when dependencies are missing.
//
// Returns:
//   - An error when a registration failure was logged.
//
// Side effects:
//   - None.
func (s *recallToolsEffectiveConfigState) noRecallToolsNotRegisteredErrorIsLogged() error {
	if s.logBuf == nil {
		return errRecallToolsNoConstruction
	}
	out := s.logBuf.String()
	if strings.Contains(out, "recall tools not registered") || strings.Contains(out, "no recall tools registered") {
		return fmt.Errorf(errRecallToolsLoggedFormat, out)
	}
	return nil
}

// noEngineRegistersRecallTools asserts neither the primary engine nor
// any delegate engine advertises the Qdrant-backed recall tools while
// the effective-config gate is closed.
//
// Returns:
//   - An error when any engine advertises a recall tool.
//
// Side effects:
//   - None.
func (s *recallToolsEffectiveConfigState) noEngineRegistersRecallTools() error {
	if s.application == nil {
		return errRecallToolsNoConstruction
	}
	for _, name := range []string{"mcp_vault-rag_query_vault", "mcp_memory_open_nodes"} {
		if s.toolNames[name] {
			return fmt.Errorf(errRecallToolsPresentFormat, name, s.registeredToolNames())
		}
	}
	for id, eng := range s.delegateEngines {
		for _, t := range eng.ToolSchemas() {
			if t.Name == "mcp_vault-rag_query_vault" || t.Name == "mcp_memory_open_nodes" {
				return fmt.Errorf(errRecallToolsDelegateLeakFmt, id, t.Name, advertisedToolNames(eng))
			}
		}
	}
	return nil
}

// delegateAdvertisesTool asserts a delegate engine advertises the named
// tool through its manifest-filtered schema surface.
//
// Expected:
//   - eng is a constructed delegate engine.
//   - id identifies the engine in failure messages.
//   - name is the tool the assertion targets.
//
// Returns:
//   - An error when the tool is absent from the advertised schemas.
//
// Side effects:
//   - None.
func delegateAdvertisesTool(eng *engine.Engine, id, name string) error {
	for _, t := range eng.ToolSchemas() {
		if t.Name == name {
			return nil
		}
	}
	return fmt.Errorf(errRecallToolsDelegateMemFmt, id, name, advertisedToolNames(eng))
}

// advertisedToolNames renders the schema names an engine advertises for
// failure messages.
//
// Expected:
//   - eng is a constructed engine.
//
// Returns:
//   - The advertised tool names, or nil when none.
//
// Side effects:
//   - None.
func advertisedToolNames(eng *engine.Engine) []string {
	schemas := eng.ToolSchemas()
	if len(schemas) == 0 {
		return nil
	}
	names := make([]string, 0, len(schemas))
	for _, t := range schemas {
		names = append(names, t.Name)
	}
	return names
}
