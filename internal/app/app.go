// Package app provides the main application container and initialization.
package app

import (
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
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/anthropic"
	"github.com/baphled/flowstate/internal/provider/ollama"
	"github.com/baphled/flowstate/internal/provider/openai"
	"github.com/baphled/flowstate/internal/skill"
)

// App is the main application container holding all initialized components.
type App struct {
	Config    *config.AppConfig
	Registry  *agent.AgentRegistry
	Skills    []skill.Skill
	Engine    *engine.Engine
	Discovery *discovery.AgentDiscovery
	Sessions  *ctxstore.FileSessionStore
	Learning  *learning.JSONFileStore
	API       *api.Server
}

// New creates a new App instance with all components initialized.
//
//nolint:funlen // Initialization function that bootstraps the entire application
func New(cfg *config.AppConfig) (*App, error) {
	providerRegistry := provider.NewRegistry()

	ollamaProvider, err := ollama.New(cfg.Providers.Ollama.Host)
	if err == nil {
		providerRegistry.Register(ollamaProvider)
	}

	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		openaiProvider, err := openai.New(apiKey)
		if err == nil {
			providerRegistry.Register(openaiProvider)
		}
	}

	if apiKey := os.Getenv("ANTHROPIC_API_KEY"); apiKey != "" {
		anthropicProvider, err := anthropic.New(apiKey)
		if err == nil {
			providerRegistry.Register(anthropicProvider)
		}
	}

	agentRegistry := agent.NewAgentRegistry()
	if err := agentRegistry.Discover(cfg.AgentDir); err != nil {
		log.Printf("warning: discovering agents: %v", err)
	}

	skillLoader := skill.NewFileSkillLoader(cfg.SkillDir)
	skills, err := skillLoader.LoadAll()
	if err != nil {
		log.Printf("warning: loading skills: %v", err)
		skills = []skill.Skill{}
	}

	alwaysActiveSkills := engine.LoadAlwaysActiveSkills(cfg.SkillDir, nil, nil)

	sessionsDir := filepath.Join(cfg.DataDir, "sessions")
	sessionStore, err := ctxstore.NewFileSessionStore(sessionsDir)
	if err != nil {
		return nil, fmt.Errorf("creating session store: %w", err)
	}

	learningsPath := filepath.Join(cfg.DataDir, "learnings.json")
	learningStore := learning.NewJSONFileStore(learningsPath)

	hooks := hook.NewChain(
		hook.LoggingHook(),
		hook.LearningHook(learningStore),
		hook.ContextInjectionHook(alwaysActiveSkills, extractSkillNames(alwaysActiveSkills)),
	)

	defaultProvider, err := providerRegistry.Get(cfg.Providers.Default)
	if err != nil {
		return nil, fmt.Errorf("getting default provider %q: %w", cfg.Providers.Default, err)
	}
	var embeddingProvider provider.Provider
	if ollamaProvider != nil {
		embeddingProvider = ollamaProvider
	}

	contextStorePath := filepath.Join(cfg.DataDir, "context.json")
	contextStore, err := ctxstore.NewFileContextStore(contextStorePath, cfg.Providers.Ollama.Model)
	if err != nil {
		return nil, fmt.Errorf("creating context store: %w", err)
	}

	defaultManifest := agent.AgentManifest{
		ID:   "default",
		Name: "Default Agent",
	}
	if manifests := agentRegistry.List(); len(manifests) > 0 {
		defaultManifest = *manifests[0]
	}

	eng := engine.New(engine.Config{
		ChatProvider:      defaultProvider,
		EmbeddingProvider: embeddingProvider,
		Registry:          providerRegistry,
		Manifest:          defaultManifest,
		Skills:            alwaysActiveSkills,
		Store:             contextStore,
		HookChain:         hooks,
	})

	manifests := agentRegistry.List()
	manifestValues := make([]agent.AgentManifest, len(manifests))
	for i, m := range manifests {
		manifestValues[i] = *m
	}
	disc := discovery.NewAgentDiscovery(manifestValues)

	apiServer := api.NewServer(eng, agentRegistry, disc, skills)

	return &App{
		Config:    cfg,
		Registry:  agentRegistry,
		Skills:    skills,
		Engine:    eng,
		Discovery: disc,
		Sessions:  sessionStore,
		Learning:  learningStore,
		API:       apiServer,
	}, nil
}

// AgentsDir returns the directory where agent manifests are stored.
func (a *App) AgentsDir() string {
	return a.Config.AgentDir
}

// SkillsDir returns the directory where skills are stored.
func (a *App) SkillsDir() string {
	return a.Config.SkillDir
}

// SessionsDir returns the directory where sessions are stored.
func (a *App) SessionsDir() string {
	return filepath.Join(a.Config.DataDir, "sessions")
}

// ConfigPath returns the path to the configuration file.
func (a *App) ConfigPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(a.Config.DataDir, "config.yaml")
	}
	return filepath.Join(homeDir, ".flowstate", "config.yaml")
}

func extractSkillNames(skills []skill.Skill) []string {
	names := make([]string, len(skills))
	for i := range skills {
		names[i] = skills[i].Name
	}
	return names
}

// TestConfig holds configuration for creating test App instances.
type TestConfig struct {
	AgentsDir   string
	SkillsDir   string
	SessionsDir string
	DataDir     string
}

// NewForTest creates an App instance for testing with minimal dependencies.
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

	agentRegistry := agent.NewAgentRegistry()
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
	manifestValues := make([]agent.AgentManifest, len(manifests))
	for i, m := range manifests {
		manifestValues[i] = *m
	}
	disc := discovery.NewAgentDiscovery(manifestValues)

	return &App{
		Config:    cfg,
		Registry:  agentRegistry,
		Skills:    skills,
		Engine:    nil,
		Discovery: disc,
		Sessions:  sessionStore,
		Learning:  learningStore,
		API:       nil,
	}, nil
}
