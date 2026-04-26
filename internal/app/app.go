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
	"sort"
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
	qdrantrecall "github.com/baphled/flowstate/internal/recall/qdrant"
	vaultrecall "github.com/baphled/flowstate/internal/recall/vault"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/bash"
	coordinationtool "github.com/baphled/flowstate/internal/tool/coordination"
	"github.com/baphled/flowstate/internal/tool/mcpproxy"
	plantool "github.com/baphled/flowstate/internal/tool/plan"
	"github.com/baphled/flowstate/internal/tool/read"
	toolrecall "github.com/baphled/flowstate/internal/tool/recall"
	skilltool "github.com/baphled/flowstate/internal/tool/skill"
	todotool "github.com/baphled/flowstate/internal/tool/todo"
	"github.com/baphled/flowstate/internal/tool/web"
	"github.com/baphled/flowstate/internal/tool/write"
	"github.com/baphled/flowstate/internal/tracer"

	"github.com/baphled/flowstate/internal/plan"
	"github.com/baphled/flowstate/internal/session"
)

// MCPConnectionResult contains the result of attempting to connect to an MCP server.
type MCPConnectionResult struct {
	Name      string
	Success   bool
	Error     string
	ToolCount int
}

// App is the main application container holding all initialized components.
type App struct {
	Config                 *config.AppConfig
	Registry               *agent.Registry
	Skills                 []skill.Skill
	Engine                 *engine.Engine
	Discovery              *discovery.AgentDiscovery
	Sessions               *ctxstore.FileSessionStore
	Learning               learning.Store
	API                    *api.Server
	Streamer               streaming.Streamer
	TodoStore              todotool.Store
	mcpClient              mcpclient.Client
	plugins                *pluginRuntime
	providerRegistry       *provider.Registry
	ollamaProvider         *ollama.Provider
	metricsRegistry        *prometheus.Registry
	Store                  *plan.Store
	defaultProvider        provider.Provider
	backgroundManager      *engine.BackgroundTaskManager
	sessionManager         *session.Manager
	completionOrchestrator *engine.CompletionOrchestrator
	// compression is the shared compression wiring for both the root
	// engine and every delegate engine. Retained on App so
	// createDelegateEngine can reuse the same CompressionConfig, metrics,
	// recorder, and L2/L3 stores without re-reading cfg or
	// re-constructing the summariser adapter.
	compression compressionComponents
	// mcpServerTools maps each configured MCP server name to the tool
	// names it exposes. Retained on App so createDelegateEngine can pass
	// the same map into every delegate engine; otherwise a delegate whose
	// manifest opts into an MCP server would silently receive zero tools
	// from it because buildAllowedToolSet would have nothing to merge.
	// See ADR - MCP Tool Gating by Agent Manifest for the full contract.
	mcpServerTools map[string][]string
	// mcpTools holds the proxy tool implementations backing each
	// connected MCP server. Retained on App so
	// buildToolsForManifestWithStore can append them to a delegate
	// engine's tool slice; the engine then gates them through
	// buildAllowedToolSet by the delegate manifest's MCPServers list.
	mcpTools []tool.Tool
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
	// Apply the cluster-wide embedding-model knob before any manifest
	// is loaded — applyDefaults consumes the package-level fallback,
	// so seeding it here ensures freshly-seeded agent manifests inherit
	// the configured value rather than the historical default.
	agent.SetDefaultEmbeddingModel(cfg.ResolvedEmbeddingModel())

	// One-time XDG_DATA -> XDG_CONFIG migration: agent manifests used to
	// live in `~/.local/share/flowstate/agents/` but they are user-edited
	// config and now belong in `~/.config/flowstate/agents/`. The helper
	// is a no-op when the new dir already exists or the legacy dir is
	// empty/missing, so it is safe to call unconditionally on startup.
	legacyAgentsDir := filepath.Join(config.DataDir(), "agents")
	if cfg.AgentDir != "" && cfg.AgentDir != legacyAgentsDir {
		if _, err := MigrateAgentsToConfigDir(legacyAgentsDir, cfg.AgentDir); err != nil {
			log.Printf("warning: migrating agent manifests from %q to %q: %v",
				legacyAgentsDir, cfg.AgentDir, err)
		}
	}

	if err := SeedAgentsDir(EmbeddedAgentsFS(), cfg.AgentDir); err != nil {
		log.Printf("warning: seeding agents to %q: %v", cfg.AgentDir, err)
	} else {
		log.Printf("info: agents seeded to %q", cfg.AgentDir)
	}

	providerRegistry, ollamaProvider, providerFailures := setupProvidersWithFailures(cfg)
	agentRegistry := setupAgentRegistry(cfg)
	defaultManifest := selectDefaultManifest(agentRegistry, cfg.DefaultAgent)
	skills, alwaysActiveSkills := loadSkills(cfg, defaultManifest)
	if err := resolveDefaultProvider(providerRegistry, providerFailures, cfg.Providers.Default); err != nil {
		return nil, err
	}
	sessionStore, learningStore, err := createDataStores(cfg, ollamaProvider)
	if err != nil {
		return nil, err
	}
	runOrphanEventTmpScan(filepath.Join(cfg.DataDir, "sessions"))
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
		dispatcher:         pluginDispatcher(pluginRT),
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
	planDir := cfg.ResolvedPlanLocation()
	planStore, err := plan.NewStore(planDir)
	if err != nil {
		log.Printf("warning: creating plan store: %v", err)
	} else {
		app.Store = planStore
	}

	app.setAgentOverridesFromConfig(cfg, eng)
	app.wireDelegateToolIfEnabled(eng, defaultManifest)
	app.wireSuggestDelegateToolIfDisabled(eng, defaultManifest)
	if runtime.setEnsureTools != nil {
		runtime.setEnsureTools(func(m agent.Manifest) {
			app.wireDelegateToolIfEnabled(eng, m)
			app.wireSuggestDelegateToolIfDisabled(eng, m)
		})
	}
	if app.backgroundManager != nil && app.API != nil {
		app.API.SetBackgroundManager(app.backgroundManager)
	}
	if app.backgroundManager != nil && app.sessionManager != nil && eng.EventBus() != nil {
		var broker engine.SessionBrokerPublisher
		if app.API != nil {
			sessionBroker := api.NewSessionBroker()
			app.API.SetSessionBroker(sessionBroker)
			broker = sessionBroker
		}
		app.completionOrchestrator = engine.NewCompletionOrchestrator(
			app.backgroundManager, app.sessionManager, eng.EventBus(), broker,
		)
		app.completionOrchestrator.Start()
		if app.API != nil {
			app.API.SetCompletionOrchestrator(app.completionOrchestrator)
		}
	}
	wireSessionStatusSync(eng.EventBus(), app.sessionManager)
	startCorePluginSubscriptions(rt, eng, buildDistiller(cfg, runtime.defaultProvider, app.ollamaProvider), runtime.mcpManager)
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
	learningStore    learning.Store
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
		defaultProvider:  runtime.defaultProvider,
		sessionManager:   runtime.sessionManager,
		compression:      runtime.compression,
		mcpServerTools:   runtime.mcpServerTools,
		mcpTools:         runtime.mcpTools,
	}

	planDir := cfg.ResolvedPlanLocation()
	planStore, err := plan.NewStore(planDir)
	if err != nil {
		log.Printf("warning: creating plan store: %v", err)
	} else {
		app.Store = planStore
	}

	app.setAgentOverridesFromConfig(cfg, runtime.engine)
	app.restorePersistedSessions()
	defaultForRuntime := selectDefaultManifest(agentRegistry, cfg.DefaultAgent)
	app.wireDelegateToolIfEnabled(runtime.engine, defaultForRuntime)
	app.wireSuggestDelegateToolIfDisabled(runtime.engine, defaultForRuntime)
	if runtime.setEnsureTools != nil {
		runtime.setEnsureTools(func(m agent.Manifest) {
			app.wireDelegateToolIfEnabled(runtime.engine, m)
			app.wireSuggestDelegateToolIfDisabled(runtime.engine, m)
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
	// appLevelSkillNames is the app-level default-active skill list
	// (cfg.AlwaysActiveSkills). Forwarded alongside skillDir so
	// createEngine can build a SkillsResolver closure that re-runs
	// engine.LoadAlwaysActiveSkills on every SetManifest swap —
	// otherwise a root engine reused by `flowstate run --agent <id>`
	// keeps the skills resolved for the startup manifest forever and
	// the swapped-in manifest's declared default-active skills
	// silently drop from LoadedSkills. Mirrors
	// App.resolveDelegateSkills on the delegate-engine construction
	// path.
	appLevelSkillNames   []string
	contextStore         *recall.FileContextStore
	chainStore           recall.ChainContextStore
	learningStore        learning.Store
	appTools             []tool.Tool
	toolRegistry         *tool.Registry
	permissionHandler    tool.PermissionHandler
	agentsFileLoader     *agent.AgentsFileLoader
	tokenCounter         ctxstore.TokenCounter
	contextAssemblyHooks []pluginpkg.ContextAssemblyHook
	failoverHook         *failover.Hook
	failoverManager      *failover.Manager
	mcpServerTools       map[string][]string
	dispatcher           *external.Dispatcher
	skillDir             string
	recallBroker         recall.Broker
	// compression carries the three-layer compression dependencies that
	// must flow into the Engine for L1/L2/L3 to activate. Zero values on
	// any field disable the corresponding layer. See buildCompressionComponents.
	compression compressionComponents
	// streamTimeout / toolTimeout carry the user-configured engine timeouts
	// from cfg.StreamTimeout / cfg.ToolTimeout. Zero means inherit the
	// engine's compiled-in defaults (5m / 2m). The delegate tool already
	// opts out of toolTimeout via TimeoutOverrider.
	streamTimeout time.Duration
	toolTimeout   time.Duration
	// backgroundOutputTimeout overrides the default poll-until-complete
	// budget on the background_output tool. Zero means inherit the
	// compiled-in default (120s).
	backgroundOutputTimeout time.Duration
}

// compressionComponents bundles the wiring required to activate the
// three-layer context compression system on an engine. Each field is
// gated on its corresponding Enabled flag in cfg.Compression, so the
// zero value of this struct is a correct "compression disabled"
// configuration.
type compressionComponents struct {
	// autoCompactor is the L2 orchestrator; nil disables L2 regardless
	// of cfg.Compression.AutoCompaction.Enabled.
	autoCompactor *ctxstore.AutoCompactor
	// config is the parsed cfg.Compression block. Passing it in unaltered
	// is deliberate: the engine reads individual Enabled flags so a
	// partially wired struct still lets the engine short-circuit cleanly.
	config ctxstore.CompressionConfig
	// metrics tracks per-builder compaction counters. Always non-nil so
	// the engine can increment without nil-guarding at the hot path.
	metrics *ctxstore.CompressionMetrics
	// sessionMemoryStore is the L3 read-side store; nil disables L3 even
	// if cfg.Compression.SessionMemory.Enabled is true.
	sessionMemoryStore *recall.SessionMemoryStore
	// recorder is the shared tracer.Recorder. Always non-nil (the no-op
	// case is filled in by a real PrometheusRecorder so
	// RecordCompressionTokensSaved and RecordContextWindowTokens surface
	// through /metrics).
	recorder tracer.Recorder
	// summariserAdapter is the production ctxstore.Summariser bound to
	// the engine's chat provider. Retained here so call-site wiring can
	// rebind the manifest after the engine is constructed.
	summariserAdapter *engine.ProviderSummariser
	// knowledgeExtractorFactory lazily produces a per-session L3
	// extractor. Non-nil iff cfg.Compression.SessionMemory.Enabled and
	// the session-memory store is live. Nil leaves L3 inert.
	knowledgeExtractorFactory func(sessionID string) *recall.KnowledgeExtractor
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
	traced, err := buildTracedProvider(params.providerRegistry, params.cfg.Providers.Default)
	if err != nil {
		return nil, err
	}
	tp := buildToolPipeline(params.cfg)
	applyFailoverPreferences(params.failoverManager, params.cfg)
	contextStore := createContextStore(params.cfg)
	chainStore := createChainStore(params.cfg)
	broker := buildRecallBrokerFromSetup(params, traced.provider, tp.mcpManager, contextStore, chainStore)
	compression := buildCompressionComponents(params.cfg, params.agentRegistry, traced.provider, traced.recorder)
	eng, setEnsureTools := createEngine(buildEngineParams(engineAssemblyParams{
		setup:        params,
		traced:       traced,
		tools:        tp,
		contextStore: contextStore,
		chainStore:   chainStore,
		broker:       broker,
		compression:  compression,
	}))
	bindCompressionManifest(compression, params.defaultManifest)
	disc := createDiscovery(params.agentRegistry)
	streamer := createHarnessStreamer(eng, params.agentRegistry, params.cfg.Harness, traced.provider, params.cfg.DefaultProviderModel())
	sessionMgr := session.NewManager(streamer)
	sessRecorder := wireSessionRecorder(params.cfg, sessionMgr, sessionsDirFromCfg(params.cfg))
	apiServer := api.NewServer(
		streamer,
		params.agentRegistry,
		disc,
		params.skills,
		api.WithSessions(params.sessionStore),
		api.WithSessionManager(sessionMgr),
		api.WithTodoStore(tp.todoStore),
		api.WithMetricsHandler(promhttp.HandlerFor(traced.metrics, promhttp.HandlerOpts{})),
		api.WithEventBus(eng.EventBus()),
	)
	return &runtimeComponents{
		engine:          eng,
		defaultProvider: traced.provider,
		discovery:       disc,
		streamer:        streamer,
		apiServer:       apiServer,
		metricsRegistry: traced.metrics,
		compression:     compression,
		mcpManager:      tp.mcpManager,
		todoStore:       tp.todoStore,
		sessionRecorder: sessRecorder,
		setEnsureTools:  setEnsureTools,
		sessionManager:  sessionMgr,
		mcpServerTools:  tp.mcpServerTools,
		mcpTools:        tp.mcpTools,
	}, nil
}

// engineAssemblyParams bundles the locals setupEngine accumulates en
// route to building the engine's configuration. Declared as a struct so
// buildEngineParams stays under the 5-argument revive gate.
type engineAssemblyParams struct {
	setup        setupEngineParams
	traced       tracedBundle
	tools        toolPipelineResult
	contextStore *recall.FileContextStore
	chainStore   recall.ChainContextStore
	broker       recall.Broker
	compression  compressionComponents
}

// buildEngineParams assembles the engineParams bundle from setupEngine
// locals. Extracted so setupEngine stays under the 60-line funlen gate
// now that compression wiring adds five additional fields to the engine
// configuration surface.
//
// Expected:
//   - in carries every local setupEngine needs to emit; see
//     engineAssemblyParams for the per-field contract.
//
// Returns:
//   - A fully populated engineParams value ready for createEngine.
//
// Side effects:
//   - None.
func buildEngineParams(in engineAssemblyParams) engineParams {
	appTools := appendChainTools(in.tools.tools, in.chainStore)
	return engineParams{
		defaultProvider:      in.traced.provider,
		ollamaProvider:       in.setup.ollamaProvider,
		providerRegistry:     in.setup.providerRegistry,
		agentRegistry:        in.setup.agentRegistry,
		defaultManifest:      in.setup.defaultManifest,
		alwaysActiveSkills:   in.setup.alwaysActiveSkills,
		appLevelSkillNames:   in.setup.cfg.AlwaysActiveSkills,
		contextStore:         in.contextStore,
		chainStore:           in.chainStore,
		learningStore:        in.setup.learningStore,
		appTools:             appTools,
		toolRegistry:         in.tools.toolRegistry,
		permissionHandler:    in.tools.permissionHandler,
		mcpServerTools:       in.tools.mcpServerTools,
		agentsFileLoader:     buildAgentsFileLoader(),
		tokenCounter:         ctxstore.NewTiktokenCounterWithResolver(in.setup.failoverManager, in.setup.cfg.Providers.Default),
		contextAssemblyHooks: in.setup.cfg.ContextAssemblyHooks,
		failoverHook:         in.setup.failoverHook,
		failoverManager:      in.setup.failoverManager,
		dispatcher:           in.setup.dispatcher,
		skillDir:                in.setup.cfg.SkillDir,
		recallBroker:            in.broker,
		compression:             in.compression,
		streamTimeout:           in.setup.cfg.ParsedStreamTimeout(),
		toolTimeout:             in.setup.cfg.ParsedToolTimeout(),
		backgroundOutputTimeout: in.setup.cfg.ParsedBackgroundOutputTimeout(),
	}
}

// appendChainTools appends chain context tools when a chain store is available.
// This gives the root engine cross-agent context queries without delegation.
//
// Expected:
//   - base is the existing tool slice (not nil).
//   - cs may be nil; when nil the original slice is returned unchanged.
//
// Returns:
//   - The tool slice with chain_search_context and chain_get_messages appended,
//     or the original slice when cs is nil.
//
// Side effects:
//   - None.
func appendChainTools(base []tool.Tool, cs recall.ChainContextStore) []tool.Tool {
	if cs == nil {
		return base
	}
	return append(base,
		toolrecall.NewChainSearchTool(cs),
		toolrecall.NewChainGetMessagesTool(cs),
	)
}

// createCoordinationStore returns a file-backed coordination store when
// DataDir is configured, falling back to an in-memory store otherwise.
//
// Expected:
//   - cfg may be nil.
//
// Returns:
//   - A coordination.Store (file-backed when DataDir is set, in-memory otherwise).
//
// Side effects:
//   - Creates the coordination JSON file directory if it does not exist.
func createCoordinationStore(cfg *config.AppConfig) coordination.Store {
	if cfg != nil && cfg.DataDir != "" {
		coordPath := filepath.Join(cfg.DataDir, "coordination.json")
		fs, err := coordination.NewFileStore(coordPath)
		if err != nil {
			slog.Warn("failed to create file-backed coordination store, falling back to memory", "error", err)
			return coordination.NewMemoryStore()
		}
		return fs
	}
	return coordination.NewMemoryStore()
}

// wrapCoordinationStoreWithApproval decorates a coordination.Store so that
// every write to `<chainID>/review` carrying an APPROVE verdict triggers
// an asynchronous flush of `<chainID>/plan` to the App's plan store on
// disk. Acts as a belt-and-braces backup behind the agent-facing
// plan_write tool: a plan-writer agent that forgets to call plan_write
// still ends up with a persisted plan on disk because the post-review
// approval write here drives it.
//
// Expected:
//   - inner is a non-nil coordination.Store.
//   - a is the App whose Store is the destination plan.Store. May be nil
//     in test fixtures; in that case the wrapper short-circuits to a
//     no-op callback so wiring stays uniform.
//
// Returns:
//   - A coordination.Store wrapping inner with the approval observer
//     installed.
//
// Side effects:
//   - None at construction; the inner Set hot path is unchanged for
//     non-review writes.
func wrapCoordinationStoreWithApproval(inner coordination.Store, a *App) coordination.Store {
	if inner == nil {
		return nil
	}
	if a == nil {
		return inner
	}
	cb := func(chainID string, store coordination.Store) {
		if err := a.PersistApprovedPlan(chainID, store); err != nil {
			slog.Debug("post-approval plan persistence skipped",
				"chain_id", chainID, "reason", err)
		}
	}
	return coordination.NewPersistingStore(inner, cb)
}

// bindCompressionManifest rebinds the summariser adapter to the default
// manifest after createEngine returns. Without this step the L2 category
// router defaults every call to "quick" even when the agent's manifest
// specifies a richer summary tier.
//
// Expected:
//   - compression is the bundle produced by buildCompressionComponents.
//     A nil summariserAdapter (compression disabled) is a safe no-op.
//   - manifest is the default agent manifest from app configuration.
//
// Side effects:
//   - Mutates compression.summariserAdapter when present.
func bindCompressionManifest(compression compressionComponents, manifest agent.Manifest) {
	if compression.summariserAdapter == nil {
		return
	}
	m := manifest
	compression.summariserAdapter.WithManifest(&m)
}

// buildRecallBrokerFromSetup is a thin helper that translates setupEngine's
// locals into a recallBrokerParams value. Keeping this as a helper prevents
// setupEngine from exceeding the 60-line function length limit.
//
// Expected:
//   - params is the setupEngine input bundle.
//   - chatProvider is the traced chat provider; retained for compatibility.
//   - mcpClient, contextStore, chainStore are the recall source dependencies.
//
// Returns:
//   - The recall.Broker as constructed by buildRecallBroker.
//
// Side effects:
//   - Delegates to buildRecallBroker; no direct I/O.
func buildRecallBrokerFromSetup(
	params setupEngineParams,
	chatProvider provider.Provider,
	mcpClient mcpclient.Client,
	contextStore *recall.FileContextStore,
	chainStore recall.ChainContextStore,
) recall.Broker {
	return buildRecallBroker(recallBrokerParams{
		cfg:            params.cfg,
		chatProvider:   chatProvider,
		mcpClient:      mcpClient,
		contextStore:   contextStore,
		chainStore:     chainStore,
		ollamaProvider: params.ollamaProvider,
	})
}

// toolPipelineResult groups the outputs of buildToolPipeline.
type toolPipelineResult struct {
	mcpManager        mcpclient.Client
	todoStore         todotool.Store
	tools             []tool.Tool
	toolRegistry      *tool.Registry
	permissionHandler tool.PermissionHandler
	mcpServerTools    map[string][]string
	// mcpTools holds the proxy tools backing each connected MCP server,
	// kept separately so delegate engines can append them to their own
	// per-manifest tool slice. Without this, an agent whose manifest
	// declares mcp_servers: [vault-rag] would have the gate authorise
	// vault-rag tools but find none registered to call.
	mcpTools []tool.Tool
}

// buildToolPipeline creates the MCP manager, todo store, and tool registry
// needed by the engine.
//
// Expected:
//   - cfg is a non-nil AppConfig.
//
// Returns:
//   - A toolPipelineResult containing all assembled dependencies.
//
// Side effects:
//   - Connects to configured MCP servers.
func buildToolPipeline(cfg *config.AppConfig) toolPipelineResult {
	mcpMgr := mcpclient.NewManager()
	todoStore := todotool.NewMemoryStore()
	appTools := buildTools(skill.NewFileSkillLoader(cfg.SkillDir), todoStore, cfg.ResolvedPlanLocation())
	allServers := mergeMCPServers(cfg.MCPServers, config.DiscoverMCPServers())
	mcpTools, results, serverToolNames := ConnectMCPServers(context.Background(), mcpMgr, allServers)
	appTools = append(appTools, mcpTools...)

	// Log MCP connection summary
	connected := 0
	for _, r := range results {
		if r.Success {
			connected++
		} else {
			log.Printf("warning: MCP server %q: %v", r.Name, r.Error)
		}
	}
	toolCount := 0
	for _, r := range results {
		toolCount += r.ToolCount
	}
	log.Printf("info: MCP servers: %d/%d connected (%d tools available)", connected, len(results), toolCount)
	toolRegistry, permHandler := buildToolsSetup(appTools)
	return toolPipelineResult{
		mcpManager:        mcpMgr,
		todoStore:         todoStore,
		tools:             appTools,
		toolRegistry:      toolRegistry,
		permissionHandler: permHandler,
		mcpServerTools:    serverToolNames,
		mcpTools:          mcpTools,
	}
}

// applyFailoverPreferences sets base preferences on the failover manager when configured.
//
// Expected:
//   - failoverManager may be nil.
//   - cfg is a non-nil AppConfig.
//
// Side effects:
//   - Sets base preferences on the failover manager if non-nil and preferences exist.
func applyFailoverPreferences(failoverManager *failover.Manager, cfg *config.AppConfig) {
	if failoverManager == nil {
		return
	}
	if prefs := buildConfigProviderPreferences(cfg); len(prefs) > 0 {
		failoverManager.SetBasePreferences(prefs)
	}
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
	learningStore      learning.Store
	failoverHook       *failover.Hook
	failoverManager    *failover.Manager
	dispatcher         *external.Dispatcher
}

// runtimeComponents groups the runtime values created during engine setup.
type runtimeComponents struct {
	engine          *engine.Engine
	defaultProvider provider.Provider
	discovery       *discovery.AgentDiscovery
	streamer        streaming.Streamer
	apiServer       *api.Server
	metricsRegistry *prometheus.Registry
	compression     compressionComponents
	mcpManager      mcpclient.Client
	todoStore       todotool.Store
	sessionRecorder *sessionrecorder.Recorder
	setEnsureTools  func(func(agent.Manifest))
	sessionManager  *session.Manager
	// mcpServerTools is the MCP server → tool-name index built at engine
	// setup. Forwarded onto the App so createDelegateEngine can pass the
	// same map into every delegate engine; a delegate whose manifest opts
	// into an MCP server must see those tools, not silently receive zero
	// because the parent's index never reached the child engine.
	mcpServerTools map[string][]string
	// mcpTools holds the proxy tool implementations for each connected
	// MCP server. Forwarded so buildToolsForManifestWithStore can append
	// them to the delegate engine's tool slice; the engine's
	// buildAllowedToolSet then gates exposure by the delegate manifest's
	// MCPServers list.
	mcpTools []tool.Tool
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
	appEventBus := eventbus.NewEventBus()
	hookChain := buildCreateEngineHookChain(params, &eng, &ensureToolsFn, appEventBus)
	skillDir := params.skillDir
	appLevelSkillNames := params.appLevelSkillNames
	skillsResolver := func(m agent.Manifest) []skill.Skill {
		return engine.LoadAlwaysActiveSkills(
			skillDir, appLevelSkillNames, m.Capabilities.AlwaysActiveSkills,
		)
	}
	eng = engine.New(engine.Config{
		ChatProvider:              params.defaultProvider,
		EmbeddingProvider:         toEmbeddingProvider(params.ollamaProvider),
		Registry:                  params.providerRegistry,
		AgentRegistry:             params.agentRegistry,
		FailoverManager:           params.failoverManager,
		Manifest:                  params.defaultManifest,
		Skills:                    params.alwaysActiveSkills,
		SkillsResolver:            skillsResolver,
		Store:                     params.contextStore,
		ChainStore:                params.chainStore,
		HookChain:                 hookChain,
		Tools:                     params.appTools,
		ToolRegistry:              params.toolRegistry,
		PermissionHandler:         params.permissionHandler,
		AgentsFileLoader:          params.agentsFileLoader,
		TokenCounter:              params.tokenCounter,
		MCPServerTools:            params.mcpServerTools,
		EventBus:                  appEventBus,
		RecallBroker:              params.recallBroker,
		ContextAssemblyHooks:      params.contextAssemblyHooks,
		AutoCompactor:             params.compression.autoCompactor,
		CompressionConfig:         params.compression.config,
		CompressionMetrics:        params.compression.metrics,
		SessionMemoryStore:        params.compression.sessionMemoryStore,
		Recorder:                  params.compression.recorder,
		KnowledgeExtractorFactory: params.compression.knowledgeExtractorFactory,
		StreamTimeout:             params.streamTimeout,
		ToolTimeout:               params.toolTimeout,
	})
	setEnsureTools := func(fn func(agent.Manifest)) {
		ensureToolsFn = fn
	}
	return eng, setEnsureTools
}

// buildCreateEngineHookChain constructs the hook chain used when the engine is created.
//
// Expected:
//   - params contains the app dependencies required to wire hooks.
//   - eng points to the engine instance that will be created.
//   - ensureToolsFn points to the callback used to ensure tool registration.
//   - appEventBus is the event bus for failover event publishing.
//
// Returns:
//   - A hook chain configured for engine creation.
//
// Side effects:
//   - None.
func buildCreateEngineHookChain(
	params engineParams,
	eng **engine.Engine,
	ensureToolsFn *func(agent.Manifest),
	appEventBus *eventbus.EventBus,
) *hook.Chain {
	return buildHookChain(hookChainConfig{
		learningStore: params.learningStore,
		manifestGetter: func() agent.Manifest {
			if *eng != nil {
				return (*eng).Manifest()
			}
			return params.defaultManifest
		},
		bakedSkillNames: bakedSkillNamesFrom(params.alwaysActiveSkills),
		failoverHk:      params.failoverHook,
		failoverMgr:     params.failoverManager,
		dispatcher:      params.dispatcher,
		eventBus:        appEventBus,
		agentID:         params.defaultManifest.ID,
		skillDir:        params.skillDir,
		twc: &toolWiringCallbacks{
			hasTool: func(name string) bool {
				if *eng == nil {
					return false
				}
				return (*eng).HasTool(name)
			},
			ensureTools: func(m agent.Manifest) {
				if *ensureToolsFn != nil {
					(*ensureToolsFn)(m)
				}
			},
			schemaRebuilder: func() []provider.Tool {
				if *eng == nil {
					return nil
				}
				return (*eng).ToolSchemas()
			},
		},
	})
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

// buildDelegateMaps builds the engines map and streamers map for all registered agents
// except the one matching excludeID. Each engine's model preference is inherited from src.
//
// Expected:
//   - excludeID is the ID of the coordinating agent to skip.
//   - store is the shared coordination store.
//   - src is the coordinating engine supplying model preferences and event bus.
//
// Returns:
//   - A map of agent ID to isolated Engine instances.
//   - A map of agent ID to Streamer implementations (HarnessStreamer when applicable).
//
// Side effects:
//   - Creates isolated engine instances for each target agent.
func (a *App) buildDelegateMaps(
	excludeID string, store coordination.Store, src *engine.Engine,
) (map[string]*engine.Engine, map[string]streaming.Streamer) {
	allAgents := a.Registry.List()
	engines := make(map[string]*engine.Engine, len(allAgents))
	streamers := make(map[string]streaming.Streamer, len(allAgents))
	for _, agentManifest := range allAgents {
		if agentManifest.ID == excludeID {
			continue
		}
		targetEngine, str := a.createDelegateEngine(*agentManifest, store, src.EventBus())
		targetEngine.SetModelPreference(src.LastProvider(), src.LastModel())
		engines[agentManifest.ID] = targetEngine
		streamers[agentManifest.ID] = str
	}
	return engines, streamers
}

// wireDelegateToolIfEnabled adds a DelegateTool to the engine when the
// manifest permits delegation. Each target agent gets its own isolated engine
// instance to prevent state corruption during delegation.
//
// This function is idempotent: when the engine already has a DelegateTool
// (typical on the ensureTools callback path after a manifest switch, or when
// both setupEngine and buildApp run the wiring for the default manifest at
// startup) the existing tool is reconfigured for the new manifest instead of
// a duplicate being appended. Duplicates break provider tool schemas (OpenAI
// forbids two functions with the same name) and corrupt the allowed-set
// filter because the first *DelegateTool in the slice is the one that
// answers SetManifest, while a later copy is what gets filtered out.
//
// Expected:
//   - eng is a fully initialised Engine.
//   - manifest is the agent manifest to inspect for delegation configuration.
//
// Side effects:
//   - When manifest.Delegation.CanDelegate is true: appends DelegateTool /
//     BackgroundOutputTool / BackgroundCancelTool and optionally
//     CoordinationTool when they are not already present; rebinds an existing
//     DelegateTool to the new manifest's delegation and source agent when it
//     is already registered; creates isolated engine instances for each
//     delegation target.
//   - When manifest.Delegation.CanDelegate is false: idempotently removes
//     DelegateTool, BackgroundOutputTool, BackgroundCancelTool and
//     CoordinationTool if they were registered by a prior delegating
//     manifest. This enforces the delegate / suggest_delegate mutual
//     exclusion on manifest swap — Anthropic rejects a request that
//     advertises both with "400 Bad Request: tools: Tool names must be
//     unique".
func (a *App) wireDelegateToolIfEnabled(eng *engine.Engine, manifest agent.Manifest) {
	if !manifest.Delegation.CanDelegate {
		// Mirror of the non-delegating branch in
		// wireSuggestDelegateToolIfDisabled: when swapping from a
		// delegating manifest to a non-delegating one, the delegate
		// tool and its siblings (background_output, background_cancel)
		// are no longer meaningful and must be unregistered. Leaving
		// them in place produces a tool schema the active manifest is
		// not permitted to use and, when paired with a freshly-added
		// suggest_delegate, drives Anthropic to reject the request
		// with "tool names must be unique". RemoveTool is idempotent —
		// the no-op case on the non-delegate→non-delegate path is
		// safe.
		//
		// coordination_store is intentionally NOT in this remove list:
		// its lifecycle is governed solely by
		// manifest.Capabilities.Tools (per ADR §Decision 9), not by
		// CanDelegate. Non-delegating agents that opted in via the
		// manifest must keep the tool. wireCoordinationToolIfDeclared
		// handles the both-directions case from the canonical guard.
		eng.RemoveTool("delegate")
		eng.RemoveTool("background_output")
		eng.RemoveTool("background_cancel")
		a.wireCoordinationToolIfDeclared(eng, manifest, nil)
		return
	}

	if a.backgroundManager == nil {
		a.backgroundManager = engine.NewBackgroundTaskManager()
		a.backgroundManager.WithSessionManager(a.sessionManager)
	}
	bgManager := a.backgroundManager
	coordinationStore := wrapCoordinationStoreWithApproval(createCoordinationStore(a.Config), a)

	engines, streamers := a.buildDelegateMaps(manifest.ID, coordinationStore, eng)

	if !eng.HasTool("delegate") {
		delegateTool := engine.NewDelegateToolWithBackground(
			engines, manifest.Delegation, manifest.ID, bgManager, coordinationStore,
		).WithStreamers(streamers)
		a.configureDelegateTool(delegateTool)
		eng.AddTool(delegateTool)
	} else if dt, ok := eng.GetDelegateTool(); ok {
		// The engine already has a DelegateTool — likely wired at startup
		// for the default manifest. Refresh its bindings so the new
		// manifest's delegation allowlist, source agent, and per-target
		// engines take effect without leaking a stale duplicate tool into
		// the registry.
		dt.SetDelegation(manifest.Delegation)
		dt.SetSourceAgentID(manifest.ID)
		dt.WithStreamers(streamers)
		a.configureDelegateTool(dt)
	}

	if !eng.HasTool("background_output") {
		eng.AddTool(engine.NewBackgroundOutputTool(bgManager).
			WithDefaultTimeout(a.Config.ParsedBackgroundOutputTimeout()))
	}
	if !eng.HasTool("background_cancel") {
		eng.AddTool(engine.NewBackgroundCancelTool(bgManager))
	}

	a.wireCoordinationToolIfDeclared(eng, manifest, coordinationStore)
}

// wireCoordinationToolIfDeclared honours the manifest's coordination_store
// opt-in independently of CanDelegate. The tool is added when the manifest
// declares it and removed when it does not — both directions, so a
// manifest swap that drops the declaration cleans up.
//
// Expected:
//   - eng is non-nil.
//   - existingStore may be nil. The delegating branch passes its shared
//     store so coordinator and delegates see the same keys during a
//     chain. The non-delegating branch passes nil; a fresh store is
//     constructed via createCoordinationStore — file-backed paths are
//     deterministic so the two store instances refer to the same on-disk
//     file, and non-delegating agents do not participate in cross-agent
//     chains.
//
// Side effects:
//   - Adds or removes the coordination_store tool on eng based on the
//     manifest's capabilities.tools declaration.
func (a *App) wireCoordinationToolIfDeclared(
	eng *engine.Engine,
	manifest agent.Manifest,
	existingStore coordination.Store,
) {
	if !a.hasCoordinationTool(manifest.Capabilities.Tools) {
		eng.RemoveTool("coordination_store")
		return
	}
	if eng.HasTool("coordination_store") {
		return
	}
	store := existingStore
	if store == nil {
		store = wrapCoordinationStoreWithApproval(createCoordinationStore(a.Config), a)
	}
	eng.AddTool(coordinationtool.New(store))
}

// configureDelegateTool applies the App-level dependencies (registry,
// embedding discovery, category resolver, session manager integration,
// store factory, sessions directory) to a DelegateTool regardless of
// whether the tool was just constructed or is being rebound after a
// manifest switch. Extracted so both the "add" and "rebind" paths share a
// single source of truth and the rebind path cannot silently drift from
// the first-time wiring.
//
// Expected:
//   - dt is a non-nil DelegateTool that has already been constructed with a
//     valid engines map, manifest and background-task manager.
//
// Side effects:
//   - Mutates dt's injected dependencies in place.
func (a *App) configureDelegateTool(dt *engine.DelegateTool) {
	dt.WithRegistry(a.Registry)

	if embedder := a.resolveEmbedder(); embedder != nil {
		ed := discovery.NewEmbeddingDiscovery(a.Registry, embedder)
		dt.SetEmbeddingDiscovery(ed)
	}

	categoryRouting := map[string]engine.CategoryConfig{}
	if a.Config != nil {
		categoryRouting = a.Config.CategoryRouting
	}
	resolver := engine.NewCategoryResolver(categoryRouting).
		WithModelLister(a.ListModels)
	dt.WithCategoryResolver(resolver)

	if a.sessionManager != nil {
		dt.WithSessionCreator(a.sessionManager)
		dt.WithMessageAppender(a.sessionManager)
		dt.WithSessionManager(a.sessionManager)
	}

	if a.Config != nil {
		dt.WithStoreFactory(newDelegateStoreFactory(a.SessionsDir()))
		dt.WithSessionsDir(a.SessionsDir())
	}
}

// wireSuggestDelegateToolIfDisabled adds a SuggestDelegateTool to the engine
// when the manifest has can_delegate=false. This is the inverse of
// wireDelegateToolIfEnabled: a given agent is offered either delegate (can
// delegate) or suggest_delegate (cannot), never both. The tool returns a
// structured payload the chat UI renders as a "switch agent?" prompt, giving
// the model a legitimate escape hatch when the user references an @<agent>
// the current non-delegating agent cannot reach directly. See also P7 — the
// premature-delegation warning path remains active as a defence-in-depth
// signal for models that ignore this tool.
//
// Expected:
//   - eng is a fully initialised Engine.
//   - manifest is the agent manifest to inspect for delegation configuration.
//
// Side effects:
//   - When manifest.Delegation.CanDelegate is false: appends a
//     SuggestDelegateTool to the engine's tool set when one is not already
//     registered (idempotent across repeated invocations for the same
//     non-delegating manifest).
//   - When manifest.Delegation.CanDelegate is true: idempotently removes
//     any SuggestDelegateTool left over from a prior non-delegating
//     manifest. This enforces the delegate / suggest_delegate mutual
//     exclusion on manifest swap.
func (a *App) wireSuggestDelegateToolIfDisabled(eng *engine.Engine, manifest agent.Manifest) {
	if manifest.Delegation.CanDelegate {
		// Mirror of the delegating branch in
		// wireDelegateToolIfEnabled: when swapping from a
		// non-delegating manifest to a delegating one, any
		// previously-wired suggest_delegate must be unregistered.
		// Leaving it in place alongside delegate advertises two
		// delegation-shaped tools to the provider and Anthropic
		// rejects the request with "400 Bad Request: tools: Tool
		// names must be unique". RemoveTool is idempotent — the
		// no-op case on the delegate→delegate path is safe.
		eng.RemoveTool("suggest_delegate")
		return
	}
	if eng.HasTool("suggest_delegate") {
		// Idempotent: avoid accumulating duplicate suggest_delegate
		// entries when setup and buildApp both wire the default
		// manifest, or when ensureTools fires repeatedly for the same
		// non-delegating manifest. Duplicates corrupt the provider
		// tool schema the same way a stale suggest_delegate would.
		return
	}
	eng.AddTool(engine.NewSuggestDelegateTool(a.Registry, manifest.ID))
}

// createDelegateEngine creates an isolated engine instance for a delegation target.
// This ensures that when a coordinator delegates to another agent, the target's
// manifest and state do not corrupt the coordinator's state.
//
// Expected:
//   - manifest is the target agent's manifest with model preferences and capabilities.
//   - store is the shared coordination store for cross-agent communication.
//   - bus is the parent engine's event bus so delegate events are visible to the same subscribers.
//
// Returns:
//   - An Engine instance configured for the target agent.
//   - The engine does NOT have a delegate tool (prevents recursive delegation).
//
// Side effects:
//   - Creates a new engine with the target's manifest, providers, and tools.
func (a *App) createDelegateEngine(
	manifest agent.Manifest, store coordination.Store, bus *eventbus.EventBus,
) (*engine.Engine, streaming.Streamer) {
	delegateSkillDir, delegateLoadedSkills := a.resolveDelegateSkills(manifest)
	hookChain := buildHookChain(hookChainConfig{
		learningStore:   a.Learning,
		manifestGetter:  func() agent.Manifest { return manifest },
		bakedSkillNames: bakedSkillNamesFrom(delegateLoadedSkills),
		eventBus:        bus,
		agentID:         manifest.ID,
		skillDir:        delegateSkillDir,
	})

	var chainStore recall.ChainContextStore
	if a.Engine != nil {
		chainStore = a.Engine.ChainStore()
	}

	var childFailoverMgr *failover.Manager
	if a.plugins != nil && a.plugins.healthManager != nil {
		childFailoverMgr = failover.NewManager(a.providerRegistry, a.plugins.healthManager, 5*time.Minute)
		if a.plugins.failoverManager != nil {
			childFailoverMgr.SetBasePreferences(a.plugins.failoverManager.Preferences())
		}
	}

	delegateCompression := a.buildDelegateCompression(manifest)

	eng := engine.New(engine.Config{
		ChatProvider:              a.defaultProvider,
		Registry:                  a.providerRegistry,
		AgentRegistry:             a.Registry,
		Manifest:                  manifest,
		Skills:                    delegateLoadedSkills,
		Tools:                     a.buildToolsForManifestWithStore(manifest, store),
		HookChain:                 hookChain,
		ChainStore:                chainStore,
		EventBus:                  bus,
		FailoverManager:           childFailoverMgr,
		MCPServerTools:            a.mcpServerTools,
		AutoCompactor:             delegateCompression.autoCompactor,
		CompressionConfig:         delegateCompression.config,
		CompressionMetrics:        delegateCompression.metrics,
		SessionMemoryStore:        delegateCompression.sessionMemoryStore,
		Recorder:                  delegateCompression.recorder,
		KnowledgeExtractorFactory: delegateCompression.knowledgeExtractorFactory,
		StreamTimeout:             a.Config.ParsedStreamTimeout(),
		ToolTimeout:               a.Config.ParsedToolTimeout(),
	})
	var str streaming.Streamer = eng
	if manifest.HarnessEnabled && a.Config != nil {
		str = createHarnessStreamer(eng, a.Registry, a.Config.Harness, a.defaultProvider, a.Config.DefaultProviderModel())
	}
	return eng, str
}

// resolveDelegateSkills returns the skill directory and the resolved
// default-active skill slice a delegate engine should receive. Mirrors
// createEngine's root-engine wiring (see app.go:828): without this,
// delegate engines silently drop manifest-declared default-active skills
// — the session's loaded_skills omits them and the harness validator
// flags the regression. See the Obsidian note
// "Delegate Engine Skills Silent Drop (April 2026)" for the predecessor
// root-engine fix this extends.
//
// Expected:
//   - manifest is the delegate agent's manifest; its
//     Capabilities.AlwaysActiveSkills is merged with the app-level list.
//   - a.Config may be nil (some test harnesses wire delegates without a
//     full Config); the returned slice is then empty and the skill dir
//     is the empty string.
//
// Returns:
//   - The skill directory used to locate skill files.
//   - The slice of resolved skill.Skill values, or nil when none match.
//
// Side effects:
//   - Reads skill files from disk via engine.LoadAlwaysActiveSkills.
func (a *App) resolveDelegateSkills(manifest agent.Manifest) (string, []skill.Skill) {
	var (
		skillDir  string
		appSkills []string
	)
	if a.Config != nil {
		skillDir = a.Config.SkillDir
		appSkills = a.Config.AlwaysActiveSkills
	}
	return skillDir, engine.LoadAlwaysActiveSkills(
		skillDir, appSkills, manifest.Capabilities.AlwaysActiveSkills,
	)
}

// buildDelegateCompression assembles compression components for a delegate
// engine. It shares the parent App's CompressionConfig, metrics, recorder,
// and SessionMemoryStore (so counters aggregate across delegates and L3
// entries land in one store), but constructs a per-delegate summariser
// adapter bound to the delegate's manifest. Without the rebind every
// delegate would resolve its summary tier against the coordinator's
// manifest, silently collapsing the category routing to a single tier.
//
// Expected:
//   - a.compression has been populated by setupEngine. When compression
//     is disabled (zero autoCompactor and nil store) the returned bundle
//     is a zero-effort pass-through.
//   - manifest is the delegate's manifest. Its ContextManagement.SummaryTier
//     picks the routing category at Summarise time.
//
// Returns:
//   - A compressionComponents value with a fresh summariser adapter and
//     AutoCompactor when L2 is enabled; otherwise a disabled bundle that
//     reuses the parent's metrics and recorder unchanged.
//
// Side effects:
//   - None.
func (a *App) buildDelegateCompression(manifest agent.Manifest) compressionComponents {
	out := compressionComponents{
		config:                    a.compression.config,
		metrics:                   a.compression.metrics,
		sessionMemoryStore:        a.compression.sessionMemoryStore,
		recorder:                  a.compression.recorder,
		knowledgeExtractorFactory: a.compression.knowledgeExtractorFactory,
	}

	if !a.compression.config.AutoCompaction.Enabled {
		return out
	}
	if a.Config == nil {
		return out
	}

	categoryResolver := engine.NewCategoryResolver(a.Config.CategoryRouting)
	summariserResolver := engine.NewSummariserResolver(categoryResolver)
	fallbackModel := a.Config.Providers.Ollama.Model
	adapter := engine.NewProviderSummariser(a.defaultProvider, summariserResolver, fallbackModel).
		WithManifest(&manifest)
	out.summariserAdapter = adapter
	out.autoCompactor = ctxstore.NewAutoCompactor(adapter)
	return out
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

	if a.Engine != nil {
		contextStore := a.Engine.ContextStore()
		if contextStore != nil && a.ollamaProvider != nil {
			tokenCounter := ctxstore.NewTiktokenCounter()
			factory := recall.NewToolFactory(
				contextStore, a.ollamaProvider, tokenCounter,
				a.Config.Providers.Ollama.Model, a.Engine.EventBus(),
			)
			tools = append(tools, factory.ToolsWithChainStore(a.Engine.ChainStore())...)
		}
	}

	// Append the MCP proxy tools so the delegate engine has something to
	// invoke when its manifest's MCPServers gate authorises a name.
	// buildAllowedToolSet (in the engine) is the single point of truth
	// that filters by the manifest's MCPServers allowlist; appending the
	// full set here is safe.
	if len(a.mcpTools) > 0 {
		tools = append(tools, a.mcpTools...)
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

	planFile := buildPersistedPlanFile(chainID, string(planData))

	if err := a.Store.Create(planFile); err != nil {
		return fmt.Errorf("persisting plan: %w", err)
	}

	return nil
}

// buildPersistedPlanFile constructs the plan.File the auto-persist path
// writes to disk. Mirrors the agent-driven plan_write tool's parser path
// (plan.ParseFile + plan.TasksFromPlanText) so the on-disk file looks the
// same regardless of which entry point produced it. Without re-parsing,
// the older code dumped the raw markdown into TLDR — the persisted file
// then had nested frontmatter (outer from this builder, inner from the
// original payload) and the structured task list was lost.
//
// Falls back gracefully when the markdown is missing frontmatter: the
// plan still lands on disk with chainID-derived defaults so the auto-
// persist path never silently drops an approved plan, just renders it
// less richly. Tasks are extracted independently of the frontmatter
// parse via TasksFromPlanText, which scans the markdown body — so even
// malformed frontmatter still yields a non-empty Tasks slice when the
// "## Tasks" / "### Task N:" sections are well-formed.
func buildPersistedPlanFile(chainID, planMarkdown string) plan.File {
	now := time.Now().UTC()
	defaults := plan.File{
		ID:        chainID,
		Title:     "Plan " + chainID,
		Status:    "approved",
		CreatedAt: now,
	}

	parsed, err := plan.ParseFile(planMarkdown)
	if err != nil || parsed == nil {
		// Frontmatter-less or malformed payload: fall back to defaults
		// but keep TLDR pointing at the raw markdown so the operator
		// can still read the original plan body. Tasks parser still
		// runs against the raw markdown — see below.
		defaults.TLDR = planMarkdown
		defaults.Tasks = plan.TasksFromPlanText(planMarkdown)
		return defaults
	}

	// Promote the parsed frontmatter onto the persisted File. Use the
	// chainID-derived defaults only when the source omitted the field.
	if strings.TrimSpace(parsed.ID) == "" {
		parsed.ID = defaults.ID
	}
	if strings.TrimSpace(parsed.Title) == "" {
		parsed.Title = defaults.Title
	}
	parsed.Status = "approved"
	if parsed.CreatedAt.IsZero() {
		parsed.CreatedAt = now
	}
	parsed.Tasks = plan.TasksFromPlanText(planMarkdown)
	return *parsed
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

// restorePersistedSessions loads session metadata from disk and registers the
// sessions into the session manager so that child sessions created before the
// last restart are available after restart.
//
// Expected:
//   - a.sessionManager is non-nil.
//   - a.Config is non-nil and provides DataDir.
//
// Returns:
//   - None.
//
// Side effects:
//   - Reads .meta.json files from SessionsDir and calls RestoreSessions.
func (a *App) restorePersistedSessions() {
	if a.sessionManager == nil {
		return
	}
	restored, err := session.LoadSessionsFromDirectory(a.SessionsDir())
	if err != nil {
		return
	}
	a.sessionManager.RestoreSessions(restored)
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
//   - plansDir is the directory containing FlowState plan markdown files
//     (typically ${DataDir}/plans). Passed to the plan_list and plan_read
//     tools so harness agents can enumerate and read plans in-process
//     without delegating filesystem searches that may look in the wrong
//     directory.
//
// Returns:
//   - A slice containing bash, file, web, skill_load, todowrite, plan_list,
//     and plan_read tools.
//
// Side effects:
//   - Initialises new tool instances.
func buildTools(skillLoader *skill.FileSkillLoader, todoStore todotool.Store, plansDir string) []tool.Tool {
	return []tool.Tool{
		bash.New(),
		read.New(),
		write.New(),
		web.New(),
		skilltool.New(skillLoader),
		todotool.New(todoStore),
		plantool.NewList(plansDir),
		plantool.NewRead(plansDir),
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
//   - A slice of MCPConnectionResult describing each connection attempt.
//   - A map from server name to the names of tools it exposes, for use by the engine
//     when resolving Capabilities.MCPServers declarations.
//
// Side effects:
//   - Connects to MCP servers via the client.
//   - Logs warnings for connection or tool listing failures.
func ConnectMCPServers(
	ctx context.Context,
	client mcpclient.Client,
	servers []config.MCPServerConfig,
) ([]tool.Tool, []MCPConnectionResult, map[string][]string) {
	var tools []tool.Tool
	var results []MCPConnectionResult
	serverToolNames := make(map[string][]string)
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
			results = append(results, MCPConnectionResult{
				Name:      serverCfg.Name,
				Success:   false,
				Error:     err.Error(),
				ToolCount: 0,
			})
			continue
		}
		serverTools, err := client.ListTools(ctx, serverCfg.Name)
		if err != nil {
			log.Printf("warning: MCP server %q ListTools failed: %v", serverCfg.Name, err)
			results = append(results, MCPConnectionResult{
				Name:      serverCfg.Name,
				Success:   false,
				Error:     err.Error(),
				ToolCount: 0,
			})
			continue
		}
		names := make([]string, 0, len(serverTools))
		for _, t := range serverTools {
			tools = append(tools, mcpproxy.NewProxy(client, serverCfg.Name, t))
			names = append(names, t.Name)
		}
		serverToolNames[serverCfg.Name] = names
		results = append(results, MCPConnectionResult{
			Name:      serverCfg.Name,
			Success:   true,
			Error:     "",
			ToolCount: len(serverTools),
		})
	}
	return tools, results, serverToolNames
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

// bakedSkillNamesFrom extracts the Name field from each skill in the
// slice. Used by buildCreateEngineHookChain and createDelegateEngine to
// forward the baked skill list into the SkillAutoLoader hook so it does
// not re-inject skills that are already in the system prompt.
//
// Expected:
//   - skills may be nil or empty; the returned slice is the same length.
//
// Returns:
//   - A slice of skill names in the same order as the input.
//
// Side effects:
//   - None.
func bakedSkillNamesFrom(skills []skill.Skill) []string {
	names := make([]string, 0, len(skills))
	for i := range skills {
		names = append(names, skills[i].Name)
	}
	return names
}

// toolWiringCallbacks groups the callbacks required to lazily wire delegation
// tools at request time via ToolWiringHook.
type toolWiringCallbacks struct {
	hasTool         func(string) bool
	ensureTools     func(agent.Manifest)
	schemaRebuilder func() []provider.Tool
}

// hookChainConfig groups parameters for buildHookChain to stay within the argument-limit.
type hookChainConfig struct {
	learningStore   learning.Store
	manifestGetter  func() agent.Manifest
	bakedSkillNames []string
	failoverHk      *failover.Hook
	failoverMgr     *failover.Manager
	twc             *toolWiringCallbacks
	dispatcher      *external.Dispatcher
	eventBus        *eventbus.EventBus
	agentID         string
	skillDir        string
}

// buildHookChain constructs a hook chain with logging, learning, and skill auto-loading hooks.
// When failoverMgr is non-nil, a StreamHook is appended LAST so provider failover wraps
// the base handler. When only failoverHk is set (legacy path), it is prepended first.
//
// Expected:
//   - params.learningStore may be nil or a Mem0LearningStore for persisting learning data.
//     If nil, learning is disabled. Mem0LearningStore provides in-memory persistence without external dependencies.
//   - params.manifestGetter returns the current agent manifest for skill selection.
//   - params.bakedSkillNames are skill names already baked into BuildSystemPrompt. May be nil.
//   - params.failoverHk may be nil; when non-nil and failoverMgr is nil, it is prepended.
//   - params.failoverMgr may be nil; when non-nil, a StreamHook is appended LAST.
//   - params.twc may be nil; when non-nil, a ToolWiringHook is appended after SkillAutoLoader.
//   - params.skillDir may be empty; when non-empty, a SkillContentCache is initialised and
//     passed to SkillAutoLoaderHook for direct content injection.
//
// Returns:
//   - A fully configured hook.Chain ready for use in the engine.
//
// Side effects:
//   - Reads skill-autoloader.yaml from the config directory if it exists.
//   - Scans params.skillDir to populate the skill content cache when non-empty.
func buildHookChain(params hookChainConfig) *hook.Chain {
	cfg, err := hook.LoadSkillAutoLoaderConfig(filepath.Join(config.Dir(), "skill-autoloader.yaml"))
	if err != nil {
		cfg = hook.DefaultSkillAutoLoaderConfig()
	}

	var skillCache *hook.SkillContentCache
	if params.skillDir != "" {
		skillCache = hook.NewSkillContentCache(params.skillDir)
		if err := skillCache.Init(); err != nil {
			slog.Warn("skill content cache init failed", "error", err)
			skillCache = nil
		}
	}

	hooks := []hook.Hook{
		hook.LoggingHook(),
	}
	if params.learningStore != nil {
		hooks = append(hooks, hook.LearningHook(params.learningStore))
	}
	hooks = append(hooks,
		hook.SkillAutoLoaderHook(cfg, params.manifestGetter, params.bakedSkillNames, skillCache),
	)
	if params.dispatcher != nil {
		d := params.dispatcher
		hooks = append(hooks, func(next hook.HandlerFunc) hook.HandlerFunc {
			return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				if err := d.Dispatch(ctx, pluginpkg.ChatParams, req); err != nil {
					slog.Warn("chat.params dispatch error", "error", err)
				}
				return next(ctx, req)
			}
		})
	}
	if params.twc != nil {
		twc := params.twc
		hooks = append(hooks, hook.ToolWiringHook(params.manifestGetter, twc.hasTool, twc.ensureTools, twc.schemaRebuilder))
	}
	projectRoot, err := os.Getwd()
	if err != nil {
		projectRoot = "."
	}
	hooks = append(hooks,
		hook.PhaseDetectorHook(params.manifestGetter),
		hook.ContextInjectionHook(params.manifestGetter, projectRoot),
		tracer.Hook(),
	)
	if params.failoverMgr != nil {
		streamHook := failover.NewStreamHook(params.failoverMgr, params.eventBus, params.agentID)
		hooks = append(hooks, streamHook.Execute)
	} else if params.failoverHk != nil {
		hooks = append([]hook.Hook{failoverHookAdapter(params.failoverHk)}, hooks...)
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

// pluginDispatcher returns the external dispatcher from the plugin runtime, or nil
// when the runtime is not initialised.
//
// Expected:
//   - rt may be nil; when nil the function returns nil.
//
// Returns:
//   - The external dispatcher, or nil when rt is nil.
//
// Side effects:
//   - None.
func pluginDispatcher(rt *pluginRuntime) *external.Dispatcher {
	if rt == nil {
		return nil
	}
	return rt.dispatcher
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
func startCorePluginSubscriptions(rt *pluginRuntime, eng *engine.Engine, distiller learning.Distiller, mcpClient mcpclient.Client) {
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
	subscribeLearningHook(bus, distiller, mcpClient)
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
//   - Subscribes to "plugin.event", "tool.execute.before", "tool.execute.result", and "tool.execute.error" events on the bus.
//   - Logs warnings if plugin hook dispatch errors occur.
func subscribeDispatcherHooks(dispatcher *external.Dispatcher, bus *eventbus.EventBus) {
	bus.Subscribe(events.EventPluginEvent, func(msg any) {
		if evt, ok := msg.(pluginpkg.Event); ok {
			if err := dispatcher.Dispatch(context.Background(), pluginpkg.EventType, evt); err != nil {
				slog.Warn("plugin hook dispatch error", "hook", "event", "error", err)
			}
		}
	})
	bus.Subscribe(events.EventToolExecuteBefore, func(msg any) {
		if toolEvt, ok := msg.(*events.ToolEvent); ok {
			args := &external.ToolExecArgs{
				Name: toolEvt.Data.ToolName,
				Args: toolEvt.Data.Args,
			}
			slog.Info("dispatcher: tool hook activated", "hook", "tool.execute.before", "tool", toolEvt.Data.ToolName)
			if err := dispatcher.Dispatch(context.Background(), pluginpkg.ToolExecBefore, args); err != nil {
				slog.Warn("plugin hook dispatch error", "hook", "tool.execute.before", "error", err)
			}
		}
	})
	bus.Subscribe(events.EventToolExecuteResult, func(msg any) {
		if toolEvt, ok := msg.(*events.ToolExecuteResultEvent); ok {
			args := &external.ToolExecArgs{
				Name: toolEvt.Data.ToolName,
				Args: toolEvt.Data.Args,
			}
			slog.Info("dispatcher: tool hook activated", "hook", "tool.execute.result", "tool", toolEvt.Data.ToolName)
			if err := dispatcher.Dispatch(context.Background(), pluginpkg.ToolExecAfter, args); err != nil {
				slog.Warn("plugin hook dispatch error", "hook", "tool.execute.result", "error", err)
			}
		}
	})
	bus.Subscribe(events.EventToolExecuteError, func(msg any) {
		if toolEvt, ok := msg.(*events.ToolExecuteErrorEvent); ok {
			args := &external.ToolExecArgs{
				Name: toolEvt.Data.ToolName,
				Args: toolEvt.Data.Args,
			}
			slog.Info("dispatcher: tool hook activated", "hook", "tool.execute.error", "tool", toolEvt.Data.ToolName)
			if err := dispatcher.Dispatch(context.Background(), pluginpkg.ToolExecAfter, args); err != nil {
				slog.Warn("plugin hook dispatch error", "hook", "tool.execute.error", "error", err)
			}
		}
	})
}

// subscribeLearningHook subscribes the learning hook to tool execute result events.
// This enables learning records to be captured when tools are executed.
//
// Expected:
//   - bus is a valid EventBus instance.
//   - distiller is a valid learning.Distiller.
//   - mcpClient is a valid mcpclient.Client for memory operations.
//
// Returns:
//   - None.
//
// Side effects:
//   - Subscribes a handler to "tool.execute.result" events on the bus.
func subscribeLearningHook(bus *eventbus.EventBus, distiller learning.Distiller, mcpClient mcpclient.Client) {
	memClient := learning.NewMCPMemoryClient(mcpClient, "memory")
	learningHook := learning.NewLearningHook(memClient)
	bus.Subscribe(events.EventToolExecuteResult, func(msg any) {
		handleToolExecuteResult(msg, learningHook, distiller)
	})
}

// handleToolExecuteResult processes a tool execute result event for learning capture.
//
// Expected:
//   - msg is the raw event message from the event bus.
//   - hook is a non-nil learning.Hook for capturing tool results.
//   - distiller may be nil; when non-nil it distils the entry into the knowledge graph.
//
// Returns:
//   - None.
//
// Side effects:
//   - Calls hook.Handle and optionally distiller.Distill; logs warnings on error.
func handleToolExecuteResult(msg any, learningHk *learning.Hook, distiller learning.Distiller) {
	toolEvt, ok := msg.(*events.ToolExecuteResultEvent)
	if !ok {
		return
	}
	result := &learning.ToolCallResult{
		Outcome: fmt.Sprintf("%s:%s", toolEvt.Data.ToolName, toolEvt.Data.Result),
	}
	if err := learningHk.Handle(context.Background(), result); err != nil {
		slog.Warn("learning hook error", "error", err)
	}
	if distiller == nil {
		return
	}
	entry := learning.Entry{
		Timestamp: toolEvt.Timestamp(),
		AgentID:   toolEvt.Data.SessionID,
		ToolsUsed: []string{toolEvt.Data.ToolName},
		Outcome:   fmt.Sprintf("%v", toolEvt.Data.Result),
	}
	if _, _, err := distiller.Distill(entry); err != nil {
		slog.Warn("distiller error", "error", err)
	}
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
	bus.Subscribe(events.EventProviderRateLimited, func(msg any) {
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

// sessionsDirFromCfg mirrors App.SessionsDir but works on a bare
// AppConfig so wireSessionRecorder can compute the recording-dir
// derivation before the App instance is assembled.
//
// Expected:
//   - cfg may be nil.
//
// Returns:
//   - filepath.Join(cfg.DataDir, "sessions") when cfg and DataDir are set.
//   - Empty string otherwise.
//
// Side effects:
//   - None.
func sessionsDirFromCfg(cfg *config.AppConfig) string {
	if cfg == nil || cfg.DataDir == "" {
		return ""
	}
	return filepath.Join(cfg.DataDir, "sessions")
}

// defaultSessionRecordingDir returns the user-cache-dir fallback path
// for session recordings. Callers should prefer resolveSessionRecordingDir
// which honours the configured sessions-dir first; the fallback exists
// only for embedded/test paths that have no sessions directory at all.
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

// resolveSessionRecordingDir picks the effective session-recording
// directory by applying the documented precedence chain:
//
//  1. cfg.SessionRecordingDir when non-empty (explicit YAML override)
//  2. <sessionsDir>/recordings when sessionsDir is non-empty (derived
//     from --sessions-dir or cfg.DataDir/sessions)
//  3. defaultSessionRecordingDir fallback under os.UserCacheDir
//
// The derived option is the Item 1 fix: test runs passing an isolated
// --sessions-dir kept writing into the real ~/.cache because the
// recorder ignored the flag. Routing recordings under sessions-dir
// gives one mental model (one flag, one tree) and keeps test isolation
// intact.
//
// Expected:
//   - cfg may be nil; nil is treated as "no YAML override".
//   - sessionsDir may be empty; that path takes the cache-dir fallback.
//
// Returns:
//   - The resolved absolute or relative path.
//
// Side effects:
//   - None.
func resolveSessionRecordingDir(cfg *config.AppConfig, sessionsDir string) string {
	if cfg != nil && cfg.SessionRecordingDir != "" {
		return cfg.SessionRecordingDir
	}
	if sessionsDir != "" {
		return filepath.Join(sessionsDir, "recordings")
	}
	return defaultSessionRecordingDir()
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
func wireSessionRecorder(cfg *config.AppConfig, sessionMgr *session.Manager, sessionsDir string) *sessionrecorder.Recorder {
	if cfg == nil || !cfg.SessionRecording {
		return nil
	}

	dir := resolveSessionRecordingDir(cfg, sessionsDir)
	recorder := sessionrecorder.New(dir)
	if err := recorder.Init(); err != nil {
		log.Printf("warning: initialising session recorder: %v", err)
		return nil
	}

	sessionMgr.SetRecorder(recorder)
	log.Printf("info: session recording enabled, writing to %s", dir)
	return recorder
}

// wireSessionStatusSync subscribes the session manager to the engine's
// event bus so any session.ended event automatically flips the matching
// session's Status field to StatusCompleted. Closes the gap reported by
// users as "delegated agent status doesn't match reality" — without
// this hook, Status only ever updated when CloseSession was called
// explicitly, so async chain ends left listed sessions stuck on
// "active". The subscription is idempotent and respects status
// precedence (failed > completed > active) inside Manager itself.
//
// Expected:
//   - bus may be nil; when nil the wiring no-ops and the legacy
//     CloseSession-only behaviour is preserved.
//   - sessionMgr may be nil; same no-op semantics.
//
// Side effects:
//   - Registers a single handler on the bus for "session.ended". The
//     handler type-asserts the published event to *events.SessionEvent
//     so the session package does not need to import plugin/events.
func wireSessionStatusSync(bus *eventbus.EventBus, sessionMgr *session.Manager) {
	if bus == nil || sessionMgr == nil {
		return
	}
	bus.Subscribe(events.EventSessionEnded, func(payload any) {
		ev, ok := payload.(*events.SessionEvent)
		if !ok || ev == nil {
			return
		}
		sessionMgr.MarkEndedFromEvent(ev.Data.SessionID)
	})
}

// tracedBundle groups the tracing pieces produced by buildTracedProvider.
// Keeping recorder and registry together is essential: compression
// counters must surface on the same /metrics registry that exposes
// provider-latency, so callers need both references.
type tracedBundle struct {
	metrics  *prometheus.Registry
	recorder tracer.Recorder
	provider *tracer.TracingProvider
}

// buildTracedProvider creates a Prometheus metrics registry, wraps the default
// provider with a TracingProvider that records per-method latency, and returns
// the pieces required by the application container.
//
// Expected:
//   - providerRegistry is a non-nil provider.Registry with registered providers.
//   - defaultName is the name of the default provider to retrieve.
//
// Returns:
//   - A tracedBundle containing the prometheus registry, the shared
//     tracer.Recorder, and a TracingProvider wrapping the default
//     provider. The recorder is exposed so compression counters
//     (RecordContextWindowTokens, RecordCompressionTokensSaved) surface
//     through the same /metrics handler used for provider latency.
//   - An error if the default provider cannot be found.
//
// Side effects:
//   - Registers Prometheus collectors with the metrics registry.
func buildTracedProvider(
	providerRegistry *provider.Registry,
	defaultName string,
) (tracedBundle, error) {
	metricsReg := prometheus.NewRegistry()
	recorder := tracer.NewPrometheusRecorder(metricsReg)
	defaultProvider, err := providerRegistry.Get(defaultName)
	if err != nil {
		return tracedBundle{}, fmt.Errorf("getting default provider %q: %w", defaultName, err)
	}
	return tracedBundle{
		metrics:  metricsReg,
		recorder: recorder,
		provider: tracer.NewTracingProvider(defaultProvider, recorder),
	}, nil
}

// buildCompressionComponents assembles the dependencies required to
// activate the three-layer context compression system. Each layer is
// gated on the corresponding Enabled flag in cfg.Compression so that
// deployments opt in via YAML rather than by code change. A fully
// disabled CompressionConfig produces a zero-value bundle except for
// the recorder and metrics fields, which are always populated because
// the engine hot path reads them unconditionally.
//
// Expected:
//   - cfg is a non-nil AppConfig whose Compression block has already
//     been populated by the config loader (tilde expansion, defaults).
//   - agentRegistry is consulted for CategoryRouting when constructing
//     the summariser resolver.
//   - chatProvider is the provider the L2 summariser will call; reusing
//     the engine's primary provider keeps the runtime to a single chat
//     client per brief.
//   - recorder is the shared tracer.Recorder so compression counters
//     surface on the same /metrics registry as provider-latency metrics.
//
// Returns:
//   - A fully wired compressionComponents value. AutoCompactor and
//     SessionMemoryStore are nil when their respective Enabled flag is
//     false; every other field is always populated.
//
// Side effects:
//   - None at this layer. SessionMemoryStore writes lazily on Save.
func buildCompressionComponents(
	cfg *config.AppConfig,
	_ *agent.Registry,
	chatProvider provider.Provider,
	recorder tracer.Recorder,
) compressionComponents {
	out := compressionComponents{
		config:   cfg.Compression,
		metrics:  &ctxstore.CompressionMetrics{},
		recorder: recorder,
	}

	if cfg.Compression.AutoCompaction.Enabled {
		categoryResolver := engine.NewCategoryResolver(cfg.CategoryRouting)
		summariserResolver := engine.NewSummariserResolver(categoryResolver)
		fallbackModel := cfg.Providers.Ollama.Model
		adapter := engine.NewProviderSummariser(chatProvider, summariserResolver, fallbackModel)
		out.summariserAdapter = adapter
		out.autoCompactor = ctxstore.NewAutoCompactor(adapter)
	}

	if cfg.Compression.SessionMemory.Enabled {
		out.sessionMemoryStore = recall.NewSessionMemoryStore(cfg.Compression.SessionMemory.StorageDir)
		// Bind a per-session extractor factory so each Stream invocation
		// writes its distilled knowledge under the session actually being
		// streamed, rather than a shared static ID. The store itself is
		// shared across invocations so cross-session recall still works
		// (retrieval is content-typed, not session-scoped).
		store := out.sessionMemoryStore
		// M6 — the chat model is taken from the explicit
		// `compression.session_memory.model` config key. Config
		// validation rejects an empty or whitespace-only value at
		// load, so by the time we reach this branch the model is
		// guaranteed non-empty. The previous implementation tried to
		// fall back to provider defaults (Ollama > OpenAI > Anthropic)
		// via a switch on `cfg.Providers.Default`; that fallback only
		// covered three providers and left custom providers silently
		// broken with an HTTP 400 at first extraction. Requiring an
		// explicit model removes the guesswork.
		model := cfg.Compression.SessionMemory.Model
		out.knowledgeExtractorFactory = func(sessionID string) *recall.KnowledgeExtractor {
			if sessionID == "" {
				sessionID = "default"
			}
			return recall.NewKnowledgeExtractor(chatProvider, store, sessionID).WithModel(model)
		}
	}

	return out
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
	registry, ollamaProv, _ := setupProvidersWithFailures(cfg)
	return registry, ollamaProv
}

// errOpenAINoKey is returned when OpenAI has no API key from any source. It is
// defined so tests and the error-surface helper can match it programmatically
// without coupling to a specific log-message string.
var errOpenAINoKey = errors.New(
	"no API key (set OPENAI_API_KEY or providers.openai.api_key)",
)

// setupProvidersWithFailures initialises providers and also reports why any of
// them failed to register. The failures map is keyed by provider name and
// contains the underlying error returned by the provider constructor (or a
// synthetic "no API key" error for OpenAI which is skipped when no key is set).
//
// Expected:
//   - cfg is a non-nil AppConfig with provider configuration.
//
// Returns:
//   - A provider.Registry containing all successfully initialised providers.
//   - The Ollama provider instance (may be nil if initialisation failed).
//   - A map of provider-name to constructor error for each failed provider.
//
// Side effects:
//   - Reads OPENAI_API_KEY, ANTHROPIC_API_KEY, GITHUB_TOKEN, ZAI_API_KEY,
//     OPENZEN_API_KEY environment variables.
//   - Logs a warning for each provider that fails to register.
//   - Registers providers with the registry if initialisation succeeds.
func setupProvidersWithFailures(
	cfg *config.AppConfig,
) (*provider.Registry, *ollama.Provider, map[string]error) {
	providerRegistry := provider.NewRegistry()
	failures := make(map[string]error)

	ollamaProvider, ollamaErr := ollama.New(cfg.Providers.Ollama.Host)
	recordProvider(providerRegistry, failures, "ollama", ollamaProvider, ollamaErr)

	openaiProvider, openaiErr := buildOpenAIProvider(cfg)
	recordProvider(providerRegistry, failures, "openai", openaiProvider, openaiErr)

	anthropicKey := resolveProviderKey("ANTHROPIC_API_KEY", cfg.Providers.Anthropic.APIKey)
	anthropicProvider, anthropicErr := anthropic.NewFromConfig(anthropicKey)
	recordProvider(providerRegistry, failures, "anthropic", anthropicProvider, anthropicErr)

	githubToken := resolveProviderKey("GITHUB_TOKEN", cfg.Providers.GitHub.APIKey)
	copilotProvider, copilotErr := copilot.NewFromConfig(nil, githubToken)
	recordProvider(providerRegistry, failures, "copilot", copilotProvider, copilotErr)

	zaiKey := resolveProviderKey("ZAI_API_KEY", cfg.Providers.ZAI.APIKey)
	zaiProvider, zaiErr := zai.NewFromConfig(zaiKey, zaiPlanFromConfig(cfg))
	recordProvider(providerRegistry, failures, "zai", zaiProvider, zaiErr)

	openzenKey := resolveProviderKey("OPENZEN_API_KEY", cfg.Providers.OpenZen.APIKey)
	openzenProvider, openzenErr := openzen.NewFromConfig(openzenKey)
	recordProvider(providerRegistry, failures, "openzen", openzenProvider, openzenErr)

	warnIfOpenCodeAuthPresent(failures)

	return providerRegistry, ollamaProvider, failures
}

// zaiAllProvidersFailed reports whether every authenticated provider in the
// failures map is failing — i.e. nothing was successfully registered for any
// authenticated provider.
//
// We use this to decide whether to emit the OpenCode-migration WARN.
//
// Expected:
//   - failures is the per-provider error map from setupProvidersWithFailures.
//
// Returns:
//   - true when every authenticated provider failed.
//   - false when at least one authenticated provider succeeded.
//
// Side effects:
//   - None.
func zaiAllProvidersFailed(failures map[string]error) bool {
	authProviders := []string{"anthropic", "copilot", "zai", "openzen"}
	for _, name := range authProviders {
		if _, failed := failures[name]; !failed {
			return false
		}
	}
	return true
}

// warnIfOpenCodeAuthPresent emits a one-time WARN when the user appears to
// have an OpenCode auth.json on disk and no FlowState provider authenticated
// successfully. The OpenCode credential bridge has been removed, so the user
// must paste keys into config.yaml or run `flowstate auth <provider>`.
//
// Expected:
//   - failures is the per-provider error map from setupProvidersWithFailures.
//
// Side effects:
//   - Reads ~/.local/share/opencode/auth.json metadata.
//   - Logs a single WARN message when the migration hint applies.
func warnIfOpenCodeAuthPresent(failures map[string]error) {
	if !zaiAllProvidersFailed(failures) {
		return
	}
	homeDir, err := os.UserHomeDir()
	if err != nil || homeDir == "" {
		return
	}
	opencodePath := filepath.Join(homeDir, ".local", "share", "opencode", "auth.json")
	if _, err := os.Stat(opencodePath); err != nil {
		return
	}
	slog.Warn(
		"detected OpenCode auth.json but no FlowState provider authenticated; "+
			"FlowState no longer reads OpenCode credentials. "+
			"Run `flowstate auth anthropic` / `flowstate auth github-copilot` "+
			"or set provider keys directly in ~/.config/flowstate/config.yaml.",
		"opencode_auth_path", opencodePath,
	)
}

// zaiPlanFromConfig returns the Z.AI plan tag for the configured provider.
// "coding" selects the coding-plan subscription endpoint; anything else
// (including empty) selects the general pay-per-token endpoint.
//
// The plan is encoded in providers.zai.host: when host equals the
// coding-plan URL we treat it as the coding plan. This avoids adding a new
// config field while still routing keys to the correct base URL.
//
// Expected:
//   - cfg is a non-nil AppConfig.
//
// Returns:
//   - "coding" when the configured host matches the coding-plan endpoint.
//   - "" otherwise.
//
// Side effects:
//   - None.
func zaiPlanFromConfig(cfg *config.AppConfig) string {
	if cfg.Providers.ZAI.Host == "https://api.z.ai/api/coding/paas/v4" {
		return zai.PlanCoding
	}
	return ""
}

// resolveProviderKey returns the value of envVar if set, otherwise the
// fallback from application configuration.
//
// Expected:
//   - envVar is a non-empty environment variable name.
//
// Returns:
//   - The environment variable value, or cfgValue if the variable is unset.
//
// Side effects:
//   - Reads the given environment variable.
func resolveProviderKey(envVar, cfgValue string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	return cfgValue
}

// buildOpenAIProvider constructs the OpenAI provider from the configured key,
// returning errOpenAINoKey when no key is available so the caller records a
// uniform failure message.
//
// Expected:
//   - cfg is a non-nil AppConfig with OpenAI provider configuration.
//
// Returns:
//   - The constructed provider and nil on success.
//   - A nil provider and the constructor error, or errOpenAINoKey if no key.
//
// Side effects:
//   - Reads the OPENAI_API_KEY environment variable.
func buildOpenAIProvider(cfg *config.AppConfig) (*openai.Provider, error) {
	key := resolveProviderKey("OPENAI_API_KEY", cfg.Providers.OpenAI.APIKey)
	if key == "" {
		return nil, errOpenAINoKey
	}
	return openai.New(key)
}

// recordProvider registers a provider on success or records the error under
// name in the failures map. Logs a warning on failure so startup diagnostics
// remain visible even when the failure is not fatal.
//
// Expected:
//   - registry and failures are non-nil.
//   - p implements provider.Provider when err is nil.
//
// Returns:
//   - None.
//
// Side effects:
//   - Registers p with registry on success.
//   - Mutates failures on failure.
//   - Logs a warning on failure.
func recordProvider(
	registry *provider.Registry,
	failures map[string]error,
	name string,
	p provider.Provider,
	err error,
) {
	if err == nil {
		registry.Register(p)
		return
	}
	failures[name] = err
	log.Printf("warning: provider %q unavailable: %v", name, err)
}

// resolveDefaultProvider verifies the default provider is registered and
// returns a diagnostic error that surfaces the list of registered providers
// and the reason the default provider failed to register, if any.
//
// Expected:
//   - registry is non-nil and already populated by setupProvidersWithFailures.
//   - failures is the per-provider failure map from setupProvidersWithFailures.
//     May be nil.
//   - defaultName is the provider name resolved from cfg.Providers.Default.
//
// Returns:
//   - nil if the default provider is registered.
//   - An error wrapping the lookup failure with diagnostic context otherwise.
//
// Side effects:
//   - None.
func resolveDefaultProvider(
	registry *provider.Registry,
	failures map[string]error,
	defaultName string,
) error {
	if _, err := registry.Get(defaultName); err != nil {
		return fmt.Errorf(
			"getting default provider %q: %w",
			defaultName,
			describeProviderResolutionFailure(
				defaultName,
				registry.List(),
				failures,
				err,
			),
		)
	}
	return nil
}

// describeProviderResolutionFailure returns an error whose message surfaces the
// full diagnostic context for a missing default provider: the list of
// successfully registered providers and the per-provider failure reasons.
// This makes startup failures actionable from stderr alone, rather than
// requiring the user to grep the log file at ~/.local/share/flowstate/flowstate.log.
//
// Expected:
//   - requested is the name of the provider resolved from cfg.Providers.Default.
//   - registered is the list of provider names that successfully registered.
//   - failures is a map of provider-name to the constructor error. May be nil or empty.
//   - lookupErr is the error returned by provider.Registry.Get for the requested provider.
//
// Returns:
//   - An error wrapping lookupErr with additional context. Never nil.
//
// Side effects:
//   - None.
func describeProviderResolutionFailure(
	requested string,
	registered []string,
	failures map[string]error,
	lookupErr error,
) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%v\n  registered: %v", lookupErr, registered)
	if failure, ok := failures[requested]; ok && failure != nil {
		fmt.Fprintf(&b, "\n  %s failure: %v", requested, failure)
	}
	if len(failures) > 0 {
		// Emit other failures in a stable order so the error message is
		// deterministic in tests and log analysis.
		names := make([]string, 0, len(failures))
		for name := range failures {
			if name == requested {
				continue
			}
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) > 0 {
			b.WriteString("\n  other failures:")
			for _, name := range names {
				fmt.Fprintf(&b, "\n    %s: %v", name, failures[name])
			}
		}
	}
	return errors.New(b.String())
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
		{"zai", cfg.Providers.ZAI.Model},
		{"openzen", cfg.Providers.OpenZen.Model},
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

// RegisterProvidersWithFailuresForTest is a test helper that exposes
// setupProvidersWithFailures so tests can assert which providers failed to
// register and the reason for each failure.
//
// Expected:
//   - cfg is a non-nil AppConfig with provider configuration.
//
// Returns:
//   - The provider registry with all successfully registered providers.
//   - The Ollama provider instance (may be nil if initialisation failed).
//   - A map from provider name to constructor error for each failed provider.
//
// Side effects:
//   - Same as setupProvidersWithFailures: reads environment variables and logs warnings.
func RegisterProvidersWithFailuresForTest(
	cfg *config.AppConfig,
) (*provider.Registry, *ollama.Provider, map[string]error) {
	return setupProvidersWithFailures(cfg)
}

// BuildHookChainForTest is a test helper that exposes buildHookChain for testing.
//
// Expected:
//   - learningStore may be nil or a Mem0LearningStore for persisting learning data.
//     If nil, learning is disabled. Mem0LearningStore provides in-memory persistence without external dependencies.
//   - manifestGetter returns the current agent manifest for hook selection.
//
// Returns:
//   - A fully configured hook.Chain for inspection in tests.
//
// Side effects:
//   - None.
func BuildHookChainForTest(
	learningStore learning.Store,
	manifestGetter func() agent.Manifest,
) *hook.Chain {
	return buildHookChain(hookChainConfig{
		learningStore:  learningStore,
		manifestGetter: manifestGetter,
	})
}

// BuildHookChainWithDispatcherForTest is a test helper that exposes buildHookChain with a
// dispatcher for testing the chat.params dispatch path.
//
// Expected:
//   - learningStore may be nil.
//   - manifestGetter returns the current agent manifest for hook selection.
//   - dispatcher may be nil; when non-nil, a chat.params hook is inserted after SkillAutoLoaderHook.
//
// Returns:
//   - A fully configured hook.Chain for inspection in tests.
//
// Side effects:
//   - None.
func BuildHookChainWithDispatcherForTest(
	learningStore learning.Store,
	manifestGetter func() agent.Manifest,
	dispatcher *external.Dispatcher,
) *hook.Chain {
	return buildHookChain(hookChainConfig{
		learningStore:  learningStore,
		manifestGetter: manifestGetter,
		dispatcher:     dispatcher,
	})
}

// MergeMCPServersForTest is a test helper that exposes mergeMCPServers for testing.
//
// Expected:
//   - configured is the user-defined server list from config.
//   - discovered is the auto-detected server list.
//
// Returns:
//   - A merged slice with configured servers taking precedence over discovered ones.
//
// Side effects:
//   - None.
func MergeMCPServersForTest(configured, discovered []config.MCPServerConfig) []config.MCPServerConfig {
	return mergeMCPServers(configured, discovered)
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

// setupAgentRegistry creates and populates an agent registry using layered discovery.
//
// The primary directory (AgentDir) is discovered first via Discover. Each entry in
// AgentDirs is then merged in order via DiscoverMerge, so later entries override
// earlier ones on ID clash. Missing AgentDirs entries are skipped with a log message.
//
// Expected:
//   - cfg is a non-nil AppConfig with a valid AgentDir path.
//
// Returns:
//   - A populated agent.Registry containing discovered agents.
//
// Side effects:
//   - Reads agent manifest files from the configured directories.
//   - Logs warnings when discovery or merge operations encounter errors.
func setupAgentRegistry(cfg *config.AppConfig) *agent.Registry {
	r := agent.NewRegistry()

	if err := r.Discover(cfg.AgentDir); err != nil {
		log.Printf("warning: discovering agents in %q: %v", cfg.AgentDir, err)
	}

	for _, dir := range cfg.AgentDirs {
		if err := r.DiscoverMerge(dir); err != nil {
			if errors.Is(err, agent.ErrAgentDirNotFound) {
				log.Printf("info: skipping missing agent dir %q", dir)
				continue
			}
			log.Printf("warning: merging agents from %q: %v", dir, err)
		}
	}

	manifests := r.List()
	if len(manifests) == 0 {
		log.Printf("warning: no agents discovered")
	} else {
		log.Printf("info: %d agent(s) in registry", len(manifests))
	}
	return r
}

// createDataStores initialises the session and learning data stores.
//
// Expected:
//   - cfg is a non-nil AppConfig with a valid DataDir path.
//   - ollamaProvider may be nil; when nil and Qdrant is configured, the
//     learning store is created with a no-op embedder so writes surface a
//     clear error rather than silently corrupting the collection.
//
// Returns:
//   - A FileSessionStore for persisting session data.
//   - A Store for persisting learning data (Mem0LearningStore if Qdrant configured, nil otherwise).
//   - An error if session store creation fails.
//
// Side effects:
//   - Creates the sessions subdirectory if it does not exist.
//   - Initializes Qdrant client when configured; no file created otherwise.
func createDataStores(cfg *config.AppConfig, ollamaProvider embedRequester) (*ctxstore.FileSessionStore, learning.Store, error) {
	sessionStore, err := ctxstore.NewFileSessionStore(filepath.Join(cfg.DataDir, "sessions"))
	if err != nil {
		return nil, nil, fmt.Errorf("creating session store: %w", err)
	}

	// Create Mem0LearningStore with Qdrant if configured, otherwise fall back to JSONFileStore
	var learningStore learning.Store
	if cfg.Qdrant.URL != "" {
		qdrantClient := qdrantrecall.NewClient(cfg.Qdrant.URL, cfg.Qdrant.APIKey, nil)
		embedder := newRecallEmbedder(ollamaProvider, cfg.ResolvedEmbeddingModel())
		adapter := &qdrantClientAdapter{client: qdrantClient}
		learningStore = learning.NewMem0LearningStore(adapter, embedder, cfg.Qdrant.Collection)
	}

	return sessionStore, learningStore, nil
}

// runOrphanEventTmpScan sweeps leftover `.events.jsonl.tmp` files from
// sessionsDir. Such files only exist as intermediate staging files for a
// SwarmEvent compaction; a surviving one after process shutdown means a
// previous run crashed mid-rename and left a half-written stager behind.
//
// Called once during app startup after the session store is constructed
// and before the TUI launches. Errors do NOT block startup — they are
// logged at WARN and the boot continues so a disk-level problem with the
// sessions directory cannot prevent the app from coming up.
//
// Expected:
//   - sessionsDir is the directory where sessions are persisted. A missing
//     directory is treated as a no-op by the underlying helper.
//
// Returns:
//   - Nothing. Diagnostics go through slog.
//
// Side effects:
//   - Deletes matching files from disk.
//   - Emits an slog.Warn on failure and an slog.Info when one or more
//     orphans were swept (quiet on the zero-removed happy path).
func runOrphanEventTmpScan(sessionsDir string) {
	removed, err := session.CleanupOrphanEventTmpFiles(sessionsDir)
	if err != nil {
		slog.Warn("orphan events.jsonl.tmp scan failed at startup",
			"sessions_dir", sessionsDir,
			"error", err,
		)
		return
	}
	if removed > 0 {
		slog.Info("swept orphan events.jsonl.tmp files at startup",
			"sessions_dir", sessionsDir,
			"removed", removed,
		)
	}
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

// createChainStore initialises the chain store for conversation context.
//
// Expected:
//   - cfg is a non-nil AppConfig with a valid DataDir.
//
// Returns:
//   - A persistent chain store when the file-backed store can be created.
//   - An in-memory chain store when file-backed initialisation fails.
//
// Side effects:
//   - Attempts to open the chain store file under cfg.DataDir.
func createChainStore(cfg *config.AppConfig) recall.ChainContextStore {
	chainPath := filepath.Join(cfg.DataDir, "chain_store.json")
	store, err := recall.NewFileChainStore(chainPath)
	if err != nil {
		return recall.NewInMemoryChainStore(nil)
	}
	return store
}

// qdrantClientAdapter bridges qdrant.Client to learning.VectorStoreClient
// by converting qdrant.ScoredPoint to learning.ScoredVectorPoint.
type qdrantClientAdapter struct {
	client *qdrantrecall.Client
}

// Upsert stores or updates points in the Qdrant collection.
//
// Expected:
//   - ctx is a valid context.
//   - collection is the target collection name.
//   - points contains the vectors and payloads to upsert.
//   - wait indicates whether to wait for the operation to complete.
//
// Returns:
//   - nil on success.
//   - An error if the upsert operation fails.
//
// Side effects:
//   - Sends an upsert request to the wrapped Qdrant client.
func (a *qdrantClientAdapter) Upsert(ctx context.Context, collection string, points []learning.VectorPoint, wait bool) error {
	qdrantPoints := make([]qdrantrecall.Point, len(points))
	for i, p := range points {
		qdrantPoints[i] = qdrantrecall.Point{
			ID:      p.ID,
			Vector:  p.Vector,
			Payload: p.Payload,
		}
	}
	return a.client.Upsert(ctx, collection, qdrantPoints, wait)
}

// Search finds the nearest vectors in the Qdrant collection.
//
// Expected:
//   - ctx is a valid context.
//   - collection is the target collection name.
//   - vector is the query vector.
//   - limit is the maximum number of results to return.
//
// Returns:
//   - A slice of ScoredVectorPoint results.
//   - An error if the search operation fails.
//
// Side effects:
//   - Sends a search request to the wrapped Qdrant client.
func (a *qdrantClientAdapter) Search(ctx context.Context, collection string,
	vector []float64, limit int) ([]learning.ScoredVectorPoint, error) {
	qdrantResults, err := a.client.Search(ctx, collection, vector, limit)
	if err != nil {
		return nil, err
	}
	results := make([]learning.ScoredVectorPoint, len(qdrantResults))
	for i, qr := range qdrantResults {
		results[i] = learning.ScoredVectorPoint{
			ID:      qr.ID,
			Score:   qr.Score,
			Payload: qr.Payload,
		}
	}
	return results, nil
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

// recallBrokerParams groups the dependencies required to build a recall broker.
type recallBrokerParams struct {
	cfg            *config.AppConfig
	chatProvider   provider.Provider
	mcpClient      mcpclient.Client
	contextStore   *recall.FileContextStore
	chainStore     recall.ChainContextStore
	ollamaProvider embedRequester
}

// buildRecallBroker constructs a recall.Broker wired with MCP memory and,
// when configured, Qdrant- and vault-rag-backed sources.
//
// This is the default construction path used from setupEngine. It does not
// thread a vault path because the AppConfig currently has no vault field;
// vault-rag is therefore disabled until a caller opts in via
// buildRecallBrokerWithVault. See P7/C1 in the Activity Timeline & Cancel
// Fix Plan for why this gate matters: attaching a VaultSource with an empty
// vault string caused 185 auto-hook query_vault invocations per session and
// led to a non-JSON decode path the engine silently switched on.
//
// Expected:
//   - params.cfg is a non-nil application config; cfg.Qdrant.URL may be empty.
//   - params.chatProvider is retained for signature compatibility but NOT used
//     for embeddings — chat providers (e.g. anthropic) do not expose an
//     embeddings endpoint. Embeddings are routed to params.ollamaProvider.
//   - params.mcpClient is the MCP manager used to reach memory and vault-rag.
//   - params.contextStore is the session context store; may be nil.
//   - params.chainStore is the chain context store; may be nil.
//   - params.ollamaProvider is the Ollama provider used for embeddings; when
//     nil, a no-op embedder is used and Qdrant queries surface a clear error.
//
// Returns:
//   - A non-nil recall.Broker with MCP memory always attached.
//   - When cfg.Qdrant.URL is non-empty the Qdrant learning source is also included.
//   - The vault source is attached when cfg.VaultPath is non-empty.
//
// Side effects:
//   - None; Qdrant connections are established lazily per-request.
func buildRecallBroker(params recallBrokerParams) recall.Broker {
	return buildRecallBrokerWithVault(params, params.cfg.VaultPath)
}

// buildRecallBrokerWithVault constructs a recall.Broker with an explicit
// vault path. When vaultPath is empty or whitespace-only the vault source
// is omitted; when non-empty a vault-rag source is attached that will
// query MCP on every broker.Query.
//
// Expected:
//   - params is the same input bundle as buildRecallBroker.
//   - vaultPath is the vault scope passed to vaultrecall.NewVaultSource; an
//     empty string disables the source.
//
// Returns:
//   - A non-nil recall.Broker wired with MCP memory and, conditionally,
//     vault and Qdrant sources.
//
// Side effects:
//   - None; connections are established lazily per-request.
func buildRecallBrokerWithVault(params recallBrokerParams, vaultPath string) recall.Broker {
	_ = params.chatProvider // retained for compatibility; embeddings use Ollama.
	cfg := params.cfg
	memClient := learning.NewMCPMemoryClient(params.mcpClient, "memory")
	memSource := recall.NewMCPMemorySource(recall.NewMCPLearningSource(memClient, nil))
	sessionSrc := recall.NewSessionSource(params.contextStore)
	chainSrc := recall.NewChainSource(params.chainStore)

	extras := []recall.Source{memSource}
	if strings.TrimSpace(vaultPath) != "" {
		extras = append(extras, vaultrecall.NewVaultSource(params.mcpClient, "vault-rag", vaultPath))
	} else {
		slog.Warn("vault path unset; vault-rag recall source disabled — set a vault path to enable Obsidian-backed recall")
	}

	if cfg.Qdrant.URL == "" {
		slog.Warn("Qdrant not configured; recall broker disabled — set QDRANT_URL to enable vector recall")
		return recall.NewRecallBroker(sessionSrc, chainSrc, nil, nil, extras...)
	}
	col := cfg.Qdrant.Collection
	if col == "" {
		col = "flowstate-recall"
	}
	client := qdrantrecall.NewClient(cfg.Qdrant.URL, cfg.Qdrant.APIKey, nil)
	embedder := newRecallEmbedder(params.ollamaProvider, cfg.ResolvedEmbeddingModel())
	source := qdrantrecall.NewSource(client, embedder, col)
	return recall.NewRecallBroker(sessionSrc, chainSrc, nil, source, extras...)
}

// buildDistiller constructs a StructuredDistiller backed by Qdrant when configured.
//
// Expected:
//   - cfg is a non-nil application config; cfg.Qdrant.URL may be empty.
//   - chatProvider is retained for signature compatibility but NOT used for
//     embeddings — see Bug #4. Embeddings are routed to ollamaProvider.
//   - ollamaProvider is the Ollama provider used for embeddings; when nil, a
//     no-op embedder is used and distillation writes surface a clear error.
//
// Returns:
//   - A non-nil Distiller when cfg.Qdrant.URL is non-empty.
//   - nil when Qdrant is not configured, disabling structured distillation.
//
// Side effects:
//   - None; Qdrant connections are established lazily per-request.
func buildDistiller(cfg *config.AppConfig, _ provider.Provider, ollamaProvider embedRequester) learning.Distiller {
	if cfg == nil || cfg.Qdrant.URL == "" {
		return nil
	}
	col := cfg.Qdrant.Collection
	if col == "" {
		col = "flowstate-recall"
	}
	client := qdrantrecall.NewClient(cfg.Qdrant.URL, cfg.Qdrant.APIKey, nil)
	embedder := newRecallEmbedder(ollamaProvider, cfg.ResolvedEmbeddingModel())
	adapter := &qdrantClientAdapter{client: client}
	memClient := learning.NewVectorStoreMemoryClient(adapter, embedder, col)
	return learning.NewStructuredDistiller(memClient)
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
	runOrphanEventTmpScan(tc.SessionsDir)

	// Learning store is nil when Qdrant is not configured (graceful degradation)
	var learningStore learning.Store

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

// CompletionOrchestrator returns the background task completion orchestrator,
// or nil if delegation is not enabled.
//
// Returns:
//   - The CompletionOrchestrator, or nil.
//
// Side effects:
//   - None.
func (a *App) CompletionOrchestrator() *engine.CompletionOrchestrator {
	return a.completionOrchestrator
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
	learningStore learning.Store,
	manifestGetter func() agent.Manifest,
) *hook.Chain {
	health := failover.NewHealthManager()
	chain := failover.NewFallbackChain(defaultFailoverProviders(), defaultFailoverTiers())
	fh := failover.NewHook(chain, health)
	return buildHookChain(hookChainConfig{
		learningStore:  learningStore,
		manifestGetter: manifestGetter,
		failoverHk:     fh,
	})
}

// SessionMgr returns the session manager for the TUI layer.
//
// Returns:
//   - The session Manager, or nil if not configured.
//
// Side effects:
//   - None.
func (a *App) SessionMgr() *session.Manager {
	return a.sessionManager
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
