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
// Side effects: subscribes to tool.execute.before, tool.execute.after, provider.rate_limited.
func (s *Server) subscribeSessionBus(sessionID string, out chan<- WSChunkMsg) func() {
	if s.eventBus == nil {
		return func() {}
	}

	handlers := []busHandler{
		{eventType: "tool.execute.before", handler: newToolBeforeHandler(sessionID, out)},
		{eventType: "tool.execute.after", handler: newToolAfterHandler(sessionID, out)},
		{eventType: "provider.rate_limited", handler: newRateLimitHandler(sessionID, out)},
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
		case out <- WSChunkMsg{EventType: "tool.execute.before", EventData: map[string]string{
			"event_type": "tool.execute.before",
			"tool_name":  te.Data.ToolName,
		}}:
		default:
		}
	}
}

// newToolAfterHandler creates an EventHandler that forwards sanitised tool.execute.after
// events to the out channel when the session ID matches.
//
// Expected: sessionID is the connection's session; out accepts WSChunkMsg values.
// Returns: an eventbus.EventHandler for tool.execute.after events.
// Side effects: sends to out channel on matching events.
func newToolAfterHandler(sessionID string, out chan<- WSChunkMsg) eventbus.EventHandler {
	return func(msg any) {
		te, ok := msg.(*events.ToolEvent)
		if !ok || te.Data.SessionID != sessionID {
			return
		}
		sanitised := map[string]any{
			"event_type": "tool.execute.after",
			"tool_name":  te.Data.ToolName,
			"ok":         te.Data.Error == nil,
		}
		select {
		case out <- WSChunkMsg{EventType: "tool.execute.after", EventData: sanitised}:
		default:
		}
	}
}

// newRateLimitHandler creates an EventHandler that forwards sanitised provider.rate_limited
// events to the out channel when the session ID matches.
//
// Expected: sessionID is the connection's session; out accepts WSChunkMsg values.
// Returns: an eventbus.EventHandler for provider.rate_limited events.
// Side effects: sends to out channel on matching events.
func newRateLimitHandler(sessionID string, out chan<- WSChunkMsg) eventbus.EventHandler {
	return func(msg any) {
		pe, ok := msg.(*events.ProviderEvent)
		if !ok || pe.Data.SessionID != sessionID {
			return
		}
		select {
		case out <- WSChunkMsg{EventType: "provider.rate_limited", EventData: map[string]string{
			"event_type": "provider.rate_limited",
			"provider":   pe.Data.ProviderName,
		}}:
		default:
		}
	}
}
