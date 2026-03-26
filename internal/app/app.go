// Package app provides the main application container and initialization.
package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/config"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/discovery"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/learning"
	mcpclient "github.com/baphled/flowstate/internal/mcp"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/anthropic"
	"github.com/baphled/flowstate/internal/provider/copilot"
	"github.com/baphled/flowstate/internal/provider/ollama"
	"github.com/baphled/flowstate/internal/provider/openai"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/bash"
	coordinationtool "github.com/baphled/flowstate/internal/tool/coordination"
	"github.com/baphled/flowstate/internal/tool/mcpproxy"
	"github.com/baphled/flowstate/internal/tool/read"
	skilltool "github.com/baphled/flowstate/internal/tool/skill"
	"github.com/baphled/flowstate/internal/tool/web"
	"github.com/baphled/flowstate/internal/tool/write"
	"github.com/baphled/flowstate/internal/tracer"

	"github.com/baphled/flowstate/internal/plan"
	"github.com/baphled/flowstate/internal/session"
)

// App is the main application container holding all initialized components.
type App struct {
	Config           *config.AppConfig
	Registry         *agent.Registry
	Skills           []skill.Skill
	Engine           *engine.Engine
	Discovery        *discovery.AgentDiscovery
	Sessions         *ctxstore.FileSessionStore
	Learning         *learning.JSONFileStore
	API              *api.Server
	Streamer         streaming.Streamer
	mcpClient        mcpclient.Client
	providerRegistry *provider.Registry
	ollamaProvider   *ollama.Provider
	metricsRegistry  *prometheus.Registry
	PlanStore        *plan.PlanStore
}

// New creates a new App instance with all components initialised.
//
// Expected:
//   - cfg is a non-nil AppConfig with provider and directory settings.
//
// Returns:
//   - A fully initialised App with all components wired together.
//   - An error if any component fails to initialise.
//
// Side effects:
//   - Reads agent manifests from the configured agent directory.
//   - Reads skill files from the configured skill directory.
//   - Creates session and context store directories if they do not exist.
//   - Connects to configured MCP servers.
func New(cfg *config.AppConfig) (*App, error) {
	if err := SeedAgentsDir(EmbeddedAgentsFS(), cfg.AgentDir); err != nil {
		log.Printf("warning: seeding agents to %q: %v", cfg.AgentDir, err)
	} else {
		log.Printf("info: agents seeded to %q", cfg.AgentDir)
	}

	providerRegistry, ollamaProvider := setupProviders(cfg)
	agentRegistry := setupAgentRegistry(cfg)
	defaultManifest := selectDefaultManifest(agentRegistry, cfg.DefaultAgent)
	skills, alwaysActiveSkills := loadSkills(cfg, defaultManifest)
	sessionStore, learningStore, err := createDataStores(cfg)
	if err != nil {
		return nil, err
	}
	runtime, err := setupEngine(setupEngineParams{
		cfg:                cfg,
		providerRegistry:   providerRegistry,
		ollamaProvider:     ollamaProvider,
		agentRegistry:      agentRegistry,
		defaultManifest:    defaultManifest,
		alwaysActiveSkills: alwaysActiveSkills,
		skills:             skills,
		sessionStore:       sessionStore,
		learningStore:      learningStore,
	})
	if err != nil {
		return nil, err
	}

	app := &App{
		Config:           cfg,
		Registry:         agentRegistry,
		Skills:           skills,
		Engine:           runtime.engine,
		Discovery:        runtime.discovery,
		Sessions:         sessionStore,
		Learning:         learningStore,
		API:              runtime.apiServer,
		Streamer:         runtime.streamer,
		mcpClient:        runtime.mcpManager,
		providerRegistry: providerRegistry,
		ollamaProvider:   ollamaProvider,
		metricsRegistry:  runtime.metricsRegistry,
	}

	planDir := filepath.Join(cfg.DataDir, "plans")
	planStore, err := plan.NewPlanStore(planDir)
	if err != nil {
		log.Printf("warning: creating plan store: %v", err)
	} else {
		app.PlanStore = planStore
	}

	app.wireDelegateToolIfEnabled(runtime.engine, defaultManifest)
	return app, nil
}

// engineParams holds parameters for engine creation.
type engineParams struct {
	defaultProvider    provider.Provider
	ollamaProvider     *ollama.Provider
	providerRegistry   *provider.Registry
	agentRegistry      *agent.Registry
	defaultManifest    agent.Manifest
	alwaysActiveSkills []skill.Skill
	contextStore       *recall.FileContextStore
	learningStore      *learning.JSONFileStore
	appTools           []tool.Tool
	toolRegistry       *tool.Registry
	permissionHandler  tool.PermissionHandler
	agentsFileLoader   *agent.AgentsFileLoader
	tokenCounter       ctxstore.TokenCounter
}

