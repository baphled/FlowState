// Package config loads FlowState application configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// AppConfig holds the complete application configuration.
type AppConfig struct {
	Providers    ProvidersConfig `json:"providers" yaml:"providers"`
	AgentDir     string          `json:"agent_dir" yaml:"agent_dir"`
	SkillDir     string          `json:"skill_dir" yaml:"skill_dir"`
	DataDir      string          `json:"data_dir" yaml:"data_dir"`
	LogLevel     string          `json:"log_level" yaml:"log_level"`
	DefaultAgent string          `json:"default_agent" yaml:"default_agent"`
}

// ProvidersConfig configures all available LLM providers.
type ProvidersConfig struct {
	Default   string         `json:"default" yaml:"default"`
	Ollama    ProviderConfig `json:"ollama" yaml:"ollama"`
	OpenAI    ProviderConfig `json:"openai" yaml:"openai"`
	Anthropic ProviderConfig `json:"anthropic" yaml:"anthropic"`
}

// ProviderConfig holds configuration for a single LLM provider.
type ProviderConfig struct {
	Host   string `json:"host" yaml:"host"`
	APIKey string `json:"api_key" yaml:"api_key"`
	Model  string `json:"model" yaml:"model"`
}

// DefaultConfig returns sensible default configuration values.
//
// Returns:
//   - An AppConfig populated with default provider and directory settings.
//
// Side effects:
//   - Resolves the user home directory to set the data path.
func DefaultConfig() *AppConfig {
	dataDir := ".flowstate"
	if homeDir, err := os.UserHomeDir(); err == nil {
		dataDir = filepath.Join(homeDir, ".flowstate")
	}

	return &AppConfig{
		Providers: ProvidersConfig{
			Default: "ollama",
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
		AgentDir:     filepath.Join(dataDir, "agents"),
		SkillDir:     filepath.Join(dataDir, "skills"),
		DataDir:      dataDir,
		LogLevel:     "info",
		DefaultAgent: "worker",
	}
}

// LoadConfig loads configuration from the default location.
//
// Returns:
//   - An AppConfig loaded from ~/.flowstate/config.yaml.
//   - An error if the home directory cannot be resolved or the file is invalid.
//
// Side effects:
//   - Reads the configuration file from disk.
func LoadConfig() (*AppConfig, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("locating user home directory: %w", err)
	}

	return LoadConfigFromPath(filepath.Join(homeDir, ".flowstate", "config.yaml"))
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
			return DefaultConfig(), nil
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
	return cfg, nil
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
}
