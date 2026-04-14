// Package config loads FlowState application configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	contextpkg "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	pluginpkg "github.com/baphled/flowstate/internal/plugin"
	"gopkg.in/yaml.v3"
)

// Config aliases AppConfig for callers that use the shorter configuration name.
type Config = AppConfig

// AppConfig holds the complete application configuration.
type AppConfig struct {
	Providers ProvidersConfig `json:"providers" yaml:"providers"`
	AgentDir  string          `json:"agent_dir" yaml:"agent_dir"`
	// AgentDirs lists user-defined agent directories merged into the registry after
	// AgentDir. Agents in later directories override agents with the same ID from
	// earlier directories and from AgentDir. Tilde paths (~/...) are expanded at load time.
	AgentDirs          []string                         `json:"agent_dirs" yaml:"agent_dirs"`
	SkillDir           string                           `json:"skill_dir" yaml:"skill_dir"`
	DataDir            string                           `json:"data_dir" yaml:"data_dir"`
	LogLevel           string                           `json:"log_level" yaml:"log_level"`
	DefaultAgent       string                           `json:"default_agent" yaml:"default_agent"`
	CategoryRouting    map[string]engine.CategoryConfig `json:"category_routing" yaml:"category_routing"`
	Plugins            PluginsConfig                    `json:"plugins" yaml:"plugins,omitempty"`
	MCPServers         []MCPServerConfig                `yaml:"mcp_servers,omitempty"`
	AlwaysActiveSkills []string                         `yaml:"always_active_skills,omitempty"`
	Harness            HarnessConfig                    `json:"harness" yaml:"harness"`
	AgentOverrides     map[string]AgentOverrideConfig   `json:"agent_overrides" yaml:"agent_overrides"`
	// ContextAssemblyHooks lets callers inject custom context assembly hooks at runtime.
	ContextAssemblyHooks []pluginpkg.ContextAssemblyHook `json:"-" yaml:"-"`
	SessionRecording     bool                            `json:"session_recording" yaml:"session_recording"`
	Qdrant               QdrantConfig                    `json:"qdrant" yaml:"qdrant"`
	// Compression controls the three-layer context compression system
	// (micro-compaction, auto-compaction, session-memory). All layers
	// default to disabled; see internal/context.DefaultCompressionConfig.
	Compression contextpkg.CompressionConfig `json:"compression" yaml:"compression"`
}

// QdrantConfig provides configuration for Qdrant-based recall storage.
//
// Fields:
//   - URL: The base URL of the Qdrant server (e.g., "http://localhost:6333").
//   - Collection: The Qdrant collection name to use for recall storage.
//   - APIKey: The optional API key for authenticated Qdrant instances.
//
// Expected:
//   - Used to configure Qdrant-backed recall in the application engine.
//
// Returns:
//   - None.
//
// Side effects:
//   - None.
type QdrantConfig struct {
	URL        string `json:"url" yaml:"url"`
	Collection string `json:"collection" yaml:"collection"`
	APIKey     string `json:"api_key" yaml:"api_key"`
}

// ProvidersConfig configures all available LLM providers.
type ProvidersConfig struct {
	Anthropic ProviderConfig `json:"anthropic" yaml:"anthropic"`
	Default   string         `json:"default" yaml:"default"`
	GitHub    ProviderConfig `json:"github" yaml:"github"`
	Ollama    ProviderConfig `json:"ollama" yaml:"ollama"`
	OpenAI    ProviderConfig `json:"openai" yaml:"openai"`
	OpenZen   ProviderConfig `json:"openzen" yaml:"openzen"`
	ZAI       ProviderConfig `json:"zai" yaml:"zai"`
}

// ProviderConfig holds configuration for a single LLM provider.
type ProviderConfig struct {
	Host   string      `json:"host" yaml:"host"`
	APIKey string      `json:"api_key" yaml:"api_key"`
	Model  string      `json:"model" yaml:"model"`
	OAuth  OAuthConfig `json:"oauth" yaml:"oauth"`
}