// setupEngine initialises the engine and the immediate runtime dependencies.
//
// Expected:
//   - params contains all required configuration and dependency references.
//
// Returns:
//   - A runtime bundle containing the engine, discovery, streamer, API server, and metrics registry.
//   - An error if any component fails to initialise.
//
// Side effects:
//   - Creates the MCP manager, tool registry, engine, discovery, streamer, and API server.
func setupEngine(params setupEngineParams) (*runtimeComponents, error) {
	metricsReg, tracedProvider, err := buildTracedProvider(params.providerRegistry, params.cfg.Providers.Default)
	if err != nil {
		return nil, err
	}
	contextStore, err := createContextStore(params.cfg)
	if err != nil {
		return nil, err
	}
	mcpMgr := mcpclient.NewManager()
	appTools := buildTools(skill.NewFileSkillLoader(params.cfg.SkillDir))
	allServers := mergeMCPServers(params.cfg.MCPServers, config.DiscoverMCPServers())
	appTools = append(appTools, ConnectMCPServers(context.Background(), mcpMgr, allServers)...)
	toolRegistry, permHandler := buildToolsSetup(appTools)
	eng := createEngine(engineParams{
		defaultProvider:    tracedProvider,
		ollamaProvider:     params.ollamaProvider,
		providerRegistry:   params.providerRegistry,
		agentRegistry:      params.agentRegistry,
		defaultManifest:    params.defaultManifest,
		alwaysActiveSkills: params.alwaysActiveSkills,
		contextStore:       contextStore,
		learningStore:      params.learningStore,
		appTools:           appTools,
		toolRegistry:       toolRegistry,
		permissionHandler:  permHandler,
		agentsFileLoader:   buildAgentsFileLoader(),
		tokenCounter:       ctxstore.NewTiktokenCounter(),
	})
	disc := createDiscovery(params.agentRegistry)
	streamer := createHarnessStreamer(eng, params.agentRegistry, params.cfg.Harness, tracedProvider)
	sessionMgr := session.NewManager(streamer)
	apiServer := api.NewServer(
		streamer,
		params.agentRegistry,
		disc,
		params.skills,
		api.WithSessions(params.sessionStore),
		api.WithSessionManager(sessionMgr),
	)
	return &runtimeComponents{
		engine:          eng,
		discovery:       disc,
		streamer:        streamer,
		apiServer:       apiServer,
		metricsRegistry: metricsReg,
		mcpManager:      mcpMgr,
	}, nil
}

// setupEngineParams groups the inputs required to initialise the application runtime.
type setupEngineParams struct {
	cfg                *config.AppConfig
	providerRegistry   *provider.Registry
	ollamaProvider     *ollama.Provider
	agentRegistry      *agent.Registry
	defaultManifest    agent.Manifest
	alwaysActiveSkills []skill.Skill
	skills             []skill.Skill
	sessionStore       *ctxstore.FileSessionStore
	learningStore      *learning.JSONFileStore
}

// runtimeComponents groups the runtime values created during engine setup.
type runtimeComponents struct {
	engine          *engine.Engine
	discovery       *discovery.AgentDiscovery
	streamer        streaming.Streamer
	apiServer       *api.Server
	metricsRegistry *prometheus.Registry
	mcpManager      mcpclient.Client
}

// createEngine initialises the engine with live manifest getter for hook chain.
//
// Expected:
//   - params contains all required fields populated.
//
// Returns:
//   - A fully initialised Engine with hook chain wired to live manifest.
//
// Side effects:
//   - Creates engine and connects MCP servers.
func createEngine(params engineParams) *engine.Engine {
	var eng *engine.Engine
	hookChain := buildHookChain(params.learningStore, func() agent.Manifest {
		if eng != nil {
			return eng.Manifest()
		}
		return params.defaultManifest
	})
	eng = engine.New(engine.Config{
		ChatProvider:      params.defaultProvider,
		EmbeddingProvider: toEmbeddingProvider(params.ollamaProvider),
		Registry:          params.providerRegistry,
		AgentRegistry:     params.agentRegistry,
		Manifest:          params.defaultManifest,
		Skills:            params.alwaysActiveSkills,
		Store:             params.contextStore,
		HookChain:         hookChain,
		Tools:             params.appTools,
		ToolRegistry:      params.toolRegistry,
		PermissionHandler: params.permissionHandler,
		AgentsFileLoader:  params.agentsFileLoader,
		TokenCounter:      params.tokenCounter,
	})
	return eng
}

