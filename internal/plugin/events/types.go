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
//
// Dynamic event types (e.g. "session."+action) are not represented as constants.
package events

// Event type constants for the FlowState event system.
const (
	EventAgentSwitched        = "agent.switched"
	EventToolReasoning        = "tool.reasoning"
	EventPromptGenerated      = "prompt.generated"
	EventContextWindowBuilt   = "context.window.built"
	EventToolExecuteBefore    = "tool.execute.before"
	EventToolExecuteAfter     = "tool.execute.after"
	EventProviderError        = "provider.error"
	EventProviderRequest      = "provider.request"
	EventProviderResponse     = "provider.response"
	EventSessionCreated       = "session.created"
	EventSessionEnded         = "session.ended"
	EventProviderRateLimited  = "provider.rate_limited"
	EventSessionResumed       = "session.resumed"
	EventToolExecuteError     = "tool.execute.error"
	EventToolExecuteResult    = "tool.execute.result"
	EventProviderRequestRetry = "provider.request.retry"
	EventPluginEvent          = "plugin.event"

	EventBackgroundTaskStarted   = "background.task.started"
	EventBackgroundTaskCompleted = "background.task.completed"
	EventBackgroundTaskFailed    = "background.task.failed"
)
