// Package app provides the main application container and initialization.
package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
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
	pluginpkg "github.com/baphled/flowstate/internal/plugin"
	_ "github.com/baphled/flowstate/internal/plugin/builtin/all" // builtin/all is blank-imported so builtin plugin factories register via init.
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/plugin/external"
	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/plugin/sessionrecorder"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/anthropic"
	"github.com/baphled/flowstate/internal/provider/copilot"
	"github.com/baphled/flowstate/internal/provider/ollama"
	"github.com/baphled/flowstate/internal/provider/openai"
	"github.com/baphled/flowstate/internal/provider/openzen"
	"github.com/baphled/flowstate/internal/provider/zai"
	recall "github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/bash"
	coordinationtool "github.com/baphled/flowstate/internal/tool/coordination"
	"github.com/baphled/flowstate/internal/tool/mcpproxy"
	"github.com/baphled/flowstate/internal/tool/read"
	skilltool "github.com/baphled/flowstate/internal/tool/skill"
	todotool "github.com/baphled/flowstate/internal/tool/todo"
	"github.com/baphled/flowstate/internal/tool/web"
	"github.com/baphled/flowstate/internal/tool/write"
	"github.com/baphled/flowstate/internal/tracer"

	"github.com/baphled/flowstate/internal/plan"
	"github.com/baphled/flowstate/internal/session"
)

// App is the main application container holding all initialized components.
type App struct {
	Config            *config.AppConfig
	Registry          *agent.Registry
	Skills            []skill.Skill
	Engine            *engine.Engine
	Discovery         *discovery.AgentDiscovery
	Sessions          *ctxstore.FileSessionStore
	Learning          *learning.JSONFileStore
	API               *api.Server
	Streamer          streaming.Streamer
	TodoStore         todotool.Store
	mcpClient         mcpclient.Client
	plugins           *pluginRuntime
	providerRegistry  *provider.Registry
	ollamaProvider    *ollama.Provider
	metricsRegistry   *prometheus.Registry
	Store             *plan.Store
	backgroundManager *engine.BackgroundTaskManager
}

// pluginRuntime groups the plugin wiring created during application startup.
type pluginRuntime struct {
	config          config.PluginsConfig
	discoverer      *external.Discoverer
	lifecycle       *external.LifecycleManager
	registry        *pluginpkg.Registry
	healthManager   *failover.HealthManager
	failoverHook    *failover.Hook
	failoverManager *failover.Manager
	dispatcher      *external.Dispatcher
	externalStarted bool
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
	pluginRT := setupPluginRuntime(cfg)
	wireFailoverManager(pluginRT, providerRegistry)
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
		failoverHook:       pluginFailoverHook(pluginRT),
		failoverManager:    pluginFailoverManager(pluginRT),
	})
	if err != nil {
		return nil, err
	}
	if err := loadBuiltinPlugins(cfg, pluginRT, runtime); err != nil {
		return nil, err
	}
	app := buildApp(appBuildParams{
		cfg:              cfg,
		agentRegistry:    agentRegistry,
		skills:           skills,
		runtime:          runtime,
		sessionStore:     sessionStore,
		learningStore:    learningStore,
		providerRegistry: providerRegistry,
		ollamaProvider:   ollamaProvider,
		pluginRuntime:    pluginRT,
	})
	configureApplicationAfterBuild(app, cfg, runtime, defaultManifest, pluginRT)
	return app, nil
}

// loadBuiltinPlugins instantiates builtin plugins after the engine has been created.
//
// Expected:
//   - cfg is a non-nil application configuration.
//   - rt and runtime are fully initialised.
//
// Returns:
//   - An error if builtin plugin loading fails.
//
// Side effects:
//   - Instantiates builtin plugins and registers them in the plugin registry.
func loadBuiltinPlugins(cfg *config.AppConfig, rt *pluginRuntime, runtime *runtimeComponents) error {
	return pluginpkg.LoadBuiltins(pluginpkg.Deps{
		Registry:      rt.registry,
		EventBus:      runtime.engine.EventBus(),
		HealthManager: rt.healthManager,
		PluginsConfig: pluginpkg.PluginsConf{
			Dir:      cfg.Plugins.Dir,
			LogPath:  defaultEventLogPath(),
			LogSize:  10 * 1024 * 1024,
			Timeout:  cfg.Plugins.Timeout,
			Enabled:  cfg.Plugins.Enabled,
			Disabled: cfg.Plugins.Disabled,
		},
	})
}