// wireDelegateToolIfEnabled adds a DelegateTool to the engine when the
// manifest permits delegation. Each target agent gets its own isolated engine
// instance to prevent state corruption during delegation.
//
// Expected:
//   - eng is a fully initialised Engine.
//   - manifest is the agent manifest to inspect for delegation configuration.
//
// Side effects:
//   - Appends a DelegateTool to the engine's tool set when can_delegate is true.
//   - Creates isolated engine instances for each delegation target.
func (a *App) wireDelegateToolIfEnabled(eng *engine.Engine, manifest agent.Manifest) {
	if !manifest.Delegation.CanDelegate {
		return
	}
	bgManager := engine.NewBackgroundTaskManager()
	coordinationStore := coordination.NewMemoryStore()

	engines := make(map[string]*engine.Engine, len(manifest.Delegation.DelegationTable))
	for _, targetID := range manifest.Delegation.DelegationTable {
		targetManifest, ok := a.Registry.Get(targetID)
		if !ok {
			continue
		}
		targetEngine := a.createDelegateEngine(*targetManifest, coordinationStore)
		engines[targetID] = targetEngine
	}
	eng.AddTool(engine.NewDelegateToolWithBackground(
		engines, manifest.Delegation, manifest.ID, bgManager, coordinationStore,
	))

	if a.hasCoordinationTool(manifest.Capabilities.Tools) {
		eng.AddTool(coordinationtool.New(coordinationStore))
	}
}

// createDelegateEngine creates an isolated engine instance for a delegation target.
// This ensures that when a coordinator delegates to another agent, the target's
// manifest and state do not corrupt the coordinator's state.
//
// Expected:
//   - manifest is the target agent's manifest with model preferences and capabilities.
//   - store is the shared coordination store for cross-agent communication.
//
// Returns:
//   - An Engine instance configured for the target agent.
//   - The engine does NOT have a delegate tool (prevents recursive delegation).
//
// Side effects:
//   - Creates a new engine with the target's manifest, providers, and tools.
func (a *App) createDelegateEngine(manifest agent.Manifest, store coordination.Store) *engine.Engine {
	hookChain := buildHookChain(a.Learning, func() agent.Manifest { return manifest })
	eng := engine.New(engine.Config{
		Registry:      a.providerRegistry,
		AgentRegistry: a.Registry,
		Manifest:      manifest,
		Tools:         a.buildToolsForManifestWithStore(manifest, store),
		HookChain:     hookChain,
	})
	return eng
}

// buildToolsForManifestWithStore returns the tools available to an agent based on its
// manifest capabilities, including the CoordinationTool when required.
//
// Expected:
//   - manifest is the agent's manifest with capabilities.
//   - store is the coordination store to use for the CoordinationTool.
//
// Returns:
//   - A slice of tools available to the agent.
//
// Side effects:
//   - None.
func (a *App) buildToolsForManifestWithStore(manifest agent.Manifest, store coordination.Store) []tool.Tool {
	tools := []tool.Tool{
		bash.New(),
		read.New(),
		write.New(),
		web.New(),
	}

	if a.hasCoordinationTool(manifest.Capabilities.Tools) {
		tools = append(tools, coordinationtool.New(store))
	}

	return tools
}

// hasCoordinationTool checks if the manifest has coordination_store in its tools list.
//
// Expected:
//   - tools is the list of tool names from the manifest.
//
// Returns:
//   - true if "coordination_store" is in the list, false otherwise.
//
// Side effects:
//   - None.
func (a *App) hasCoordinationTool(tools []string) bool {
	for _, t := range tools {
		if t == "coordination_store" {
			return true
		}
	}
	return false
}

// PersistApprovedPlan retrieves an approved plan from the coordination store and
// saves it to the PlanStore. This is called after the reviewer approves a plan.
//
// Expected:
//   - chainID is the delegation chain identifier.
//   - coordinationStore contains the plan at "{chainID}/plan" and review at "{chainID}/review".
//
// Returns:
//   - error if the plan cannot be retrieved or saved.
//
// Side effects:
//   - Writes plan file to the PlanStore directory.
func (a *App) PersistApprovedPlan(chainID string, coordinationStore coordination.Store) error {
	if a.PlanStore == nil {
		return errors.New("plan store not configured")
	}

	reviewData, err := coordinationStore.Get(chainID + "/review")
	if err != nil {
		return fmt.Errorf("getting review: %w", err)
	}

	reviewStr := string(reviewData)
	if !strings.Contains(reviewStr, "APPROVE") {
		return errors.New("plan not approved")
	}

	planData, err := coordinationStore.Get(chainID + "/plan")
	if err != nil {
		return fmt.Errorf("getting plan: %w", err)
	}

	planFile := plan.File{
		ID:        chainID,
		Title:     "Plan " + chainID,
		Status:    "approved",
		CreatedAt: time.Now(),
		TLDR:      string(planData),
	}

	if err := a.PlanStore.Create(planFile); err != nil {
		return fmt.Errorf("persisting plan: %w", err)
	}

	return nil
}

