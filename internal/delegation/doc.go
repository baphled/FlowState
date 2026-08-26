// Package delegation provides async agent delegation primitives for FlowState.
//
// This package handles the runtime mechanics of delegating work between agents, including:
//   - Handoff schema for passing context between coordinator and sub-agents
//   - Circuit breaker for limiting reject-regenerate cycles (max 3 attempts)
//   - Async delegation lifecycle management
package delegation
