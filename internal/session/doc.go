// Package session provides session management for FlowState planning sessions.
//
// This package enables programmatic session management without the TUI, including:
//   - Creating and resuming sessions
//   - Tracking conversation history
//   - Managing coordination store state per session
//   - Delegation chain status tracking
//
// Sessions are isolated in-memory units, each with their own coordination store
// for sharing context during delegation chains.
package session