// buildAgentsFileLoader loads AGENTS.md from the global configuration directory and the current working directory.
//
// Returns:
//   - A configured AgentsFileLoader instance.
//
// Side effects:
//   - Calls os.Getwd to determine the current working directory.
func buildAgentsFileLoader() *agent.AgentsFileLoader {
	workingDir, err := os.Getwd()
	if err != nil {
		workingDir = ""
	}
	return agent.NewAgentsFileLoader(config.Dir(), workingDir)
}

// AgentsDir returns the directory where agent manifests are stored.
//
// Returns:
//   - The configured agent directory path.
//
// Side effects:
//   - None.
func (a *App) AgentsDir() string {
	return a.Config.AgentDir
}

// SkillsDir returns the directory where skills are stored.
//
// Returns:
//   - The configured skill directory path.
//
// Side effects:
//   - None.
func (a *App) SkillsDir() string {
	return a.Config.SkillDir
}

// SessionsDir returns the directory where sessions are stored.
//
// Returns:
//   - The sessions subdirectory path under the data directory.
//
// Side effects:
//   - None.
func (a *App) SessionsDir() string {
	return filepath.Join(a.Config.DataDir, "sessions")
}

// ConfigPath returns the path to the configuration file.
//
// Returns:
//   - The resolved path to the config.yaml file.
//
// Side effects:
//   - None.
func (a *App) ConfigPath() string {
	return filepath.Join(config.Dir(), "config.yaml")
}

// ListModels returns all available models from registered providers.
//
// Returns:
//   - A slice of available models from all providers.
//   - An error if fetching models from any provider fails.
//
// Side effects:
//   - None.
func (a *App) ListModels() ([]provider.Model, error) {
	if a.providerRegistry == nil {
		return []provider.Model{}, nil
	}

	var allModels []provider.Model
	providerNames := a.providerRegistry.List()

	for _, name := range providerNames {
		p, err := a.providerRegistry.Get(name)
		if err != nil {
			return nil, fmt.Errorf("getting provider %q: %w", name, err)
		}

		models, err := p.Models()
		if err != nil {
			return nil, fmt.Errorf("listing models from provider %q: %w", name, err)
		}

		allModels = append(allModels, models...)
	}

	return allModels, nil
}

// SetProviderRegistry sets the provider registry for testing purposes.
//
// Expected:
//   - registry is a valid provider.Registry.
//
// Returns:
//   - None.
//
// Side effects:
//   - Updates the app's provider registry reference.
func (a *App) SetProviderRegistry(registry *provider.Registry) {
	a.providerRegistry = registry
}

// ProviderRegistry returns the provider registry for the TUI layer.
//
// Returns:
//   - The provider registry.
//
// Side effects:
//   - None.
func (a *App) ProviderRegistry() *provider.Registry {
	return a.providerRegistry
}

// MetricsHandler returns an HTTP handler serving Prometheus metrics from the
// application's metrics registry. Register this on the API server's router at
// the "/metrics" path to expose collected observability data.
//
// Returns:
//   - An http.Handler that serves Prometheus metrics in exposition format.
//
// Side effects:
//   - None.
func (a *App) MetricsHandler() http.Handler {
	return promhttp.HandlerFor(a.metricsRegistry, promhttp.HandlerOpts{})
}

// SetModel overrides the engine's model preference to use the specified model.
//
// Expected:
//   - modelID is a non-empty string identifying a model available in the registry.
//   - The engine must be configured with a failback chain.
//
// Returns:
//   - nil on success, or an error if the model is not found in any provider.
//
// Side effects:
//   - Reconfigures the engine's failback chain to prioritise the requested model.
func (a *App) SetModel(modelID string) error {
	if a.Engine == nil {
		return errors.New("engine not configured")
	}

	if a.providerRegistry == nil {
		return errors.New("provider registry not available")
	}

	models, err := a.ListModels()
	if err != nil {
		return fmt.Errorf("listing available models: %w", err)
	}

	var selectedModel *provider.Model
	for i := range models {
		if models[i].ID == modelID {
			selectedModel = &models[i]
			break
		}
	}

	if selectedModel == nil {
		return fmt.Errorf("model %q not found in any provider", modelID)
	}

	a.Engine.SetModelPreference(selectedModel.Provider, selectedModel.ID)
	return nil
}

