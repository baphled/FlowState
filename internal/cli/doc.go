// Package cli provides the command-line interface for FlowState.
//
// This package implements the Cobra-based CLI, including:
//   - Root command with TUI chat interface
//   - Chat subcommand for direct message interaction
//   - Serve subcommand for HTTP API server
//   - Discover subcommand for agent discovery
//   - Session management subcommands
//
// All commands share configuration via persistent flags and integrate
// with the app package for component initialisation.
package cli