// OAuthConfig holds OAuth-specific configuration for a provider.
type OAuthConfig struct {
	Enabled   bool   `json:"enabled" yaml:"enabled"`
	ClientID  string `json:"client_id" yaml:"client_id"`
	TokenFile string `json:"token_file" yaml:"token_file"`
	Scopes    string `json:"scopes" yaml:"scopes"`
	UseOAuth  bool   `json:"use_oauth" yaml:"use_oauth"`
}

// MCPToolPermission defines the permission mode for a specific MCP server tool.
type MCPToolPermission struct {
	ServerName string `yaml:"server_name"`
	ToolName   string `yaml:"tool_name"`
	Permission string `yaml:"permission"`
}

// MCPServerConfig holds configuration for a single MCP server connection.
// Name and Command are required fields.
type MCPServerConfig struct {
	Name    string            `yaml:"name"`
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
	Enabled bool              `yaml:"enabled"`
}

// HarnessConfig holds configuration for the planning harness.
//
// Each field controls an optional layer of the harness. By default,
// the harness is enabled but the critic and voting are disabled.
// MaxRetries controls how many evaluation attempts the harness makes
// before returning a best-effort result; defaults to 1.
//
// Mode selects the harness loop type. Valid values are "plan" (default)
// and "execution". When empty, "plan" behaviour is assumed.
//
// LearningEnabled enables the async learning loop for this harness.
// LearningOnFailure triggers learning captures when evaluation fails.
// LearningOnNovelty triggers learning captures when novel output is detected.
type HarnessConfig struct {
	Enabled            bool   `json:"enabled" yaml:"enabled"`
	ProjectRoot        string `json:"project_root" yaml:"project_root"`
	CriticEnabled      bool   `json:"critic_enabled" yaml:"critic_enabled"`
	VotingEnabled      bool   `json:"voting_enabled" yaml:"voting_enabled"`
	IncrementalEnabled bool   `json:"incremental_enabled" yaml:"incremental_enabled"`
	MaxRetries         int    `json:"harness_max_retries" yaml:"harness_max_retries"`
	Mode               string `json:"mode" yaml:"mode"`
	LearningEnabled    bool   `json:"learning_enabled" yaml:"learning_enabled"`
	LearningOnFailure  bool   `json:"learning_on_failure" yaml:"learning_on_failure"`
	LearningOnNovelty  bool   `json:"learning_on_novelty" yaml:"learning_on_novelty"`
}

// AgentOverrideConfig holds per-agent configuration overrides.
//
// PromptAppend contains text to be appended to an agent's system prompt
// at runtime, without modifying the agent .md file.
type AgentOverrideConfig struct {
	PromptAppend string `json:"prompt_append" yaml:"prompt_append"`
}

// PluginsConfig holds configuration for FlowState plugins.
type PluginsConfig struct {
	Dir      string         `json:"dir" yaml:"dir,omitempty"`
	Enabled  []string       `json:"enabled" yaml:"enabled,omitempty"`
	Disabled []string       `json:"disabled" yaml:"disabled,omitempty"`
	Timeout  int            `json:"timeout" yaml:"timeout,omitempty"`
	Failover FailoverConfig `json:"failover" yaml:"failover,omitempty"`
}

// FailoverConfig holds configurable tier mappings for provider failover.
type FailoverConfig struct {
	Tiers map[string]string `json:"tiers" yaml:"tiers,omitempty"`
}

// Dir returns the configuration directory path.
//
// Checks XDG_CONFIG_HOME environment variable first, then falls back to
// ~/.config/flowstate. Returns the directory path (not the config file).
//
// Returns:
//   - The path to the FlowState configuration directory.
//
// Side effects:
//   - None.
func Dir() string {
	if xdgConfigHome := os.Getenv("XDG_CONFIG_HOME"); xdgConfigHome != "" {
		return filepath.Join(xdgConfigHome, "flowstate")
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "flowstate")
	}
	return filepath.Join(homeDir, ".config", "flowstate")
}