// DisconnectAll closes all connected MCP server connections.
//
// Returns:
//   - An error if disconnection fails, nil otherwise.
//   - nil if no MCP client is configured.
//
// Side effects:
//   - Closes all MCP sessions managed by the client.
func (a *App) DisconnectAll() error {
	if a.mcpClient == nil {
		return nil
	}
	return a.mcpClient.DisconnectAll()
}

// buildTools constructs and returns the default set of available tools.
//
// Expected:
//   - skillLoader is a non-nil skill.FileSkillLoader.
//
// Returns:
//   - A slice containing bash, file, web, and skill_load tools.
//
// Side effects:
//   - Initialises new tool instances.
func buildTools(skillLoader *skill.FileSkillLoader) []tool.Tool {
	return []tool.Tool{
		bash.New(),
		read.New(),
		write.New(),
		web.New(),
		skilltool.New(skillLoader),
	}
}

// buildToolsSetup creates a tool registry and permission handler for the engine.
// MCP proxy tools default to Ask permission, requiring user approval before execution.
// Built-in tools (bash, file, web, skill) default to Allow.
//
// Expected:
//   - tools is a slice of tool.Tool values to register.
//
// Returns:
//   - A tool.Registry with all tools registered and MCP tools set to Ask permission.
//   - A tool.PermissionHandler that allows all tool invocations.
//
// Side effects:
//   - None.
func buildToolsSetup(tools []tool.Tool) (*tool.Registry, tool.PermissionHandler) {
	registry := tool.NewRegistry()
	for _, t := range tools {
		registry.Register(t)
		if _, ok := t.(*mcpproxy.Proxy); ok {
			registry.SetPermission(t.Name(), tool.Ask)
		}
	}
	handler := func(_ tool.PermissionRequest) (bool, error) {
		return true, nil
	}
	return registry, handler
}

// ConnectMCPServers connects to configured MCP servers and returns proxy tools.
// Connection failures are logged as warnings and do not stop processing.
//
// Expected:
//   - ctx is a valid context.
//   - client is a connected MCP Client.
//   - servers is a slice of MCP server configurations.
//
// Returns:
//   - A slice of tool.Tool implementations backed by connected MCP servers.
//
// Side effects:
//   - Connects to MCP servers via the client.
//   - Logs warnings for connection or tool listing failures.
func ConnectMCPServers(ctx context.Context, client mcpclient.Client, servers []config.MCPServerConfig) []tool.Tool {
	var tools []tool.Tool
	for _, serverCfg := range servers {
		if !serverCfg.Enabled {
			continue
		}
		mcpServerConfig := mcpclient.ServerConfig{
			Name:    serverCfg.Name,
			Command: serverCfg.Command,
			Args:    serverCfg.Args,
			Env:     serverCfg.Env,
		}
		if err := client.Connect(ctx, mcpServerConfig); err != nil {
			log.Printf("warning: MCP server %q failed to connect: %v", serverCfg.Name, err)
			continue
		}
		serverTools, err := client.ListTools(ctx, serverCfg.Name)
		if err != nil {
			log.Printf("warning: MCP server %q ListTools failed: %v", serverCfg.Name, err)
			continue
		}
		for _, t := range serverTools {
			tools = append(tools, mcpproxy.NewProxy(client, serverCfg.Name, t))
		}
	}
	return tools
}

// mergeMCPServers merges discovered MCP servers with configured servers,
// preferring configured servers when names conflict.
//
// Expected:
//   - configured is the user-defined server list from config.
//   - discovered is the auto-detected server list.
//
// Returns:
//   - A merged slice with configured servers taking precedence.
//
// Side effects:
//   - None.
func mergeMCPServers(configured, discovered []config.MCPServerConfig) []config.MCPServerConfig {
	existing := make(map[string]bool)
	result := make([]config.MCPServerConfig, 0, len(configured)+len(discovered))
	for _, s := range configured {
		result = append(result, s)
		existing[s.Name] = true
	}
	for _, s := range discovered {
		if !existing[s.Name] {
			result = append(result, s)
		}
	}
	return result
}

