//go:build e2e

package support

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/config"
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

// recallToolsEffectiveConfigState holds per-scenario state for the
// effective Qdrant configuration scenarios: a sandboxed environment, the
// prepared AppConfig, and the tool names the wired engine surfaced.
type recallToolsEffectiveConfigState struct {
	sandboxDir string
	savedEnv   map[string]string
	cfg        *config.AppConfig
	toolNames  map[string]bool
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