// configureApplicationAfterBuild applies the remaining startup wiring used by New.
//
// Expected:
//   - app, cfg, runtime, and rt are fully initialised.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Creates the plan store when available.
//   - Applies agent overrides and delegate wiring.
//   - Starts builtin and external plugin wiring.
//   - Starts the session recorder when available.
func configureApplicationAfterBuild(
	app *App,
	cfg *config.AppConfig,
	runtime *runtimeComponents,
	defaultManifest agent.Manifest,
	rt *pluginRuntime,
) {
	eng := runtime.engine
	planDir := filepath.Join(cfg.DataDir, "plans")
	planStore, err := plan.NewStore(planDir)
	if err != nil {
		log.Printf("warning: creating plan store: %v", err)
	} else {
		app.Store = planStore
	}

	app.setAgentOverridesFromConfig(cfg, eng)
	app.wireDelegateToolIfEnabled(eng, defaultManifest)
	if runtime.setEnsureTools != nil {
		runtime.setEnsureTools(func(m agent.Manifest) {
			app.wireDelegateToolIfEnabled(eng, m)
		})
	}
	if app.backgroundManager != nil && app.API != nil {
		app.API.SetBackgroundManager(app.backgroundManager)
	}
	startCorePluginSubscriptions(rt, eng)
	startSessionRecorder(runtime.sessionRecorder, eng)
	startExternalPlugins(rt)
}

// appBuildParams groups the dependencies required to assemble an App value.
type appBuildParams struct {
	cfg              *config.AppConfig
	agentRegistry    *agent.Registry
	skills           []skill.Skill
	runtime          *runtimeComponents
	sessionStore     *ctxstore.FileSessionStore
	learningStore    *learning.JSONFileStore
	providerRegistry *provider.Registry
	ollamaProvider   *ollama.Provider
	pluginRuntime    *pluginRuntime
}

// buildApp assembles the application container from initialised runtime pieces.
//
// Expected:
//   - params contains the fully initialised application dependencies.
//
// Returns:
//   - A wired App instance ready for post-startup adjustments.
//
// Side effects:
//   - Creates the plan store when the destination directory is available.
func buildApp(params appBuildParams) *App {
	cfg := params.cfg
	agentRegistry := params.agentRegistry
	skills := params.skills
	runtime := params.runtime
	sessionStore := params.sessionStore
	learningStore := params.learningStore
	providerRegistry := params.providerRegistry
	ollamaProvider := params.ollamaProvider
	pluginRuntime := params.pluginRuntime
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
		TodoStore:        runtime.todoStore,
		mcpClient:        runtime.mcpManager,
		plugins:          pluginRuntime,
		providerRegistry: providerRegistry,
		ollamaProvider:   ollamaProvider,
		metricsRegistry:  runtime.metricsRegistry,
	}

	planDir := filepath.Join(cfg.DataDir, "plans")
	planStore, err := plan.NewStore(planDir)
	if err != nil {
		log.Printf("warning: creating plan store: %v", err)
	} else {
		app.Store = planStore
	}

	app.setAgentOverridesFromConfig(cfg, runtime.engine)
	app.wireDelegateToolIfEnabled(runtime.engine, selectDefaultManifest(agentRegistry, cfg.DefaultAgent))
	if runtime.setEnsureTools != nil {
		runtime.setEnsureTools(func(m agent.Manifest) {
			app.wireDelegateToolIfEnabled(runtime.engine, m)
		})
	}
	if app.backgroundManager != nil && app.API != nil {
		app.API.SetBackgroundManager(app.backgroundManager)
	}

	return app
}