// loadSkills loads all available skills and always-active skills from the configured skill directory and agent manifest.
//
// Expected:
//   - cfg is a non-nil AppConfig with a valid SkillDir path.
//   - manifest is the selected agent Manifest with Capabilities.
//
// Returns:
//   - A slice of all loaded skills (empty slice if loading fails).
//   - A slice of always-active skills loaded from the engine, merged from config and manifest.
//
// Side effects:
//   - Reads skill files from the configured skill directory.
//   - Logs a warning if skill loading fails.
func loadSkills(cfg *config.AppConfig, manifest agent.Manifest) ([]skill.Skill, []skill.Skill) {
	skillLoader := skill.NewFileSkillLoader(cfg.SkillDir)
	skills, err := skillLoader.LoadAll()
	if err != nil {
		log.Printf("warning: loading skills: %v", err)
		skills = []skill.Skill{}
	}
	alwaysActiveSkills := engine.LoadAlwaysActiveSkills(cfg.SkillDir, cfg.AlwaysActiveSkills, manifest.Capabilities.AlwaysActiveSkills)
	return skills, alwaysActiveSkills
}

// buildHookChain constructs a hook chain with logging, learning, and skill auto-loading hooks.
//
// Expected:
//   - learningStore is a non-nil JSONFileStore for persisting learning data.
//   - manifestGetter returns the current agent manifest for skill selection.
//
// Returns:
//   - A fully configured hook.Chain ready for use in the engine.
//
// Side effects:
//   - Reads skill-autoloader.yaml from the config directory if it exists.
func buildHookChain(
	learningStore *learning.JSONFileStore,
	manifestGetter func() agent.Manifest,
) *hook.Chain {
	cfg, err := hook.LoadSkillAutoLoaderConfig(filepath.Join(config.Dir(), "skill-autoloader.yaml"))
	if err != nil {
		cfg = hook.DefaultSkillAutoLoaderConfig()
	}
	hooks := []hook.Hook{
		hook.LoggingHook(),
		hook.LearningHook(learningStore),
		hook.SkillAutoLoaderHook(cfg, manifestGetter),
	}
	if manifestGetter().HarnessEnabled {
		projectRoot, err := os.Getwd()
		if err != nil {
			projectRoot = "."
		}
		hooks = append(hooks, hook.PhaseDetectorHook(), hook.ContextInjectionHook(manifestGetter, projectRoot))
	}
	hooks = append(hooks, tracer.Hook())
	return hook.NewChain(hooks...)
}

// buildTracedProvider creates a Prometheus metrics registry, wraps the default
// provider with a TracingProvider that records per-method latency, and returns
// both for use in the application container.
//
// Expected:
//   - providerRegistry is a non-nil provider.Registry with registered providers.
//   - defaultName is the name of the default provider to retrieve.
//
// Returns:
//   - A prometheus.Registry for gathering metrics.
//   - A TracingProvider wrapping the default provider with latency recording.
//   - An error if the default provider cannot be found.
//
// Side effects:
//   - Registers Prometheus collectors with the metrics registry.
func buildTracedProvider(
	providerRegistry *provider.Registry,
	defaultName string,
) (*prometheus.Registry, *tracer.TracingProvider, error) {
	metricsReg := prometheus.NewRegistry()
	recorder := tracer.NewPrometheusRecorder(metricsReg)
	defaultProvider, err := providerRegistry.Get(defaultName)
	if err != nil {
		return nil, nil, fmt.Errorf("getting default provider %q: %w", defaultName, err)
	}
	return metricsReg, tracer.NewTracingProvider(defaultProvider, recorder), nil
}

