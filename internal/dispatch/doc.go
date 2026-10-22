// Package dispatch hosts the canonical user-message to engine-stream service
// mediating every FlowState entry point.
//
// This package handles:
//   - Mediating between the API, WebSocket handler, and CLI entry points
//   - Enforcing the sync/async dichotomy via compile-time-distinct handle types
//   - Session-anchored dispatch via DispatchSession and ephemeral flows via DispatchEphemeral
//   - Queuing session prompts behind in-flight turns to prevent race conditions
package dispatch
