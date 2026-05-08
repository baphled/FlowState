package api

import (
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
)

// busHandler ties an event type to its handler function.
type busHandler struct {
	eventType string
	handler   eventbus.EventHandler
}

// subscribeSessionBus subscribes to relevant EventBus events for a specific session,
// forwarding sanitised summaries to the provided channel.
//
// Expected: sessionID is the active API session; out is a buffered WSChunkMsg channel.
// Returns: unsubscribe function — call it via defer in the WebSocket handler.
// Side effects: subscribes to EventToolExecuteBefore, EventToolExecuteResult, EventToolExecuteError, EventProviderRateLimited.
func (s *Server) subscribeSessionBus(sessionID string, out chan<- WSChunkMsg) func() {
	if s.eventBus == nil {
		return func() {}
	}

	handlers := []busHandler{
		{eventType: events.EventToolExecuteBefore, handler: newToolBeforeHandler(sessionID, out)},
		{eventType: events.EventToolExecuteResult, handler: newToolResultHandler(sessionID, out)},
		{eventType: events.EventToolExecuteError, handler: newToolErrorHandler(sessionID, out)},
		{eventType: events.EventProviderRateLimited, handler: newRateLimitHandler(out)},
		{eventType: events.EventBackgroundTaskStarted, handler: newBackgroundTaskHandler(sessionID, out)},
		{eventType: events.EventBackgroundTaskCompleted, handler: newBackgroundTaskHandler(sessionID, out)},
		{eventType: events.EventBackgroundTaskFailed, handler: newBackgroundTaskHandler(sessionID, out)},
		{eventType: events.EventContextCompacted, handler: newContextCompactedHandler(sessionID, out)},
	}

	for _, h := range handlers {
		s.eventBus.Subscribe(h.eventType, h.handler)
	}

	return func() {
		for _, h := range handlers {
			s.eventBus.Unsubscribe(h.eventType, h.handler)
		}
	}
}

// newToolBeforeHandler creates an EventHandler that forwards sanitised tool.execute.before
// events to the out channel when the session ID matches.
//
// Expected: sessionID is the connection's session; out accepts WSChunkMsg values.
// Returns: an eventbus.EventHandler for tool.execute.before events.
// Side effects: sends to out channel on matching events.
func newToolBeforeHandler(sessionID string, out chan<- WSChunkMsg) eventbus.EventHandler {
	return func(msg any) {
		te, ok := msg.(*events.ToolEvent)
		if !ok || te.Data.SessionID != sessionID {
			return
		}
		select {
		case out <- WSChunkMsg{EventType: events.EventToolExecuteBefore, EventData: map[string]string{
			"event_type": events.EventToolExecuteBefore,
			"tool_name":  te.Data.ToolName,
		}}:
		default:
		}
	}
}

// newToolResultHandler creates an EventHandler that forwards sanitised tool.execute.result
// events to the out channel when the session ID matches.
//
// Expected: sessionID is the connection's session; out accepts WSChunkMsg values.
// Returns: an eventbus.EventHandler for tool.execute.result events.
// Side effects: sends to out channel on matching events.
func newToolResultHandler(sessionID string, out chan<- WSChunkMsg) eventbus.EventHandler {
	return func(msg any) {
		te, ok := msg.(*events.ToolExecuteResultEvent)
		if !ok || te.Data.SessionID != sessionID {
			return
		}
		sanitised := map[string]any{
			"event_type": events.EventToolExecuteResult,
			"tool_name":  te.Data.ToolName,
			"ok":         true,
		}
		select {
		case out <- WSChunkMsg{EventType: events.EventToolExecuteResult, EventData: sanitised}:
		default:
		}
	}
}

// newToolErrorHandler creates an EventHandler that forwards sanitised tool.execute.error
// events to the out channel when the session ID matches.
//
// Expected: sessionID is the connection's session; out accepts WSChunkMsg values.
// Returns: an eventbus.EventHandler for tool.execute.error events.
// Side effects: sends to out channel on matching events.
func newToolErrorHandler(sessionID string, out chan<- WSChunkMsg) eventbus.EventHandler {
	return func(msg any) {
		te, ok := msg.(*events.ToolExecuteErrorEvent)
		if !ok || te.Data.SessionID != sessionID {
			return
		}
		sanitised := map[string]any{
			"event_type": events.EventToolExecuteError,
			"tool_name":  te.Data.ToolName,
			"ok":         false,
		}
		select {
		case out <- WSChunkMsg{EventType: events.EventToolExecuteError, EventData: sanitised}:
		default:
		}
	}
}

