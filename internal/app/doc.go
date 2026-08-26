// Package app provides the main application container and initialisation.
//
// This package is responsible for:
//   - Wiring together all FlowState components (providers, tools, agents, etc.)
//   - Managing application lifecycle and configuration
//   - Providing a unified entry point for CLI and API usage
//
// The App struct serves as the dependency injection container, holding
// references to all initialised subsystems including the engine, registries,
// and session stores.
package app
