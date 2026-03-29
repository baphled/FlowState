package events

import "time"

// Event is the interface implemented by all event types for the plugin EventBus.
//
// Expected:
//   - EventType returns the event's type string.
//   - Timestamp returns the event's creation time.
//
// Returns: interface for event types.
// Side effects: none.
type Event interface {
	// EventType returns the event's type string.
	// Expected: Returns a string identifying the event type.
	// Returns: event type string.
	// Side effects: none.
	EventType() string
	// Timestamp returns the event's creation time.
	// Expected: Returns the time the event was created.
	// Returns: event creation time.
	// Side effects: none.
	Timestamp() time.Time
}

// BaseEvent provides common fields for all events.
//
// Expected:
//   - Embeddable in all event types.
//   - Stores event type and timestamp.
//
// Returns: struct for embedding in events.
// Side effects: none.
type BaseEvent struct {
	eventType string
	timestamp time.Time
}

// EventType returns the event's type string.
//
// Expected: returns the event type string.
// Returns: event type string.
// Side effects: none.
func (e *BaseEvent) EventType() string { return e.eventType }

// Timestamp returns the event's creation time.
//
// Expected: returns the event creation time.
// Returns: event creation time.
// Side effects: none.
func (e *BaseEvent) Timestamp() time.Time { return e.timestamp }

// SessionEventData holds data for session events.
//
// Expected: used as payload for SessionEvent.
// Returns: struct with session event fields.
// Side effects: none.
type SessionEventData struct {
	SessionID string
	UserID    string
	Action    string
	Details   map[string]any
}

// SessionEvent represents a session-related event.
//
// Expected:
//   - Embeds BaseEvent.
//   - Data contains session event details.
//
// Returns: struct for session events.
// Side effects: none.
type SessionEvent struct {
	BaseEvent
	Data SessionEventData
}

// NewSessionEvent creates a new SessionEvent.
//
// Expected:
//   - Sets eventType to "session".
//   - Sets timestamp to now if zero.
//
// Returns: pointer to new SessionEvent.
// Side effects: none.
func NewSessionEvent(data SessionEventData, ts ...time.Time) *SessionEvent {
	t := time.Now()
	if len(ts) > 0 && !ts[0].IsZero() {
		t = ts[0]
	}
	return &SessionEvent{
		BaseEvent: BaseEvent{eventType: "session", timestamp: t},
		Data:      data,
	}
}

// ToolEventData holds data for tool events.
//
// Expected: used as payload for ToolEvent.
// Returns: struct with tool event fields.
// Side effects: none.
type ToolEventData struct {
	ToolName string
	Args     map[string]any
	Result   any
	Error    error
}

// ToolEvent represents a tool-related event.
//
// Expected:
//   - Embeds BaseEvent.
//   - Data contains tool event details.
//
// Returns: struct for tool events.
// Side effects: none.
type ToolEvent struct {
	BaseEvent
	Data ToolEventData
}

// NewToolEvent creates a new ToolEvent.
//
// Expected:
//   - Sets eventType to "tool".
//   - Sets timestamp to now if zero.
//
// Returns: pointer to new ToolEvent.
// Side effects: none.
func NewToolEvent(data ToolEventData, ts ...time.Time) *ToolEvent {
	t := time.Now()
	if len(ts) > 0 && !ts[0].IsZero() {
		t = ts[0]
	}
	return &ToolEvent{
		BaseEvent: BaseEvent{eventType: "tool", timestamp: t},
		Data:      data,
	}
}

// ProviderEventData holds data for provider events.
//
// Expected: used as payload for ProviderEvent.
// Returns: struct with provider event fields.
// Side effects: none.
type ProviderEventData struct {
	ProviderName string
	Request      any
	Response     any
	Error        error
}

// ProviderEvent represents a provider-related event.
//
// Expected:
//   - Embeds BaseEvent.
//   - Data contains provider event details.
//
// Returns: struct for provider events.
// Side effects: none.
type ProviderEvent struct {
	BaseEvent
	Data ProviderEventData
}

// NewProviderEvent creates a new ProviderEvent.
//
// Expected:
//   - Sets eventType to "provider".
//   - Sets timestamp to now if zero.
//
// Returns: pointer to new ProviderEvent.
// Side effects: none.
func NewProviderEvent(data ProviderEventData, ts ...time.Time) *ProviderEvent {
	t := time.Now()
	if len(ts) > 0 && !ts[0].IsZero() {
		t = ts[0]
	}
	return &ProviderEvent{
		BaseEvent: BaseEvent{eventType: "provider", timestamp: t},
		Data:      data,
	}
}

// Compile-time interface checks.
//
// Expected: ensures event types implement Event interface.
// Returns: none.
// Side effects: none.
var (
	_ Event = (*SessionEvent)(nil)
	_ Event = (*ToolEvent)(nil)
	_ Event = (*ProviderEvent)(nil)
)
