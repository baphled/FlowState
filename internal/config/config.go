// Package config loads FlowState application configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type AppConfig struct {
	Providers    ProvidersConfig `json:"providers" yaml:"providers"`
	AgentDir     string          `json:"agent_dir" yaml:"agent_dir"`
	SkillDir     string          `json:"skill_dir" yaml:"skill_dir"`
	DataDir      string          `json:"data_dir" yaml:"data_dir"`
	LogLevel     string          `json:"log_level" yaml:"log_level"`
	DefaultAgent string          `json:"default_agent" yaml:"default_agent"`
}

type ProvidersConfig struct {
	Default   string         `json:"default" yaml:"default"`
	Ollama    ProviderConfig `json:"ollama" yaml:"ollama"`
	OpenAI    ProviderConfig `json:"openai" yaml:"openai"`
	Anthropic ProviderConfig `json:"anthropic" yaml:"anthropic"`
}

type ProviderConfig struct {
	Host   string `json:"host" yaml:"host"`
	APIKey string `json:"api_key" yaml:"api_key"`
	Model  string `json:"model" yaml:"model"`
}

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

func LoadConfig() (*AppConfig, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("locating user home directory: %w", err)
	}

	return LoadConfigFromPath(filepath.Join(homeDir, ".flowstate", "config.yaml"))
}

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