// DataDir returns the data directory path.
//
// Checks XDG_DATA_HOME environment variable first, then falls back to
// ~/.local/share/flowstate.
//
// Returns:
//   - The path to the FlowState data directory.
//
// Side effects:
//   - None.
func DataDir() string {
	if xdgDataHome := os.Getenv("XDG_DATA_HOME"); xdgDataHome != "" {
		return filepath.Join(xdgDataHome, "flowstate")
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "share", "flowstate")
	}
	return filepath.Join(homeDir, ".local", "share", "flowstate")
}

// DefaultConfig returns sensible default configuration values.
//
// Returns:
//   - An AppConfig populated with default provider and directory settings.
//
// Side effects:
//   - Resolves the user home directory to set the data path.
func DefaultConfig() *AppConfig {
	dataDir := DataDir()

	return &AppConfig{
		Providers: ProvidersConfig{
			Default: "anthropic",
			Ollama: ProviderConfig{
				Host:  "http://localhost:11434",
				Model: "llama3.2",
			},
			OpenAI: ProviderConfig{
				Model: "gpt-4o",
			},
			Anthropic: ProviderConfig{
				Model: "claude-sonnet-4-20250514",
			},
		},
		AgentDir:        filepath.Join(dataDir, "agents"),
		SkillDir:        filepath.Join(dataDir, "skills"),
		DataDir:         dataDir,
		LogLevel:        "info",
		DefaultAgent:    "executor",
		CategoryRouting: engine.DefaultCategoryRouting(),
		AlwaysActiveSkills: []string{
			"pre-action",
			"memory-keeper",
			"token-cost-estimation",
			"retrospective",
			"note-taking",
			"knowledge-base",
			"discipline",
			"skill-discovery",
			"agent-discovery",
		},
		Harness: HarnessConfig{
			Enabled:            true,
			CriticEnabled:      false,
			VotingEnabled:      false,
			IncrementalEnabled: false,
			MaxRetries:         1,
		},
		Plugins: PluginsConfig{
			Failover: FailoverConfig{
				Tiers: map[string]string{
					"claude-sonnet-4-20250514":   "tier-0",
					"claude-3-5-sonnet-20241022": "tier-0",
					"gpt-4o":                     "tier-1",
					"gpt-4o-mini":                "tier-2",
					"llama3.2":                   "tier-3",
					"llama3":                     "tier-3",
				},
			},
		},
		AgentOverrides: make(map[string]AgentOverrideConfig),
		Compression:    contextpkg.DefaultCompressionConfig(),
	}
}

// LoadConfig loads configuration from the default location.
//
// Checks paths in order:
//  1. $XDG_CONFIG_HOME/flowstate/config.yaml
//  2. ~/.config/flowstate/config.yaml
//  3. ~/.flowstate/config.yaml (backwards compatibility)
//
// Returns:
//   - An AppConfig loaded from the first found file, or defaults if none exist.
//   - An error only if a file exists but cannot be parsed.
//
// Side effects:
//   - Reads the configuration file from disk if it exists.
func LoadConfig() (*AppConfig, error) {
	paths := []string{
		filepath.Join(Dir(), "config.yaml"),
		filepath.Join(homeDir(), ".config", "flowstate", "config.yaml"),
		filepath.Join(homeDir(), ".flowstate", "config.yaml"),
	}

	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return LoadConfigFromPath(path)
		}
	}

	return DefaultConfig(), nil
}

// homeDir returns the user's home directory, or "." if it cannot be resolved.
//
// Returns:
//   - The user's home directory path, or "." as fallback.
//
// Side effects:
//   - None.
func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}

