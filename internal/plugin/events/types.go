// Package events provides event type constants for the FlowState event system.
//
// This file defines named constants for all event types used throughout the FlowState
// agent platform. Use these constants instead of raw string literals when publishing
// or subscribing to events, to ensure consistency and avoid typos.
//
// Event type values MUST NOT be changed, and new event types should only be added
// when new event flows are introduced. This file does not define event payloads—only
// event type names.
//
// Example usage:
//
//	bus.Publish(events.EventAgentSwitched, ...)
//	bus.Subscribe(events.EventProviderError, handler)
//
// The following event types are currently in use:
//   - agent.switched
//   - tool.reasoning
//   - prompt.generated
//   - context.window.built
//   - tool.execute.before
//   - tool.execute.after
//   - tool.execute.error
//   - tool.execute.result
//   - provider.error
//   - provider.request
//   - provider.request.retry
//   - provider.response
//   - session.created
//   - session.ended
//   - session.resumed
//   - provider.rate_limited
//   - plugin.event
//   - background.task.started
//   - background.task.completed
//   - background.task.failed
//   - delegation.started
//   - delegation.completed
//   - delegation.failed
//   - recall.embedding.stored
//   - recall.searched
//   - recall.chain.searched
//   - recall.summarized
//   - context.compacted
//   - discovery.published
//   - learning.recorded
//   - gate.evaluating
//   - gate.passed
//   - gate.failed
//
// Dynamic event types (e.g. "session."+action) are not represented as constants.
package events

// Event type constants for the FlowState event system.
const (
	EventAgentSwitched           = "agent.switched"
	EventToolReasoning           = "tool.reasoning"
	EventPromptGenerated         = "prompt.generated"
	EventContextWindowBuilt      = "context.window.built"
	EventToolExecuteBefore       = "tool.execute.before"
	EventToolExecuteAfter        = "tool.execute.after"
	EventProviderError           = "provider.error"
	EventProviderRequest         = "provider.request"
	EventProviderResponse        = "provider.response"
	EventSessionCreated          = "session.created"
	EventSessionEnded            = "session.ended"
	EventProviderRateLimited     = "provider.rate_limited"
	EventSessionResumed          = "session.resumed"
	EventToolExecuteError        = "tool.execute.error"
	EventToolExecuteResult       = "tool.execute.result"
	EventProviderRequestRetry    = "provider.request.retry"
	EventPluginEvent             = "plugin.event"
	EventBackgroundTaskStarted   = "background.task.started"
	EventBackgroundTaskCompleted = "background.task.completed"
	EventBackgroundTaskFailed    = "background.task.failed"
	EventBackgroundTaskCancelled = "background.task.cancelled"
	EventDelegationStarted       = "delegation.started"
	EventDelegationCompleted     = "delegation.completed"
	EventDelegationFailed        = "delegation.failed"
	EventRecallEmbeddingStored   = "recall.embedding.stored"
	EventRecallSearched          = "recall.searched"
	EventRecallChainSearched     = "recall.chain.searched"
	EventRecallSummarized        = "recall.summarized"
	EventContextCompacted        = "context.compacted"
	EventDiscoveryPublished      = "discovery.published"
	EventLearningRecorded        = "learning.recorded"
	// Plans/Gate Bus Bridge — Engine to SSE and TUI (May 2026): swarm
	// gate lifecycle events. The engine publishes one batch-level
	// `gate.evaluating` and one batch-level `gate.passed` per
	// `swarm.Dispatch` call site (clean batches), or one per-failing-
	// gate `gate.failed` on halt-class failure. Continue-class and
	// warn-class failures are intentionally NOT published; the web
	// surface subscribes to `gate.failed` only.
	EventGateEvaluating = "gate.evaluating"
	EventGatePassed     = "gate.passed"
	EventGateFailed     = "gate.failed"
	// Streaming Coherence — Slice F (May 2026): streaming heartbeat.
	// The engine emits one heartbeat at most every ~15s during a turn
	// so the chat UI's stall watchdog re-arms even when the provider
	// pauses content emission (long thinking, sandboxed tool execution,
	// queued delegation). Forwarded over SSE as `streaming.heartbeat`
	// per the Engine Bus Event Taxonomy ADR's naming + payload shape
	// rules (lower-snake-case dotted variant, payload carries the
	// turn-phase discriminant the frontend's adaptive watchdog reads).
	//
	// Anthropic provider `ping` events MUST be forwarded as this
	// heartbeat rather than silently dropped — the ADR's anti-pattern
	// callout names that explicitly.
	EventStreamingHeartbeat = "streaming.heartbeat"
)
