// Package config provides application configuration loading and management.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// AppConfig represents the FlowState application configuration.
type AppConfig struct {
	Providers ProviderConfig `yaml:"providers"`
	AgentDir  string         `yaml:"agent_dir"`
	SkillDir  string         `yaml:"skill_dir"`
	DataDir   string         `yaml:"data_dir"`
	LogLevel  string         `yaml:"log_level"`
}

// ProviderConfig contains provider-related settings.
type ProviderConfig struct {
	Default   string          `yaml:"default"`
	Ollama    OllamaConfig    `yaml:"ollama"`
	OpenAI    OpenAIConfig    `yaml:"openai"`
	Anthropic AnthropicConfig `yaml:"anthropic"`
}

// OllamaConfig contains Ollama provider settings.
type OllamaConfig struct {
	Host  string `yaml:"host"`
	Model string `yaml:"model"`
}

// OpenAIConfig contains OpenAI provider settings.
type OpenAIConfig struct {
	APIKey string `yaml:"api_key"`
	Model  string `yaml:"model"`
}

// AnthropicConfig contains Anthropic provider settings.
type AnthropicConfig struct {
	APIKey string `yaml:"api_key"`
	Model  string `yaml:"model"`
}

// DefaultConfig returns the default configuration with sensible defaults.
func DefaultConfig() *AppConfig {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}
	dataDir := filepath.Join(homeDir, ".flowstate")

	return &AppConfig{
		Providers: ProviderConfig{
			Default: "ollama",
			Ollama: OllamaConfig{
				Host:  "http://localhost:11434",
				Model: "llama3.2",
			},
			OpenAI: OpenAIConfig{
				Model: "gpt-4o",
			},
			Anthropic: AnthropicConfig{
				Model: "claude-sonnet-4-20250514",
			},
		},
		AgentDir: filepath.Join(dataDir, "agents"),
		SkillDir: filepath.Join(dataDir, "skills"),
		DataDir:  dataDir,
		LogLevel: "info",
	}
}

// LoadConfig loads configuration from the default location.
func LoadConfig() (*AppConfig, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home directory: %w", err)
	}
	path := filepath.Join(homeDir, ".flowstate", "config.yaml")
	return LoadConfigFromPath(path)
}

// LoadConfigFromPath loads configuration from a specific file path.
// Returns default config if the file does not exist.
func LoadConfigFromPath(path string) (*AppConfig, error) {
	cleanPath := filepath.Clean(path)

	if _, err := os.Stat(cleanPath); os.IsNotExist(err) {
		return DefaultConfig(), nil
	}

	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg AppConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config YAML: %w", err)
	}

	applyDefaults(&cfg)
	return &cfg, nil
}

// applyDefaults fills in default values for any zero-value fields.
func applyDefaults(cfg *AppConfig) {
	defaults := DefaultConfig()

	if cfg.Providers.Default == "" {
		cfg.Providers.Default = defaults.Providers.Default
	}
	if cfg.Providers.Ollama.Host == "" {
		cfg.Providers.Ollama.Host = defaults.Providers.Ollama.Host
	}
	if cfg.Providers.Ollama.Model == "" {
		cfg.Providers.Ollama.Model = defaults.Providers.Ollama.Model
	}
	if cfg.Providers.OpenAI.Model == "" {
		cfg.Providers.OpenAI.Model = defaults.Providers.OpenAI.Model
	}
	if cfg.Providers.Anthropic.Model == "" {
		cfg.Providers.Anthropic.Model = defaults.Providers.Anthropic.Model
	}
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
}
