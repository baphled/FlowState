// Package engine orchestrates AI agent interactions with providers and tools.
//
// This package is the core execution layer for FlowState, handling:
//   - Chat and streaming interactions with LLM providers
//   - Tool execution and result injection
//   - Context window management
//   - Skill loading and application
//   - Provider failback chains
//
// The engine coordinates between the provider, tool, context, and skill
// subsystems to deliver coherent AI assistant functionality.
package engine
