// Package tracer provides the observability layer for FlowState.
//
// It exposes three components:
//   - Hook: a hook.Hook implementation that wraps each provider call
//     with timing, message count, and error logging via slog.
//   - TracingProvider: a provider.Provider decorator that records per-method
//     latency and error status for Stream, Chat, and Embed calls.
//   - Recorder: a provider-agnostic interface for emitting metrics (retry
//     counts, validation scores, critic results, provider latency).
package tracer