// LoadConfigFromPath loads configuration from the specified file path.
//
// Expected:
//   - path is a file path to a YAML configuration file.
//
// Returns:
//   - An AppConfig loaded from the file, with defaults applied for missing fields.
//   - An error if the file cannot be read or parsed.
//
// Side effects:
//   - Reads the configuration file from disk.
func LoadConfigFromPath(path string) (*AppConfig, error) {
	cleanPath := filepath.Clean(path)
	if _, err := os.Stat(cleanPath); err != nil {
		if os.IsNotExist(err) {
			cfg := DefaultConfig()
			expandPaths(cfg)
			return cfg, nil
		}
		return nil, fmt.Errorf("stat config file %q: %w", cleanPath, err)
	}

	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("reading config file %q: %w", cleanPath, err)
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file %q: %w", cleanPath, err)
	}

	applyDefaults(cfg)
	expandPaths(cfg)
	return cfg, nil
}

// ValidateMCPServers validates that all MCP servers have required fields.
//
// Expected:
//   - servers is a slice of MCPServerConfig.
//
// Returns:
//   - An error if any server is missing Name or Command, nil otherwise.
//
// Side effects:
//   - None.
func ValidateMCPServers(servers []MCPServerConfig) error {
	for i, server := range servers {
		if server.Name == "" {
			return fmt.Errorf("MCP server at index %d: missing required field 'name'", i)
		}
		if server.Command == "" {
			return fmt.Errorf("MCP server at index %d: missing required field 'command'", i)
		}
	}
	return nil
}

// applyDefaults populates missing configuration fields with sensible defaults.
//
// Expected:
//   - cfg is a non-nil AppConfig pointer.
//
// Side effects:
//   - Modifies cfg in place, filling empty fields with default values from DefaultConfig.
func applyDefaults(cfg *AppConfig) {
	defaults := DefaultConfig()

	if cfg.Providers.Default == "" {
		cfg.Providers.Default = defaults.Providers.Default
	}
	applyProviderDefaults(&cfg.Providers.Ollama, defaults.Providers.Ollama)
	applyProviderDefaults(&cfg.Providers.OpenAI, defaults.Providers.OpenAI)
	applyProviderDefaults(&cfg.Providers.Anthropic, defaults.Providers.Anthropic)

	if cfg.AgentDir == "" {
		cfg.AgentDir = defaults.AgentDir
	}
	if cfg.SkillDir == "" {
		cfg.SkillDir = defaults.SkillDir
	}
	if cfg.DataDir == "" {
		cfg.DataDir = defaults.DataDir
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = defaults.LogLevel
	}
	if cfg.DefaultAgent == "" {
		cfg.DefaultAgent = defaults.DefaultAgent
	}
	cfg.CategoryRouting = mergeCategoryRouting(defaults.CategoryRouting, cfg.CategoryRouting)
	if cfg.Plugins.Dir == "" {
		cfg.Plugins.Dir = filepath.Join(homeDir(), ".config", "flowstate", "plugins")
	}
	if cfg.Plugins.Timeout == 0 {
		cfg.Plugins.Timeout = 5
	}

	if len(cfg.Plugins.Failover.Tiers) == 0 {
		cfg.Plugins.Failover.Tiers = defaults.Plugins.Failover.Tiers
	}

	if !cfg.Harness.Enabled {
		cfg.Harness.Enabled = true
	}
	if cfg.Harness.MaxRetries == 0 {
		cfg.Harness.MaxRetries = defaults.Harness.MaxRetries
	}

	for i := range cfg.MCPServers {
		if !cfg.MCPServers[i].Enabled {
			cfg.MCPServers[i].Enabled = true
		}
	}

	applyCompressionDefaults(&cfg.Compression, defaults.Compression)
}

