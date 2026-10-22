// Package streaming provides the producer and consumer contracts for all FlowState streaming paths.
//
// This package defines:
//   - Streamer: the producer interface satisfied by engine.Engine and HarnessStreamer
//   - StreamConsumer: the consumer strategy interface for CLI, HTTP, and TUI adapters
//   - Run: the coordinator that drives a Streamer into a StreamConsumer
//   - HarnessStreamer: a decorator that routes planner agent requests through Harness
//   - SessionContextStreamer: a decorator that injects the session ID into the streaming context
package streaming
