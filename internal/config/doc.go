// Package config loads and manages FlowState application configuration.
//
// This package handles:
//   - Loading configuration from YAML files
//   - Provider configuration (Ollama, OpenAI, Anthropic)
//   - Directory paths for agents, skills, and data
//   - Default agent and logging settings
//
// Configuration is loaded from ~/.config/flowstate/config.yaml by default,
// with support for environment variable overrides.
package config