// applyCompressionDefaults fills empty numeric and path fields of the
// CompressionConfig from defaults, leaving any explicitly configured
// value untouched. Enabled flags are never overridden — an explicit
// false in YAML is preserved because all defaults are false too.
//
// Expected:
//   - cfg is a non-nil CompressionConfig pointer.
//   - defaults carries the values returned by DefaultCompressionConfig.
//
// Side effects:
//   - Modifies cfg in place.
func applyCompressionDefaults(cfg *contextpkg.CompressionConfig, defaults contextpkg.CompressionConfig) {
	if cfg.MicroCompaction.HotTailSize == 0 {
		cfg.MicroCompaction.HotTailSize = defaults.MicroCompaction.HotTailSize
	}
	if cfg.MicroCompaction.TokenThreshold == 0 {
		cfg.MicroCompaction.TokenThreshold = defaults.MicroCompaction.TokenThreshold
	}
	if cfg.MicroCompaction.StorageDir == "" {
		cfg.MicroCompaction.StorageDir = defaults.MicroCompaction.StorageDir
	}
	if cfg.MicroCompaction.PlaceholderTokens == 0 {
		cfg.MicroCompaction.PlaceholderTokens = defaults.MicroCompaction.PlaceholderTokens
	}
	if cfg.AutoCompaction.Threshold == 0 {
		cfg.AutoCompaction.Threshold = defaults.AutoCompaction.Threshold
	}
	if cfg.SessionMemory.StorageDir == "" {
		cfg.SessionMemory.StorageDir = defaults.SessionMemory.StorageDir
	}
}

// mergeCategoryRouting applies user overrides on top of the default routing map.
//
// Expected:
//   - defaults contains the base category routing configuration.
//   - overrides contains user-specified replacements.
//
// Returns:
//   - A merged map with overrides applied over defaults.
//
// Side effects:
//   - None.
func mergeCategoryRouting(defaults, overrides map[string]engine.CategoryConfig) map[string]engine.CategoryConfig {
	merged := make(map[string]engine.CategoryConfig, len(defaults))
	for key, value := range defaults {
		merged[key] = value
	}
	for key, value := range overrides {
		merged[key] = value
	}
	return merged
}

// applyProviderDefaults populates missing provider configuration fields with defaults.
//
// Expected:
//   - cfg is a non-nil ProviderConfig pointer.
//   - defaults is a ProviderConfig with fallback values.
//
// Side effects:
//   - Modifies cfg in place, filling empty Host, APIKey, and Model fields from defaults.
func applyProviderDefaults(cfg *ProviderConfig, defaults ProviderConfig) {
	if cfg.Host == "" {
		cfg.Host = defaults.Host
	}
	if cfg.APIKey == "" {
		cfg.APIKey = defaults.APIKey
	}
	if cfg.Model == "" {
		cfg.Model = defaults.Model
	}
	if cfg.OAuth.ClientID == "" {
		cfg.OAuth.ClientID = defaults.OAuth.ClientID
	}
	if cfg.OAuth.Scopes == "" {
		cfg.OAuth.Scopes = defaults.OAuth.Scopes
	}
}

// expandTilde expands a leading ~ or ~/ in a path to the user's home directory.
//
// Expected:
//   - path is a filesystem path that may begin with ~ or ~/.
//
// Returns:
//   - The expanded path, or the original path when no tilde prefix is present.
//
// Side effects:
//   - None.
func expandTilde(path string) string {
	if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return home
	}
	if len(path) > 2 && path[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}

// expandPaths expands tildes in all relevant AppConfig path fields.
//
// Expected:
//   - cfg is a non-nil AppConfig pointer.
//
// Side effects:
//   - Modifies cfg in place.
func expandPaths(cfg *AppConfig) {
	cfg.AgentDir = expandTilde(cfg.AgentDir)
	cfg.SkillDir = expandTilde(cfg.SkillDir)
	cfg.DataDir = expandTilde(cfg.DataDir)
	cfg.Plugins.Dir = expandTilde(cfg.Plugins.Dir)
	for i, dir := range cfg.AgentDirs {
		cfg.AgentDirs[i] = expandTilde(dir)
	}
	cfg.Compression.MicroCompaction.StorageDir = expandTilde(cfg.Compression.MicroCompaction.StorageDir)
	cfg.Compression.SessionMemory.StorageDir = expandTilde(cfg.Compression.SessionMemory.StorageDir)
}