// setupProviders initialises and registers all configured LLM providers.
//
// Expected:
//   - cfg is a non-nil AppConfig with provider configuration.
//
// Returns:
//   - A provider.Registry containing all successfully initialised providers.
//   - The Ollama provider instance (may be nil if initialisation failed).
//
// Side effects:
//   - Reads OPENAI_API_KEY and ANTHROPIC_API_KEY environment variables.
//   - Registers providers with the registry if initialisation succeeds.
func setupProviders(cfg *config.AppConfig) (*provider.Registry, *ollama.Provider) {
	providerRegistry := provider.NewRegistry()

	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = ""
	}
	opencodePath := filepath.Join(homeDir, ".local", "share", "opencode", "auth.json")

	ollamaProvider, err := ollama.New(cfg.Providers.Ollama.Host)
	if err == nil {
		providerRegistry.Register(ollamaProvider)
	}

	openaiKey := os.Getenv("OPENAI_API_KEY")
	if openaiKey == "" {
		openaiKey = cfg.Providers.OpenAI.APIKey
	}
	if openaiKey != "" {
		openaiProvider, openaiErr := openai.New(openaiKey)
		if openaiErr == nil {
			providerRegistry.Register(openaiProvider)
		}
	}

	// Anthropic: Try OpenCode first, then env, then config
	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")
	if anthropicKey == "" {
		anthropicKey = cfg.Providers.Anthropic.APIKey
	}
	anthropicProvider, anthropicErr := anthropic.NewFromOpenCodeOrConfig(opencodePath, anthropicKey)
	if anthropicErr == nil {
		providerRegistry.Register(anthropicProvider)
	}

	// GitHub Copilot: Try OpenCode first, then env, then config
	githubToken := os.Getenv("GITHUB_TOKEN")
	if githubToken == "" {
		githubToken = cfg.Providers.GitHub.APIKey
	}
	copilotProvider, copilotErr := copilot.NewFromOpenCodeOrFallback(opencodePath, nil, githubToken)
	if copilotErr == nil {
		providerRegistry.Register(copilotProvider)
	}
	return providerRegistry, ollamaProvider
}

// RegisterProvidersForTest is a test helper that exposes registerProviders for testing.
//
// Expected:
//   - cfg is a non-nil AppConfig with provider configuration.
//
// Returns:
//   - A provider.Registry containing all successfully initialised providers.
//   - The Ollama provider instance (may be nil if initialisation failed).
//
// Side effects:
//   - Initialises provider instances and registers them in the registry.
func RegisterProvidersForTest(cfg *config.AppConfig) (*provider.Registry, *ollama.Provider) {
	return setupProviders(cfg)
}

// BuildHookChainForTest is a test helper that exposes buildHookChain for testing.
//
// Expected:
//   - learningStore is a non-nil JSONFileStore for persisting learning data.
//   - manifestGetter returns the current agent manifest for hook selection.
//
// Returns:
//   - A fully configured hook.Chain for inspection in tests.
//
// Side effects:
//   - None.
func BuildHookChainForTest(
	learningStore *learning.JSONFileStore,
	manifestGetter func() agent.Manifest,
) *hook.Chain {
	return buildHookChain(learningStore, manifestGetter)
}

// selectDefaultManifest selects the default agent manifest from the registry.
//
// Expected:
//   - registry is a non-nil agent.Registry.
//   - defaultAgentID may be empty or contain a valid agent ID.
//
// Returns:
//   - The manifest for the specified defaultAgentID if found.
//   - Otherwise, the first manifest in the registry if available.
//   - Otherwise, a fallback manifest with ID "default".
//
// Side effects:
//   - None.
func selectDefaultManifest(registry *agent.Registry, defaultAgentID string) agent.Manifest {
	if defaultAgentID != "" {
		if m, found := registry.Get(defaultAgentID); found {
			return *m
		}
	}
	manifests := registry.List()
	if len(manifests) > 0 {
		return *manifests[0]
	}
	return agent.Manifest{ID: "default", Name: "Default Agent"}
}

// setupAgentRegistry creates and populates an agent registry by discovering agents in the configured directory.
//
// Expected:
//   - cfg is a non-nil AppConfig with a valid AgentDir path.
//
// Returns:
//   - A populated agent.Registry containing discovered agents.
//
// Side effects:
//   - Reads agent manifest files from the configured agent directory.
//   - Logs a warning if agent discovery fails.
func setupAgentRegistry(cfg *config.AppConfig) *agent.Registry {
	agentRegistry := agent.NewRegistry()
	if err := agentRegistry.Discover(cfg.AgentDir); err != nil {
		log.Printf("warning: discovering agents in %q: %v", cfg.AgentDir, err)
	} else {
		manifests := agentRegistry.List()
		if len(manifests) == 0 {
			log.Printf("warning: no agents discovered in %q", cfg.AgentDir)
		} else {
			log.Printf("info: discovered %d agent(s) in %q", len(manifests), cfg.AgentDir)
		}
	}
	return agentRegistry
}

// createDataStores initialises the session and learning data stores.
//
// Expected:
//   - cfg is a non-nil AppConfig with a valid DataDir path.
//
// Returns:
//   - A FileSessionStore for persisting session data.
//   - A JSONFileStore for persisting learning data.
//   - An error if session store creation fails.
//
// Side effects:
//   - Creates the sessions subdirectory if it does not exist.
//   - Creates the learnings.json file path (file creation deferred to store).
func createDataStores(cfg *config.AppConfig) (*ctxstore.FileSessionStore, *learning.JSONFileStore, error) {
	sessionStore, err := ctxstore.NewFileSessionStore(filepath.Join(cfg.DataDir, "sessions"))
	if err != nil {
		return nil, nil, fmt.Errorf("creating session store: %w", err)
	}
	learningStore := learning.NewJSONFileStore(filepath.Join(cfg.DataDir, "learnings.json"))
	return sessionStore, learningStore, nil
}