// newRateLimitHandler creates an EventHandler that forwards provider.rate_limited
// events to all connected WebSocket clients. Rate-limit events are provider-wide
// and not session-scoped.
//
// Expected: out accepts WSChunkMsg values.
// Returns: an eventbus.EventHandler for provider.rate_limited events.
// Side effects: sends to out channel on matching events.
func newRateLimitHandler(out chan<- WSChunkMsg) eventbus.EventHandler {
	return func(msg any) {
		pe, ok := msg.(*events.ProviderEvent)
		if !ok {
			return
		}
		select {
		case out <- WSChunkMsg{EventType: events.EventProviderRateLimited, EventData: map[string]string{
			"event_type": events.EventProviderRateLimited,
			"provider":   pe.Data.ProviderName,
		}}:
		default:
		}
	}
}

// newBackgroundTaskHandler creates an EventHandler that forwards background task
// lifecycle events (started, completed, failed) to the out channel when the
// session ID matches. This enables WebSocket clients to receive real-time
// notifications about background task progress.
//
// Expected:
//   - sessionID is the connection's session.
//   - out accepts WSChunkMsg values.
//
// Returns:
//   - An eventbus.EventHandler for background task events.
//
// Side effects:
//   - Sends to out channel on matching events.
func newBackgroundTaskHandler(sessionID string, out chan<- WSChunkMsg) eventbus.EventHandler {
	return func(msg any) {
		var data events.BackgroundTaskEventData
		var eventType string

		switch e := msg.(type) {
		case *events.BackgroundTaskStartedEvent:
			data = e.Data
			eventType = events.EventBackgroundTaskStarted
		case *events.BackgroundTaskCompletedEvent:
			data = e.Data
			eventType = events.EventBackgroundTaskCompleted
		case *events.BackgroundTaskFailedEvent:
			data = e.Data
			eventType = events.EventBackgroundTaskFailed
		default:
			return
		}

		if data.SessionID != sessionID {
			return
		}

		sanitised := map[string]any{
			"event_type": eventType,
			"task_id":    data.TaskID,
			"name":       data.Name,
			"status":     data.Status,
		}
		if data.Error != "" {
			sanitised["error"] = data.Error
		}

		select {
		case out <- WSChunkMsg{EventType: eventType, EventData: sanitised}:
		default:
		}
	}
}

// newContextCompactedHandler creates an EventHandler that forwards
// EventContextCompacted bus events to the out channel when the session
// ID matches. Slice 6a of the Phase-4 follow-ups bridges the engine's
// auto-compaction telemetry onto the wire so Vue clients can render a
// compaction affordance (Slice 6b consumes this on the chip).
//
// The sanitised payload mirrors the engine's
// pluginevents.ContextCompactedEventData fields (session_id, agent_id,
// original_tokens, summary_tokens, latency_ms) plus the canonical
// `event_type` discriminant the SSE writer emits as
// `"type":"context_compacted"`.
//
// Expected:
//   - sessionID is the connection's session.
//   - out accepts WSChunkMsg values.
//
// Returns:
//   - An eventbus.EventHandler for context.compacted events.
//
// Side effects:
//   - Sends to out channel on matching events.
//   - Drops the event on a full out channel rather than blocking the
//     bus dispatcher (matches the existing pattern across this file).
func newContextCompactedHandler(sessionID string, out chan<- WSChunkMsg) eventbus.EventHandler {
	return func(msg any) {
		ce, ok := msg.(*events.ContextCompactedEvent)
		if !ok || ce.Data.SessionID != sessionID {
			return
		}
		sanitised := map[string]any{
			"event_type":      events.EventContextCompacted,
			"session_id":      ce.Data.SessionID,
			"agent_id":        ce.Data.AgentID,
			"original_tokens": ce.Data.OriginalTokens,
			"summary_tokens":  ce.Data.SummaryTokens,
			"latency_ms":      ce.Data.LatencyMS,
		}
		select {
		case out <- WSChunkMsg{EventType: events.EventContextCompacted, EventData: sanitised}:
		default:
		}
	}
}
