// Package api provides the HTTP API server with SSE streaming support.
//
// This package implements the FlowState HTTP API, including:
//   - Chat endpoints with server-sent events for streaming responses
//   - Agent discovery and listing endpoints
//   - Session management endpoints
//   - Skill listing endpoints
//   - WebSocket session endpoints with EventBus event forwarding
//
// The server is designed to integrate with the engine package for
// orchestrating AI interactions and the context package for session
// persistence.
package api