// createContextStore initialises the context store for managing conversation context.
//
// Expected:
//   - cfg is a non-nil AppConfig with a valid DataDir and Ollama model configuration.
//
// Returns:
//   - A FileContextStore for persisting context data.
//   - An error if context store creation fails.
//
// Side effects:
//   - None; creates an in-memory context store with no file I/O.
//
//nolint:unparam // error return kept for interface compatibility
func createContextStore(cfg *config.AppConfig) (*recall.FileContextStore, error) {
	return recall.NewEmptyContextStore(cfg.Providers.Ollama.Model), nil
}

// toEmbeddingProvider converts an Ollama provider to a generic provider interface for embedding operations.
//
// Expected:
//   - ollamaProvider may be nil or a valid Ollama provider instance.
//
// Returns:
//   - The Ollama provider cast to provider.Provider if non-nil.
//   - nil if the Ollama provider is nil.
//
// Side effects:
//   - None.
func toEmbeddingProvider(ollamaProvider *ollama.Provider) provider.Provider {
	if ollamaProvider != nil {
		return ollamaProvider
	}
	return nil
}

// createDiscovery initialises agent discovery with manifests from the provided registry.
//
// Expected:
//   - agentRegistry is a non-nil agent.Registry containing agent manifests.
//
// Returns:
//   - An AgentDiscovery instance populated with all manifests from the registry.
//
// Side effects:
//   - None.
func createDiscovery(agentRegistry *agent.Registry) *discovery.AgentDiscovery {
	manifests := agentRegistry.List()
	manifestValues := make([]agent.Manifest, len(manifests))
	for i, m := range manifests {
		manifestValues[i] = *m
	}
	return discovery.NewAgentDiscovery(manifestValues)
}

// TestConfig holds configuration for creating test App instances.
type TestConfig struct {
	AgentsDir   string
	SkillsDir   string
	SessionsDir string
	DataDir     string
	MCPClient   mcpclient.Client
}

// NewForTest creates an App instance for testing with minimal dependencies.
//
// Expected:
//   - tc contains the directory paths for test setup; empty fields use defaults.
//
// Returns:
//   - A minimally initialised App suitable for testing.
//   - An error if any component fails to initialise.
//
// Side effects:
//   - Reads agent and skill files from the configured directories.
//   - Creates session store directories if they do not exist.
func NewForTest(tc TestConfig) (*App, error) {
	if tc.DataDir == "" {
		tc.DataDir = os.TempDir()
	}
	if tc.SessionsDir == "" {
		tc.SessionsDir = filepath.Join(tc.DataDir, "sessions")
	}

	cfg := config.DefaultConfig()
	cfg.AgentDir = tc.AgentsDir
	cfg.SkillDir = tc.SkillsDir
	cfg.DataDir = tc.DataDir

	agentRegistry := agent.NewRegistry()
	if tc.AgentsDir != "" {
		if err := agentRegistry.Discover(tc.AgentsDir); err != nil {
			return nil, fmt.Errorf("discovering agents: %w", err)
		}
	}

	var skills []skill.Skill
	if tc.SkillsDir != "" {
		skillLoader := skill.NewFileSkillLoader(tc.SkillsDir)
		var err error
		skills, err = skillLoader.LoadAll()
		if err != nil {
			return nil, fmt.Errorf("loading skills: %w", err)
		}
	}

	sessionStore, err := ctxstore.NewFileSessionStore(tc.SessionsDir)
	if err != nil {
		return nil, fmt.Errorf("creating session store: %w", err)
	}

	learningsPath := filepath.Join(tc.DataDir, "learnings.json")
	learningStore := learning.NewJSONFileStore(learningsPath)

	manifests := agentRegistry.List()
	manifestValues := make([]agent.Manifest, len(manifests))
	for i, m := range manifests {
		manifestValues[i] = *m
	}
	disc := discovery.NewAgentDiscovery(manifestValues)

	return &App{
		Config:           cfg,
		Registry:         agentRegistry,
		Skills:           skills,
		Engine:           nil,
		Discovery:        disc,
		Sessions:         sessionStore,
		Learning:         learningStore,
		API:              nil,
		mcpClient:        tc.MCPClient,
		providerRegistry: nil,
	}, nil
}