// setupPluginRuntime initialises the plugin registry and external plugin wiring.
//
// Expected:
//   - cfg is a non-nil AppConfig.
//
// Returns:
//   - A pluginRuntime containing the config values and external plugin managers,
//     or nil when cfg is nil.
//
// Side effects:
//   - Allocates the plugin registry and plugin lifecycle manager.
func setupPluginRuntime(cfg *config.AppConfig) *pluginRuntime {
	if cfg == nil {
		return nil
	}

	registry := pluginpkg.NewRegistry()
	healthManager := failover.NewHealthManager()
	tiers := resolveFailoverTiers(cfg.Plugins.Failover.Tiers)
	chain := failover.NewFallbackChain(defaultFailoverProviders(), tiers)
	failoverHk := failover.NewHook(chain, healthManager)

	return &pluginRuntime{
		config:        cfg.Plugins,
		discoverer:    external.NewDiscoverer(cfg.Plugins),
		lifecycle:     external.NewLifecycleManager(external.NewSpawner(), registry),
		registry:      registry,
		healthManager: healthManager,
		failoverHook:  failoverHk,
		dispatcher:    external.NewDispatcher(registry),
	}
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
	failoverHook       *failover.Hook
	failoverManager    *failover.Manager
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
	contextStore := createContextStore(params.cfg)
	mcpMgr := mcpclient.NewManager()
	todoStore := todotool.NewMemoryStore()
	appTools := buildTools(skill.NewFileSkillLoader(params.cfg.SkillDir), todoStore)
	allServers := mergeMCPServers(params.cfg.MCPServers, config.DiscoverMCPServers())
	appTools = append(appTools, ConnectMCPServers(context.Background(), mcpMgr, allServers)...)
	toolRegistry, permHandler := buildToolsSetup(appTools)
	if params.failoverManager != nil {
		if prefs := buildConfigProviderPreferences(params.cfg); len(prefs) > 0 {
			params.failoverManager.SetBasePreferences(prefs)
		}
	}
	eng, setEnsureTools := createEngine(engineParams{
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
		tokenCounter:       ctxstore.NewTiktokenCounterWithResolver(params.failoverManager, params.cfg.Providers.Default),
		failoverHook:       params.failoverHook,
		failoverManager:    params.failoverManager,
	})
	disc := createDiscovery(params.agentRegistry)
	streamer := createHarnessStreamer(eng, params.agentRegistry, params.cfg.Harness, tracedProvider)
	sessionMgr := session.NewManager(streamer)
	sessRecorder := wireSessionRecorder(params.cfg, sessionMgr)
	apiServer := api.NewServer(
		streamer,
		params.agentRegistry,
		disc,
		params.skills,
		api.WithSessions(params.sessionStore),
		api.WithSessionManager(sessionMgr),
		api.WithTodoStore(todoStore),
		api.WithMetricsHandler(promhttp.HandlerFor(metricsReg, promhttp.HandlerOpts{})),
		api.WithEventBus(eng.EventBus()),
	)
	return &runtimeComponents{
		engine:          eng,
		discovery:       disc,
		streamer:        streamer,
		apiServer:       apiServer,
		metricsRegistry: metricsReg,
		mcpManager:      mcpMgr,
		todoStore:       todoStore,
		sessionRecorder: sessRecorder,
		setEnsureTools:  setEnsureTools,
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
	failoverHook       *failover.Hook
	failoverManager    *failover.Manager
}

// runtimeComponents groups the runtime values created during engine setup.
type runtimeComponents struct {
	engine          *engine.Engine
	discovery       *discovery.AgentDiscovery
	streamer        streaming.Streamer
	apiServer       *api.Server
	metricsRegistry *prometheus.Registry
	mcpManager      mcpclient.Client
	todoStore       todotool.Store
	sessionRecorder *sessionrecorder.Recorder
	setEnsureTools  func(func(agent.Manifest))
}

// createEngine initialises the engine with live manifest getter for hook chain.
//
// Expected:
//   - params contains all required fields populated.
//
// Returns:
//   - A fully initialised Engine with hook chain wired to live manifest.
//   - A setter that accepts a func(agent.Manifest) for lazy ensureTools wiring.
//
// Side effects:
//   - Creates engine and connects MCP servers.
func createEngine(params engineParams) (*engine.Engine, func(func(agent.Manifest))) {
	var eng *engine.Engine
	var ensureToolsFn func(agent.Manifest)
	hookChain := buildHookChain(params.learningStore, func() agent.Manifest {
		if eng != nil {
			return eng.Manifest()
		}
		return params.defaultManifest
	}, params.failoverHook, params.failoverManager,
		&toolWiringCallbacks{
			hasTool: func(name string) bool {
				if eng == nil {
					return false
				}
				return eng.HasTool(name)
			},
			ensureTools: func(m agent.Manifest) {
				if ensureToolsFn != nil {
					ensureToolsFn(m)
				}
			},
			schemaRebuilder: func() []provider.Tool {
				if eng == nil {
					return nil
				}
				return eng.ToolSchemas()
			},
		},
	)
	eng = engine.New(engine.Config{
		ChatProvider:      params.defaultProvider,
		EmbeddingProvider: toEmbeddingProvider(params.ollamaProvider),
		Registry:          params.providerRegistry,
		AgentRegistry:     params.agentRegistry,
		FailoverManager:   params.failoverManager,
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
	setEnsureTools := func(fn func(agent.Manifest)) {
		ensureToolsFn = fn
	}
	return eng, setEnsureTools
}

// setAgentOverridesFromConfig extracts agent overrides from the app config and applies them to the engine.
//
// Expected:
//   - cfg is a non-nil AppConfig.
//   - eng is a fully initialised Engine.
//
// Side effects:
//   - Calls SetAgentOverrides on the engine with the prompt_append values from cfg.
func (a *App) setAgentOverridesFromConfig(cfg *config.AppConfig, eng *engine.Engine) {
	if cfg == nil || len(cfg.AgentOverrides) == 0 {
		return
	}

	overrides := make(map[string]string)
	for agentID, override := range cfg.AgentOverrides {
		if override.PromptAppend != "" {
			overrides[agentID] = override.PromptAppend
		}
	}

	if len(overrides) > 0 {
		eng.SetAgentOverrides(overrides)
	}
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

	allAgents := a.Registry.List()
	engines := make(map[string]*engine.Engine, len(allAgents))
	for _, agentManifest := range allAgents {
		if agentManifest.ID == manifest.ID {
			continue
		}
		targetEngine := a.createDelegateEngine(*agentManifest, coordinationStore)
		engines[agentManifest.ID] = targetEngine
	}

	delegateTool := engine.NewDelegateToolWithBackground(
		engines, manifest.Delegation, manifest.ID, bgManager, coordinationStore,
	)
	delegateTool.WithRegistry(a.Registry)

	if embedder := a.resolveEmbedder(); embedder != nil {
		ed := discovery.NewEmbeddingDiscovery(a.Registry, embedder)
		delegateTool.SetEmbeddingDiscovery(ed)
	}

	categoryRouting := map[string]engine.CategoryConfig{}
	if a.Config != nil {
		categoryRouting = a.Config.CategoryRouting
	}
	resolver := engine.NewCategoryResolver(categoryRouting).
		WithModelLister(a.ListModels)
	delegateTool.WithCategoryResolver(resolver)

	eng.AddTool(delegateTool)

	eng.AddTool(engine.NewBackgroundOutputTool(bgManager))
	eng.AddTool(engine.NewBackgroundCancelTool(bgManager))

	if a.hasCoordinationTool(manifest.Capabilities.Tools) {
		eng.AddTool(coordinationtool.New(coordinationStore))
	}

	a.backgroundManager = bgManager
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
	hookChain := buildHookChain(a.Learning, func() agent.Manifest { return manifest }, nil, nil, nil)
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
// saves it to the Store. This is called after the reviewer approves a plan.
//
// Expected:
//   - chainID is the delegation chain identifier.
//   - coordinationStore contains the plan at "{chainID}/plan" and review at "{chainID}/review".
//
// Returns:
//   - error if the plan cannot be retrieved or saved.
//
// Side effects:
//   - Writes plan file to the Store directory.
func (a *App) PersistApprovedPlan(chainID string, coordinationStore coordination.Store) error {
	if a.Store == nil {
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

	if err := a.Store.Create(planFile); err != nil {
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
//   - todoStore is the app-level Store for persisting todo state.
//
// Returns:
//   - A slice containing bash, file, web, skill_load, and todowrite tools.
//
// Side effects:
//   - Initialises new tool instances.
func buildTools(skillLoader *skill.FileSkillLoader, todoStore todotool.Store) []tool.Tool {
	return []tool.Tool{
		bash.New(),
		read.New(),
		write.New(),
		web.New(),
		skilltool.New(skillLoader),
		todotool.New(todoStore),
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

// toolWiringCallbacks groups the callbacks required to lazily wire delegation
// tools at request time via ToolWiringHook.
type toolWiringCallbacks struct {
	hasTool         func(string) bool
	ensureTools     func(agent.Manifest)
	schemaRebuilder func() []provider.Tool
}

// buildHookChain constructs a hook chain with logging, learning, and skill auto-loading hooks.
// When failoverMgr is non-nil, a StreamHook is appended LAST so provider failover wraps
// the base handler. When only failoverHk is set (legacy path), it is prepended first.
//
// Expected:
//   - learningStore is a non-nil JSONFileStore for persisting learning data.
//   - manifestGetter returns the current agent manifest for skill selection.
//   - failoverHk may be nil; when non-nil and failoverMgr is nil, it is prepended.
//   - failoverMgr may be nil; when non-nil, a StreamHook is appended LAST.
//   - twc may be nil; when non-nil, a ToolWiringHook is appended after SkillAutoLoader.
//
// Returns:
//   - A fully configured hook.Chain ready for use in the engine.
//
// Side effects:
//   - Reads skill-autoloader.yaml from the config directory if it exists.
func buildHookChain(
	learningStore *learning.JSONFileStore,
	manifestGetter func() agent.Manifest,
	failoverHk *failover.Hook,
	failoverMgr *failover.Manager,
	twc *toolWiringCallbacks,
) *hook.Chain {
	cfg, err := hook.LoadSkillAutoLoaderConfig(filepath.Join(config.Dir(), "skill-autoloader.yaml"))
	if err != nil {
		cfg = hook.DefaultSkillAutoLoaderConfig()
	}
	hooks := []hook.Hook{
		hook.LoggingHook(),
	}
	if learningStore != nil {
		hooks = append(hooks, hook.LearningHook(learningStore))
	}
	hooks = append(hooks,
		hook.SkillAutoLoaderHook(cfg, manifestGetter),
	)
	if twc != nil {
		hooks = append(hooks, hook.ToolWiringHook(manifestGetter, twc.hasTool, twc.ensureTools, twc.schemaRebuilder))
	}
	projectRoot, err := os.Getwd()
	if err != nil {
		projectRoot = "."
	}
	hooks = append(hooks,
		hook.PhaseDetectorHook(manifestGetter),
		hook.ContextInjectionHook(manifestGetter, projectRoot),
		tracer.Hook(),
	)
	if failoverMgr != nil {
		streamHook := failover.NewStreamHook(failoverMgr, nil, "")
		hooks = append(hooks, streamHook.Execute)
	} else if failoverHk != nil {
		hooks = append([]hook.Hook{failoverHookAdapter(failoverHk)}, hooks...)
	}
	return hook.NewChain(hooks...)
}

// wireFailoverManager creates a failover.Manager on the plugin runtime using the
// provider registry and health manager. This bridges the gap between plugin setup
// (which has the health manager) and engine setup (which needs the manager).
//
// Expected:
//   - rt may be nil; when nil the function returns immediately.
//   - providerRegistry is the registry containing all configured providers.
//
// Side effects:
//   - Sets rt.failoverManager when rt is non-nil.
func wireFailoverManager(rt *pluginRuntime, providerRegistry *provider.Registry) {
	if rt == nil || rt.healthManager == nil {
		return
	}
	rt.failoverManager = failover.NewManager(providerRegistry, rt.healthManager, 5*time.Minute)
}

// pluginFailoverManager returns the failover manager from the plugin runtime, or nil
// when the runtime is not initialised.
//
// Expected:
//   - rt may be nil; when nil the function returns nil.
//
// Returns:
//   - The failover manager, or nil when rt is nil.
//
// Side effects:
//   - None.
func pluginFailoverManager(rt *pluginRuntime) *failover.Manager {
	if rt == nil {
		return nil
	}
	return rt.failoverManager
}

// pluginFailoverHook returns the failover hook from the plugin runtime, or nil
// when the runtime is not initialised.
//
// Expected:
//   - rt may be nil; when nil the function returns nil.
//
// Returns:
//   - The failover hook, or nil when rt is nil.
//
// Side effects:
//   - None.
func pluginFailoverHook(rt *pluginRuntime) *failover.Hook {
	if rt == nil {
		return nil
	}
	return rt.failoverHook
}

// startCorePluginSubscriptions wires the event logger, rate-limit detector, and
// plugin dispatcher to the engine's EventBus after the engine has been created.
// If either the plugin runtime or the engine is nil, subscriptions are safely skipped.
//
// Expected:
//   - rt may be nil; when non-nil its eventLogger, healthManager, and dispatcher are wired.
//   - eng may be nil; when non-nil its EventBus is used for subscriptions.
//
// Returns:
//   - None.
//
// Side effects:
//   - Starts the event logger (opens file, subscribes to EventBus).
//   - Creates and subscribes a RateLimitDetector to "provider.error" events.
//   - Subscribes the dispatcher to plugin hook events for forwarding to external plugins.
func startCorePluginSubscriptions(rt *pluginRuntime, eng *engine.Engine) {
	if rt == nil || eng == nil {
		return
	}
	bus := eng.EventBus()
	if bus == nil {
		return
	}
	startBusPlugins(rt.registry, bus)
	subscribeRateLimitLogger(bus)
	if rt.dispatcher != nil {
		subscribeDispatcherHooks(rt.dispatcher, bus)
	}
}

// startBusPlugins starts builtin plugins that implement BusStarter.
//
// Expected:
//   - registry and bus may be nil.
//   - registry contains any builtin plugins that need event bus access.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Calls Start on each BusStarter plugin in registry order.
//   - Logs warnings when a plugin fails to start.
func startBusPlugins(registry *pluginpkg.Registry, bus *eventbus.EventBus) {
	if registry == nil || bus == nil {
		return
	}
	for _, name := range registry.Names() {
		plug, ok := registry.Get(name)
		if !ok {
			continue
		}
		starter, ok := plug.(pluginpkg.BusStarter)
		if !ok {
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("panic: starting builtin plugin %q: %v", name, r)
				}
			}()
			if err := starter.Start(bus); err != nil {
				log.Printf("warning: starting builtin plugin %q: %v", name, err)
			}
		}()
	}
}

// subscribeDispatcherHooks wires event forwarding from the engine's EventBus
// to the external plugin dispatcher, enabling plugins to respond to hook events.
//
// The "plugin.event" subscription is an integration point for external plugins.
// No internal component publishes this event; it exists so that external plugin
// processes can emit events that are forwarded to other registered plugins via
// the dispatcher.
//
// Expected:
//   - dispatcher is a valid Dispatcher instance.
//   - bus is a valid EventBus instance.
//
// Returns:
//   - None.
//
// Side effects:
//   - Subscribes to "plugin.event", "tool.execute.before", and "tool.execute.after" events on the bus.
//   - Logs warnings if plugin hook dispatch errors occur.
func subscribeDispatcherHooks(dispatcher *external.Dispatcher, bus *eventbus.EventBus) {
	bus.Subscribe("plugin.event", func(msg any) {
		if evt, ok := msg.(pluginpkg.Event); ok {
			if err := dispatcher.Dispatch(context.Background(), pluginpkg.EventType, evt); err != nil {
				slog.Warn("plugin hook dispatch error", "hook", "event", "error", err)
			}
		}
	})
	bus.Subscribe("tool.execute.before", func(msg any) {
		if args, ok := msg.(*external.ToolExecArgs); ok {
			if err := dispatcher.Dispatch(context.Background(), pluginpkg.ToolExecBefore, args); err != nil {
				slog.Warn("plugin hook dispatch error", "hook", "tool.execute.before", "error", err)
			}
		}
	})
	bus.Subscribe("tool.execute.after", func(msg any) {
		if args, ok := msg.(*external.ToolExecArgs); ok {
			if err := dispatcher.Dispatch(context.Background(), pluginpkg.ToolExecAfter, args); err != nil {
				slog.Warn("plugin hook dispatch error", "hook", "tool.execute.after", "error", err)
			}
		}
	})
}

// subscribeRateLimitLogger subscribes to "provider.rate_limited" events and logs
// them as warnings. The failover hook handles provider switching internally;
// this subscriber ensures rate-limit events are visible in application logs.
//
// Expected:
//   - bus is a valid EventBus instance.
//
// Returns:
//   - None.
//
// Side effects:
//   - Subscribes a logging handler to "provider.rate_limited" on the bus.
func subscribeRateLimitLogger(bus *eventbus.EventBus) {
	bus.Subscribe("provider.rate_limited", func(msg any) {
		if pe, ok := msg.(*events.ProviderEvent); ok {
			slog.Warn("provider rate-limited", "provider", pe.Data.ProviderName)
		}
	})
}

// startExternalPlugins discovers and starts external plugins from the configured
// directory. Discovery or startup failures are logged as warnings and do not
// prevent FlowState from starting.
//
// Expected:
//   - rt may be nil; when nil the function returns immediately.
//
// Returns:
//   - None.
//
// Side effects:
//   - Reads plugin manifests from disk via the discoverer.
//   - Spawns external plugin processes via the lifecycle manager.
//   - Sets rt.externalStarted to true after the attempt completes.
func startExternalPlugins(rt *pluginRuntime) {
	if rt == nil {
		return
	}
	rt.externalStarted = true
	manifests, err := rt.discoverer.Discover(rt.config.Dir)
	if err != nil {
		slog.Warn("discovering external plugins", "dir", rt.config.Dir, "error", err)
		return
	}
	if len(manifests) == 0 {
		return
	}
	if startErr := rt.lifecycle.Start(context.Background(), manifests); startErr != nil {
		slog.Warn("starting external plugins", "error", startErr)
	}
}

// failoverHookAdapter wraps a failover.Hook as a hook.Hook middleware so it can
// be included in the request middleware chain. The failover hook's Apply method
// is called before the next handler; if it returns an error the request is aborted.
//
// Expected:
//   - fh is a non-nil failover.Hook.
//
// Returns:
//   - A hook.Hook middleware wrapping the failover logic.
//
// Side effects:
//   - None.
func failoverHookAdapter(fh *failover.Hook) hook.Hook {
	return func(next hook.HandlerFunc) hook.HandlerFunc {
		return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			if applyErr := fh.Apply(ctx, req); applyErr != nil {
				return nil, applyErr
			}
			return next(ctx, req)
		}
	}
}

// defaultFailoverProviders returns the default provider/model entries for the
// failover fallback chain, ordered by tier preference.
//
// Returns:
//   - A slice of ProviderModel entries from Tier0 through Tier3.
//
// Side effects:
//   - None.
func defaultFailoverProviders() []failover.ProviderModel {
	return []failover.ProviderModel{
		{Provider: "anthropic", Model: "claude-sonnet-4-20250514"},
		{Provider: "github-copilot", Model: "claude-sonnet-4-20250514"},
		{Provider: "openai", Model: "gpt-4o"},
		{Provider: "ollama", Model: "llama3.2"},
	}
}

// resolveFailoverTiers returns the configured tiers when non-empty, falling
// back to the hardcoded defaults otherwise.
//
// Expected:
//   - configTiers is the map from the application configuration; may be nil or empty.
//
// Returns:
//   - The configTiers map when it contains at least one entry, or the default tiers.
//
// Side effects:
//   - None.
func resolveFailoverTiers(configTiers map[string]string) map[string]string {
	if len(configTiers) > 0 {
		return configTiers
	}
	return defaultFailoverTiers()
}

// defaultFailoverTiers returns the default tier assignments for each provider.
//
// Returns:
//   - A map from provider name to tier constant.
//
// Side effects:
//   - None.
func defaultFailoverTiers() map[string]string {
	return map[string]string{
		"anthropic":      failover.Tier0,
		"github-copilot": failover.Tier1,
		"openai":         failover.Tier2,
		"ollama":         failover.Tier3,
	}
}

// defaultEventLogPath returns the default file path for the event logger output.
//
// Returns:
//   - A path under the user's cache directory, or a temp directory fallback.
//
// Side effects:
//   - None.
func defaultEventLogPath() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = os.TempDir()
	}
	return filepath.Join(cacheDir, "flowstate", "events.jsonl")
}

// startSessionRecorder starts the session recorder's EventBus subscriptions
// when session recording is enabled.
//
// Expected:
//   - recorder may be nil; when nil the function returns immediately.
//   - eng is a non-nil Engine with a valid EventBus.
//
// Returns: none.
// Side effects: subscribes the recorder to EventBus events.
func startSessionRecorder(recorder *sessionrecorder.Recorder, eng *engine.Engine) {
	if recorder == nil || eng == nil {
		return
	}
	bus := eng.EventBus()
	if bus == nil {
		return
	}
	if err := recorder.Start(bus); err != nil {
		log.Printf("warning: starting session recorder: %v", err)
	}
}

// defaultSessionRecordingDir returns the default directory for session recording output.
//
// Returns:
//   - A path under the user's cache directory, or a temp directory fallback.
//
// Side effects:
//   - None.
func defaultSessionRecordingDir() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = os.TempDir()
	}
	return filepath.Join(cacheDir, "flowstate", "session-recordings")
}

// wireSessionRecorder creates and attaches a session recorder to the session
// manager when session recording is enabled in the configuration. The returned
// recorder must be started separately via its Start method to begin subscribing
// to EventBus events.
//
// Expected:
//   - cfg is a non-nil AppConfig.
//   - sessionMgr is a fully initialised session Manager.
//
// Returns: the recorder when session recording is enabled, nil otherwise.
// Side effects: creates a Recorder and wires it to the session manager.
func wireSessionRecorder(cfg *config.AppConfig, sessionMgr *session.Manager) *sessionrecorder.Recorder {
	if cfg == nil || !cfg.SessionRecording {
		return nil
	}

	recorder := sessionrecorder.New(defaultSessionRecordingDir())
	if err := recorder.Init(); err != nil {
		log.Printf("warning: initialising session recorder: %v", err)
		return nil
	}

	sessionMgr.SetRecorder(recorder)
	log.Printf("info: session recording enabled, writing to %s", defaultSessionRecordingDir())
	return recorder
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

	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")
	if anthropicKey == "" {
		anthropicKey = cfg.Providers.Anthropic.APIKey
	}
	anthropicProvider, anthropicErr := anthropic.NewFromOpenCodeOrConfig(opencodePath, anthropicKey)
	if anthropicErr == nil {
		providerRegistry.Register(anthropicProvider)
	}

	githubToken := os.Getenv("GITHUB_TOKEN")
	if githubToken == "" {
		githubToken = cfg.Providers.GitHub.APIKey
	}
	copilotProvider, copilotErr := copilot.NewFromOpenCodeOrFallback(opencodePath, nil, githubToken)
	if copilotErr == nil {
		providerRegistry.Register(copilotProvider)
	}

	zaiKey := os.Getenv("ZAI_API_KEY")
	if zaiKey == "" {
		zaiKey = cfg.Providers.ZAI.APIKey
	}
	zaiProvider, zaiErr := zai.NewFromOpenCodeOrConfig(opencodePath, zaiKey)
	if zaiErr == nil {
		providerRegistry.Register(zaiProvider)
	}

	openzenKey := os.Getenv("OPENZEN_API_KEY")
	if openzenKey == "" {
		openzenKey = cfg.Providers.OpenZen.APIKey
	}
	openzenProvider, openzenErr := openzen.NewFromOpenCodeOrConfig(opencodePath, openzenKey)
	if openzenErr == nil {
		providerRegistry.Register(openzenProvider)
	}
	return providerRegistry, ollamaProvider
}

// buildConfigProviderPreferences constructs a provider preference list from application
// configuration, ordered so that cfg.Providers.Default is always tried first.
//
// Expected:
//   - cfg is a non-nil AppConfig with provider configuration.
//
// Returns:
//   - A slice of ModelPreference values in default-first order, skipping providers
//     with no model configured.
//
// Side effects:
//   - None.
func buildConfigProviderPreferences(cfg *config.AppConfig) []provider.ModelPreference {
	type namedProvider struct {
		name  string
		model string
	}

	allProviders := []namedProvider{
		{"ollama", cfg.Providers.Ollama.Model},
		{"anthropic", cfg.Providers.Anthropic.Model},
		{"openai", cfg.Providers.OpenAI.Model},
		{"github", cfg.Providers.GitHub.Model},
	}

	defaultName := cfg.Providers.Default
	sorted := make([]namedProvider, 0, len(allProviders))
	for _, p := range allProviders {
		if p.name == defaultName {
			sorted = append([]namedProvider{p}, sorted...)
		} else {
			sorted = append(sorted, p)
		}
	}

	var prefs []provider.ModelPreference
	for _, p := range sorted {
		if p.model == "" {
			continue
		}
		prefs = append(prefs, provider.ModelPreference{
			Provider: p.name,
			Model:    p.model,
		})
	}
	return prefs
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
	return buildHookChain(learningStore, manifestGetter, nil, nil, nil)
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
//
// Side effects:
//   - None; creates an in-memory context store with no file I/O.
func createContextStore(cfg *config.AppConfig) *recall.FileContextStore {
	return recall.NewEmptyContextStore(cfg.Providers.Ollama.Model)
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

// resolveEmbedder returns the best available embedding provider for discovery.
// The Ollama provider is preferred as it runs locally; otherwise, the first
// registered provider from the registry is used.
//
// Returns:
//   - A discovery.EmbeddingProvider when any provider is available.
//   - nil if no provider is available.
//
// Side effects:
//   - None.
func (a *App) resolveEmbedder() discovery.EmbeddingProvider {
	if a.ollamaProvider != nil {
		return a.ollamaProvider
	}
	if a.providerRegistry == nil {
		return nil
	}
	names := a.providerRegistry.List()
	if len(names) == 0 {
		return nil
	}
	p, err := a.providerRegistry.Get(names[0])
	if err != nil {
		return nil
	}
	return p
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

// BackgroundManager returns the background task manager for delegation.
//
// Expected:
//   - None.
//
// Returns:
//   - The background task manager, or nil if not configured.
//
// Side effects:
//   - None.
func (a *App) BackgroundManager() *engine.BackgroundTaskManager {
	return a.backgroundManager
}

// PluginConfigForTest returns the plugin configuration wired into the app.
//
// Returns:
//   - The plugin configuration currently attached to the app, or a zero-value
//     configuration when plugin wiring has not been initialised.
//
// Side effects:
//   - None.
func (a *App) PluginConfigForTest() config.PluginsConfig {
	if a.plugins == nil {
		return config.PluginsConfig{}
	}
	return a.plugins.config
}

// HasEventLogger reports whether the event logger is configured in the plugin runtime.
//
// Returns:
//   - true if the event logger is present, false otherwise.
//
// Side effects:
//   - None.
func (a *App) HasEventLogger() bool {
	if a.plugins == nil || a.plugins.registry == nil {
		return false
	}
	_, ok := a.plugins.registry.Get("event-logger")
	return ok
}

// HasFailoverHook reports whether the failover hook is configured in the plugin runtime.
//
// Returns:
//   - true if the failover hook is present, false otherwise.
//
// Side effects:
//   - None.
func (a *App) HasFailoverHook() bool {
	return a.plugins != nil && a.plugins.failoverHook != nil
}

// HasDispatcher reports whether the external plugin dispatcher is configured in the plugin runtime.
//
// Returns:
//   - true if the dispatcher is present, false otherwise.
//
// Side effects:
//   - None.
func (a *App) HasDispatcher() bool {
	return a.plugins != nil && a.plugins.dispatcher != nil
}

// ExternalPluginsStarted reports whether external plugin discovery and startup
// was attempted during application initialisation.
//
// Returns:
//   - true if external plugin startup was attempted, false otherwise.
//
// Side effects:
//   - None.
func (a *App) ExternalPluginsStarted() bool {
	return a.plugins != nil && a.plugins.externalStarted
}

// ClosePlugins shuts down plugin runtime resources, stopping external plugin
// processes and closing the event logger.
//
// Returns:
//   - An error if closing the event logger fails, nil otherwise.
//
// Side effects:
//   - Stops all active external plugin processes via the lifecycle manager.
//   - Closes the event logger file handle if present.
func (a *App) ClosePlugins() error {
	if a.plugins == nil {
		return nil
	}
	if a.plugins.lifecycle != nil {
		if err := a.plugins.lifecycle.Stop(context.Background()); err != nil {
			slog.Warn("stopping external plugins", "error", err)
		}
	}
	if a.plugins.registry != nil {
		if plug, ok := a.plugins.registry.Get("event-logger"); ok {
			if closer, ok := plug.(interface{ Close() error }); ok {
				return closer.Close()
			}
		}
	}
	return nil
}

// BuildHookChainForTestWithFailover is a test helper that exposes buildHookChain
// with a non-nil failover hook for verifying chain length.
//
// Expected:
//   - learningStore may be nil for testing purposes.
//   - manifestGetter returns the current agent manifest.
//
// Returns:
//   - A hook.Chain with the failover hook included.
//
// Side effects:
//   - None.
func BuildHookChainForTestWithFailover(
	learningStore *learning.JSONFileStore,
	manifestGetter func() agent.Manifest,
) *hook.Chain {
	health := failover.NewHealthManager()
	chain := failover.NewFallbackChain(defaultFailoverProviders(), defaultFailoverTiers())
	fh := failover.NewHook(chain, health)
	return buildHookChain(learningStore, manifestGetter, fh, nil, nil)
}

// SetBackgroundManager sets the background task manager.
//
// Expected:
//   - mgr is a valid BackgroundTaskManager or nil.
//
// Returns:
//   - None.
//
// Side effects:
//   - Stores the background manager for later access.
func (a *App) SetBackgroundManager(mgr *engine.BackgroundTaskManager) {
	a.backgroundManager = mgr
}
