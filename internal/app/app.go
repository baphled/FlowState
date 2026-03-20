// Package app provides the main application container and initialization.
package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/config"
	ctxstore "github.com/baphled/flowstate/internal/context"
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
	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/bash"
	"github.com/baphled/flowstate/internal/tool/file"
	"github.com/baphled/flowstate/internal/tool/mcpproxy"
	"github.com/baphled/flowstate/internal/tool/web"
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
	mcpClient        mcpclient.Client
	providerRegistry *provider.Registry
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
	bundledFS, err := BundledAgentsDir()
	if err != nil {
		log.Printf("warning: bundled agents not found: %v", err)
	} else {
		if err := SeedAgentsDir(bundledFS, cfg.AgentDir); err != nil {
			log.Printf("warning: seeding agents to %q: %v", cfg.AgentDir, err)
		} else {
			log.Printf("info: agents seeded to %q", cfg.AgentDir)
		}
	}

	providerRegistry, ollamaProvider := registerProviders(cfg)
	agentRegistry := setupAgentRegistry(cfg)
	skills, alwaysActiveSkills := loadSkills(cfg)
	sessionStore, learningStore, err := createDataStores(cfg)
	if err != nil {
		return nil, err
	}
	defaultProvider, err := providerRegistry.Get(cfg.Providers.Default)
	if err != nil {
		return nil, fmt.Errorf("getting default provider %q: %w", cfg.Providers.Default, err)
	}
	contextStore, err := createContextStore(cfg)
	if err != nil {
		return nil, err
	}
	mcpMgr := mcpclient.NewManager()
	appTools := buildTools()
	discoveredServers := config.DiscoverMCPServers()
	allServers := mergeMCPServers(cfg.MCPServers, discoveredServers)
	mcpTools := ConnectMCPServers(context.Background(), mcpMgr, allServers)
	appTools = append(appTools, mcpTools...)
	toolRegistry, permHandler := buildToolsSetup(appTools)
	eng := engine.New(engine.Config{
		ChatProvider:      defaultProvider,
		EmbeddingProvider: toEmbeddingProvider(ollamaProvider),
		Registry:          providerRegistry,
		Manifest:          selectDefaultManifest(agentRegistry, cfg.DefaultAgent),
		Skills:            alwaysActiveSkills,
		Store:             contextStore,
		HookChain:         buildHookChain(learningStore, alwaysActiveSkills),
		Tools:             appTools,
		ToolRegistry:      toolRegistry,
		PermissionHandler: permHandler,
	})
	disc := createDiscovery(agentRegistry)
	apiServer := api.NewServer(eng, agentRegistry, disc, skills, sessionStore)

	return &App{
		Config:           cfg,
		Registry:         agentRegistry,
		Skills:           skills,
		Engine:           eng,
		Discovery:        disc,
		Sessions:         sessionStore,
		Learning:         learningStore,
		API:              apiServer,
		mcpClient:        mcpMgr,
		providerRegistry: providerRegistry,
	}, nil
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

// extractSkillNames extracts the name field from each skill in the provided slice.
//
// Expected:
//   - skills is a non-nil slice of Skill values.
//
// Returns:
//   - A string slice containing the name of each skill in the same order.
//
// Side effects:
//   - None.
func extractSkillNames(skills []skill.Skill) []string {
	names := make([]string, len(skills))
	for i := range skills {
		names[i] = skills[i].Name
	}
	return names
}

// buildTools constructs and returns the default set of available tools.
//
// Expected:
//   - None.
//
// Returns:
//   - A slice containing bash, file, and web tools.
//
// Side effects:
//   - Initialises new tool instances.
func buildTools() []tool.Tool {
	return []tool.Tool{
		bash.New(),
		file.New(),
		web.New(),
	}
}

// buildToolsSetup creates a tool registry and permission handler for the engine.
//
// Expected:
//   - tools is a slice of tool.Tool values to register.
//
// Returns:
//   - A tool.Registry with all tools registered.
//   - A tool.PermissionHandler that allows all tool invocations.
//
// Side effects:
//   - None.
func buildToolsSetup(tools []tool.Tool) (*tool.Registry, tool.PermissionHandler) {
	registry := tool.NewRegistry()
	for _, t := range tools {
		registry.Register(t)
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

// loadSkills loads all available skills and always-active skills from the configured skill directory.
//
// Expected:
//   - cfg is a non-nil AppConfig with a valid SkillDir path.
//
// Returns:
//   - A slice of all loaded skills (empty slice if loading fails).
//   - A slice of always-active skills loaded from the engine.
//
// Side effects:
//   - Reads skill files from the configured skill directory.
//   - Logs a warning if skill loading fails.
func loadSkills(cfg *config.AppConfig) ([]skill.Skill, []skill.Skill) {
	skillLoader := skill.NewFileSkillLoader(cfg.SkillDir)
	skills, err := skillLoader.LoadAll()
	if err != nil {
		log.Printf("warning: loading skills: %v", err)
		skills = []skill.Skill{}
	}
	alwaysActiveSkills := engine.LoadAlwaysActiveSkills(cfg.SkillDir, nil, nil)
	return skills, alwaysActiveSkills
}

// buildHookChain constructs a hook chain with logging, learning, and context injection hooks.
//
// Expected:
//   - learningStore is a non-nil JSONFileStore for persisting learning data.
//   - alwaysActiveSkills is a non-nil slice of skills to inject into context.
//
// Returns:
//   - A fully configured hook.Chain ready for use in the engine.
//
// Side effects:
//   - None.
func buildHookChain(learningStore *learning.JSONFileStore, alwaysActiveSkills []skill.Skill) *hook.Chain {
	return hook.NewChain(
		hook.LoggingHook(),
		hook.LearningHook(learningStore),
		hook.ContextInjectionHook(alwaysActiveSkills, extractSkillNames(alwaysActiveSkills)),
	)
}

// registerProviders initialises and registers all configured LLM providers.
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
func registerProviders(cfg *config.AppConfig) (*provider.Registry, *ollama.Provider) {
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
	if anthropicKey != "" {
		anthropicProvider, anthropicErr := anthropic.NewFromOpenCodeOrConfig(opencodePath, anthropicKey)
		if anthropicErr == nil {
			providerRegistry.Register(anthropicProvider)
		}
	}

	githubToken := os.Getenv("GITHUB_TOKEN")
	if githubToken == "" {
		githubToken = cfg.Providers.GitHub.APIKey
	}
	if githubToken != "" {
		copilotProvider, copilotErr := copilot.NewFromOpenCodeOrFallback(opencodePath, nil, githubToken)
		if copilotErr == nil {
			providerRegistry.Register(copilotProvider)
		}
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
	return registerProviders(cfg)
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
//   - Creates the context.json file path (file creation deferred to store).
func createContextStore(cfg *config.AppConfig) (*ctxstore.FileContextStore, error) {
	contextStorePath := filepath.Join(cfg.DataDir, "context.json")
	contextStore, err := ctxstore.NewFileContextStore(contextStorePath, cfg.Providers.Ollama.Model)
	if err != nil {
		return nil, fmt.Errorf("creating context store: %w", err)
	}
	return contextStore, nil
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
