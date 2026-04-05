package events

import (
	"encoding/json"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

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
// KNOWN DIVERGENCE: EventType() returns "session" but this event is published
// to topic "session.created" or "session.ended" depending on the action.
// Do not change EventType() — it affects serialised JSONL format.
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
	SessionID string
	ToolName  string
	Args      map[string]any
	Result    any
	Error     error
}

// MarshalJSON serialises ToolEventData while preserving error messages.
//
// Expected:
//   - The receiver contains tool event data ready for serialisation.
//
// Returns:
//   - JSON bytes for the event payload.
//   - An error if serialisation fails.
//
// Side effects:
//   - None.
func (d ToolEventData) MarshalJSON() ([]byte, error) {
	type payload struct {
		SessionID string         `json:"session_id,omitempty"`
		ToolName  string         `json:"tool_name"`
		Args      map[string]any `json:"args,omitempty"`
		Result    any            `json:"result,omitempty"`
		Error     string         `json:"error,omitempty"`
	}

	data := payload{
		SessionID: d.SessionID,
		ToolName:  d.ToolName,
		Args:      d.Args,
		Result:    d.Result,
	}
	if d.Error != nil {
		data.Error = d.Error.Error()
	}

	return json.Marshal(data)
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
// KNOWN DIVERGENCE: EventType() returns "tool" but this event is published
// to topic "tool.execute.before" or "tool.execute.after" depending on the phase.
// Do not change EventType() — it affects serialised JSONL format.
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
	SessionID    string
	ProviderName string
	Request      any
	Response     any
	Error        error
}

// MarshalJSON serialises ProviderEventData while preserving error messages.
//
// Expected:
//   - The receiver contains provider event data ready for serialisation.
//
// Returns:
//   - JSON bytes for the event payload.
//   - An error if serialisation fails.
//
// Side effects:
//   - None.
func (d ProviderEventData) MarshalJSON() ([]byte, error) {
	type payload struct {
		SessionID    string `json:"session_id,omitempty"`
		ProviderName string `json:"provider_name"`
		Request      any    `json:"request,omitempty"`
		Response     any    `json:"response,omitempty"`
		Error        string `json:"error,omitempty"`
	}

	data := payload{
		SessionID:    d.SessionID,
		ProviderName: d.ProviderName,
		Request:      d.Request,
		Response:     d.Response,
	}
	if d.Error != nil {
		data.Error = d.Error.Error()
	}

	return json.Marshal(data)
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
// KNOWN DIVERGENCE: EventType() returns "provider" but this event is published
// to topic "provider.rate_limited".
// Do not change EventType() — it affects serialised JSONL format.
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

// ProviderRequestEventData holds data for outbound provider request events.
//
// Expected: used as payload for ProviderRequestEvent.
// Returns: struct with provider request fields.
// Side effects: none.
type ProviderRequestEventData struct {
	SessionID    string
	AgentID      string
	ProviderName string
	ModelName    string
	Request      provider.ChatRequest
}

// ProviderRequestEvent represents an outbound request to a provider.
//
// Expected:
//   - Embeds BaseEvent.
//   - Data contains the full ChatRequest being sent.
//
// Returns: struct for provider request events.
// Side effects: none.
type ProviderRequestEvent struct {
	BaseEvent
	Data ProviderRequestEventData
}

// NewProviderRequestEvent creates a new ProviderRequestEvent.
//
// Expected:
//   - Sets eventType to "provider.request".
//   - Sets timestamp to now if zero.
//
// Returns: pointer to new ProviderRequestEvent.
// Side effects: none.
func NewProviderRequestEvent(data ProviderRequestEventData, ts ...time.Time) *ProviderRequestEvent {
	t := time.Now()
	if len(ts) > 0 && !ts[0].IsZero() {
		t = ts[0]
	}
	return &ProviderRequestEvent{
		BaseEvent: BaseEvent{eventType: "provider.request", timestamp: t},
		Data:      data,
	}
}

// AgentSwitchedEventData holds data for agent switch events.
type AgentSwitchedEventData struct {
	SessionID string
	FromAgent string
	ToAgent   string
}

// AgentSwitchedEvent represents an agent switch event.
type AgentSwitchedEvent struct {
	BaseEvent
	Data AgentSwitchedEventData
}

// NewAgentSwitchedEvent creates a new AgentSwitchedEvent.
//
// Expected:
//   - data contains the agent switch metadata to include in the event.
//   - ts is optional and, when provided, uses the first non-zero timestamp.
//
// Returns:
//   - An AgentSwitchedEvent configured with the supplied data.
//
// Side effects:
//   - Uses the current time when no timestamp override is supplied.
func NewAgentSwitchedEvent(data AgentSwitchedEventData, ts ...time.Time) *AgentSwitchedEvent {
	t := time.Now()
	if len(ts) > 0 && !ts[0].IsZero() {
		t = ts[0]
	}
	return &AgentSwitchedEvent{
		BaseEvent: BaseEvent{eventType: "agent.switched", timestamp: t},
		Data:      data,
	}
}

// PromptEventData holds data for prompt observation events.
//
// Expected: used as payload for PromptEvent.
// Returns: struct with prompt event fields.
// Side effects: none.
type PromptEventData struct {
	SessionID  string
	AgentID    string
	FullPrompt string
	TokenCount int
	Truncated  bool
	Sources    []string
}

// PromptEvent represents a prompt observation event emitted when the system
// prompt is assembled for a model call.
//
// Expected:
//   - Embeds BaseEvent.
//   - Data contains prompt event details.
//
// Returns: struct for prompt events.
// Side effects: none.
type PromptEvent struct {
	BaseEvent
	Data PromptEventData
}

// NewPromptEvent creates a new PromptEvent.
//
// KNOWN DIVERGENCE: EventType() returns "prompt" but this event is published
// to topic "prompt.generated".
// Do not change EventType() — it affects serialised JSONL format.
//
// Expected:
//   - Sets eventType to "prompt".
//   - Sets timestamp to now if not provided.
//
// Returns: pointer to new PromptEvent.
// Side effects: none.
func NewPromptEvent(data PromptEventData, ts ...time.Time) *PromptEvent {
	t := time.Now()
	if len(ts) > 0 && !ts[0].IsZero() {
		t = ts[0]
	}
	return &PromptEvent{
		BaseEvent: BaseEvent{eventType: "prompt", timestamp: t},
		Data:      data,
	}
}

// ContextWindowEventData holds data for context window observation events.
//
// Expected: used as payload for ContextWindowEvent.
// Returns: struct with context window event fields.
// Side effects: none.
type ContextWindowEventData struct {
	SessionID       string
	AgentID         string
	TokenBudget     int
	TokensUsed      int
	BudgetRemaining int
	MessageCount    int
	Truncated       bool
}

// ContextWindowEvent represents a context window state event emitted after
// the context window is built for a model call.
//
// Expected:
//   - Embeds BaseEvent.
//   - Data contains context window event details.
//
// Returns: struct for context window events.
// Side effects: none.
type ContextWindowEvent struct {
	BaseEvent
	Data ContextWindowEventData
}

// NewContextWindowEvent creates a new ContextWindowEvent.
//
// KNOWN DIVERGENCE: EventType() returns "context.window" but this event is published
// to topic "context.window.built".
// Do not change EventType() — it affects serialised JSONL format.
//
// Expected:
//   - Sets eventType to "context.window".
//   - Sets timestamp to now if not provided.
//
// Returns: pointer to new ContextWindowEvent.
// Side effects: none.
func NewContextWindowEvent(data ContextWindowEventData, ts ...time.Time) *ContextWindowEvent {
	t := time.Now()
	if len(ts) > 0 && !ts[0].IsZero() {
		t = ts[0]
	}
	return &ContextWindowEvent{
		BaseEvent: BaseEvent{eventType: "context.window", timestamp: t},
		Data:      data,
	}
}

// ToolReasoningEventData holds data for tool reasoning observation events.
//
// Expected: used as payload for ToolReasoningEvent.
// Returns: struct with tool reasoning event fields.
// Side effects: none.
type ToolReasoningEventData struct {
	SessionID        string
	AgentID          string
	ToolName         string
	ReasoningContent string
}

// ToolReasoningEvent represents a tool reasoning observation event emitted
// when the model produces text output before choosing a tool call.
//
// Expected:
//   - Embeds BaseEvent.
//   - Data contains tool reasoning event details.
//
// Returns: struct for tool reasoning events.
// Side effects: none.
type ToolReasoningEvent struct {
	BaseEvent
	Data ToolReasoningEventData
}

// NewToolReasoningEvent creates a new ToolReasoningEvent.
//
// Expected:
//   - Sets eventType to "tool.reasoning".
//   - Sets timestamp to now if not provided.
//
// Returns: pointer to new ToolReasoningEvent.
// Side effects: none.
func NewToolReasoningEvent(data ToolReasoningEventData, ts ...time.Time) *ToolReasoningEvent {
	t := time.Now()
	if len(ts) > 0 && !ts[0].IsZero() {
		t = ts[0]
	}
	return &ToolReasoningEvent{
		BaseEvent: BaseEvent{eventType: "tool.reasoning", timestamp: t},
		Data:      data,
	}
}

// BackgroundTaskEventData holds data for background task lifecycle events.
type BackgroundTaskEventData struct {
	SessionID string
	TaskID    string
	Name      string
	Status    string // running, completed, failed
	Error     string // non-empty if failed
}

// Event type constants for background task lifecycle.
const (
	EventTypeBackgroundTaskStarted   = "background.task.started"
	EventTypeBackgroundTaskCompleted = "background.task.completed"
	EventTypeBackgroundTaskFailed    = "background.task.failed"
)

// BackgroundTaskStartedEvent represents a background task start event.
type BackgroundTaskStartedEvent struct {
	BaseEvent
	Data BackgroundTaskEventData
}

// NewBackgroundTaskStartedEvent creates a new background task started event.
//
// Expected:
//   - data contains the background task metadata to include in the event.
//   - ts is optional and, when provided, uses the first non-zero timestamp.
//
// Returns:
//   - A BackgroundTaskStartedEvent configured with the supplied data.
//
// Side effects:
//   - Uses the current time when no timestamp override is supplied.
func NewBackgroundTaskStartedEvent(data BackgroundTaskEventData, ts ...time.Time) *BackgroundTaskStartedEvent {
	t := time.Now()
	if len(ts) > 0 && !ts[0].IsZero() {
		t = ts[0]
	}
	return &BackgroundTaskStartedEvent{
		BaseEvent: BaseEvent{eventType: EventTypeBackgroundTaskStarted, timestamp: t},
		Data:      data,
	}
}

// BackgroundTaskCompletedEvent represents a background task completion event.
type BackgroundTaskCompletedEvent struct {
	BaseEvent
	Data BackgroundTaskEventData
}

// NewBackgroundTaskCompletedEvent creates a new background task completed event.
//
// Expected:
//   - data contains the background task metadata to include in the event.
//   - ts is optional and, when provided, uses the first non-zero timestamp.
//
// Returns:
//   - A BackgroundTaskCompletedEvent configured with the supplied data.
//
// Side effects:
//   - Uses the current time when no timestamp override is supplied.
func NewBackgroundTaskCompletedEvent(data BackgroundTaskEventData, ts ...time.Time) *BackgroundTaskCompletedEvent {
	t := time.Now()
	if len(ts) > 0 && !ts[0].IsZero() {
		t = ts[0]
	}
	return &BackgroundTaskCompletedEvent{
		BaseEvent: BaseEvent{eventType: EventTypeBackgroundTaskCompleted, timestamp: t},
		Data:      data,
	}
}

// BackgroundTaskFailedEvent represents a background task failure event.
type BackgroundTaskFailedEvent struct {
	BaseEvent
	Data BackgroundTaskEventData
}

// NewBackgroundTaskFailedEvent creates a new background task failed event.
//
// Expected:
//   - data contains the background task metadata to include in the event.
//   - ts is optional and, when provided, uses the first non-zero timestamp.
//
// Returns:
//   - A BackgroundTaskFailedEvent configured with the supplied data.
//
// Side effects:
//   - Uses the current time when no timestamp override is supplied.
func NewBackgroundTaskFailedEvent(data BackgroundTaskEventData, ts ...time.Time) *BackgroundTaskFailedEvent {
	t := time.Now()
	if len(ts) > 0 && !ts[0].IsZero() {
		t = ts[0]
	}
	return &BackgroundTaskFailedEvent{
		BaseEvent: BaseEvent{eventType: EventTypeBackgroundTaskFailed, timestamp: t},
		Data:      data,
	}
}

// ProviderResponseEventData holds data for provider response events emitted
// when a streaming provider call completes successfully.
//
// Expected: used as payload for ProviderResponseEvent.
// Returns: struct with provider response fields.
// Side effects: none.
type ProviderResponseEventData struct {
	SessionID       string
	AgentID         string
	ProviderName    string
	ModelName       string
	ResponseContent string
	ToolCalls       int
	DurationMS      int64
}

// ProviderResponseEvent represents a completed provider response event.
//
// Expected:
//   - Embeds BaseEvent.
//   - Data contains the provider response details.
//
// Returns: struct for provider response events.
// Side effects: none.
type ProviderResponseEvent struct {
	BaseEvent
	Data ProviderResponseEventData
}

// NewProviderResponseEvent creates a new ProviderResponseEvent.
//
// Expected:
//   - data contains the provider response metadata to include in the event.
//   - ts is optional and, when provided, uses the first non-zero timestamp.
//
// Returns:
//   - A ProviderResponseEvent configured with the supplied data.
//
// Side effects:
//   - Uses the current time when no timestamp override is supplied.
func NewProviderResponseEvent(data ProviderResponseEventData, ts ...time.Time) *ProviderResponseEvent {
	t := time.Now()
	if len(ts) > 0 && !ts[0].IsZero() {
		t = ts[0]
	}
	return &ProviderResponseEvent{
		BaseEvent: BaseEvent{eventType: "provider.response", timestamp: t},
		Data:      data,
	}
}

// ProviderErrorEventData holds data for provider error events emitted
// when a provider call fails during streaming or failover.
//
// Expected: used as payload for ProviderErrorEvent.
// Returns: struct with provider error fields.
// Side effects: none.
type ProviderErrorEventData struct {
	SessionID    string
	AgentID      string
	ProviderName string
	ModelName    string
	Error        error
	Phase        string
}

// MarshalJSON serialises ProviderErrorEventData while preserving error messages.
//
// Expected:
//   - The receiver contains provider error data ready for serialisation.
//
// Returns:
//   - JSON bytes for the event payload.
//   - An error if serialisation fails.
//
// Side effects:
//   - None.
func (d ProviderErrorEventData) MarshalJSON() ([]byte, error) {
	type payload struct {
		SessionID    string `json:"session_id,omitempty"`
		AgentID      string `json:"agent_id,omitempty"`
		ProviderName string `json:"provider_name"`
		ModelName    string `json:"model_name,omitempty"`
		Error        string `json:"error,omitempty"`
		Phase        string `json:"phase,omitempty"`
	}

	data := payload{
		SessionID:    d.SessionID,
		AgentID:      d.AgentID,
		ProviderName: d.ProviderName,
		ModelName:    d.ModelName,
		Phase:        d.Phase,
	}
	if d.Error != nil {
		data.Error = d.Error.Error()
	}

	return json.Marshal(data)
}

// ProviderErrorEvent represents a provider error event.
//
// Expected:
//   - Embeds BaseEvent.
//   - Data contains the provider error details.
//
// Returns: struct for provider error events.
// Side effects: none.
type ProviderErrorEvent struct {
	BaseEvent
	Data ProviderErrorEventData
}

// NewProviderErrorEvent creates a new ProviderErrorEvent.
//
// Expected:
//   - data contains the provider error metadata to include in the event.
//   - ts is optional and, when provided, uses the first non-zero timestamp.
//
// Returns:
//   - A ProviderErrorEvent configured with the supplied data.
//
// Side effects:
//   - Uses the current time when no timestamp override is supplied.
func NewProviderErrorEvent(data ProviderErrorEventData, ts ...time.Time) *ProviderErrorEvent {
	t := time.Now()
	if len(ts) > 0 && !ts[0].IsZero() {
		t = ts[0]
	}
	return &ProviderErrorEvent{
		BaseEvent: BaseEvent{eventType: "provider.error", timestamp: t},
		Data:      data,
	}
}

// SessionResumedEventData holds data for session resumed events.
type SessionResumedEventData struct {
	SessionID string
	UserID    string
	Action    string
	Details   map[string]any
}

// SessionResumedEvent represents a session resumed event.
type SessionResumedEvent struct {
	BaseEvent
	Data SessionResumedEventData `json:"data"`
}

// NewSessionResumedEvent creates a new SessionResumedEvent.
//
// Expected:
//   - data contains the session resumed metadata to include in the event.
//   - ts is optional and, when provided, uses the first non-zero timestamp.
//
// Returns:
//   - A SessionResumedEvent configured with the supplied data.
//
// Side effects:
//   - Uses the current time when no timestamp override is supplied.
func NewSessionResumedEvent(data SessionResumedEventData, ts ...time.Time) *SessionResumedEvent {
	t := time.Now()
	if len(ts) > 0 && !ts[0].IsZero() {
		t = ts[0]
	}
	return &SessionResumedEvent{
		BaseEvent: BaseEvent{eventType: EventSessionResumed, timestamp: t},
		Data:      data,
	}
}

// ToolExecuteErrorEventData holds data for tool execution error events.
type ToolExecuteErrorEventData struct {
	SessionID string
	ToolName  string
	Args      map[string]any
	Error     error
}

// MarshalJSON serialises ToolExecuteErrorEventData while preserving error messages.
//
// Expected:
//   - The receiver contains tool execution error data ready for serialisation.
//
// Returns:
//   - JSON bytes for the event payload.
//   - An error if serialisation fails.
//
// Side effects:
//   - None.
func (d ToolExecuteErrorEventData) MarshalJSON() ([]byte, error) {
	type payload struct {
		SessionID string         `json:"session_id,omitempty"`
		ToolName  string         `json:"tool_name"`
		Args      map[string]any `json:"args,omitempty"`
		Error     string         `json:"error,omitempty"`
	}
	data := payload{SessionID: d.SessionID, ToolName: d.ToolName, Args: d.Args}
	if d.Error != nil {
		data.Error = d.Error.Error()
	}
	return json.Marshal(data)
}

// ToolExecuteErrorEvent represents a tool execution error event.
type ToolExecuteErrorEvent struct {
	BaseEvent
	Data ToolExecuteErrorEventData
}

// NewToolExecuteErrorEvent creates a new ToolExecuteErrorEvent.
//
// Expected:
//   - data contains the tool execution error metadata to include in the event.
//   - ts is optional and, when provided, uses the first non-zero timestamp.
//
// Returns:
//   - A ToolExecuteErrorEvent configured with the supplied data.
//
// Side effects:
//   - Uses the current time when no timestamp override is supplied.
func NewToolExecuteErrorEvent(data ToolExecuteErrorEventData, ts ...time.Time) *ToolExecuteErrorEvent {
	t := time.Now()
	if len(ts) > 0 && !ts[0].IsZero() {
		t = ts[0]
	}
	return &ToolExecuteErrorEvent{
		BaseEvent: BaseEvent{eventType: EventToolExecuteError, timestamp: t},
		Data:      data,
	}
}

// ToolExecuteResultEventData holds data for tool execution result events.
type ToolExecuteResultEventData struct {
	SessionID string         `json:"session_id,omitempty"`
	ToolName  string         `json:"tool_name"`
	Args      map[string]any `json:"args,omitempty"`
	Result    any            `json:"result,omitempty"`
}

// ToolExecuteResultEvent represents a tool execution result event.
type ToolExecuteResultEvent struct {
	BaseEvent
	Data ToolExecuteResultEventData
}

// NewToolExecuteResultEvent creates a new ToolExecuteResultEvent.
//
// Expected:
//   - data contains the tool execution result metadata to include in the event.
//   - ts is optional and, when provided, uses the first non-zero timestamp.
//
// Returns:
//   - A ToolExecuteResultEvent configured with the supplied data.
//
// Side effects:
//   - Uses the current time when no timestamp override is supplied.
func NewToolExecuteResultEvent(data ToolExecuteResultEventData, ts ...time.Time) *ToolExecuteResultEvent {
	t := time.Now()
	if len(ts) > 0 && !ts[0].IsZero() {
		t = ts[0]
	}
	return &ToolExecuteResultEvent{
		BaseEvent: BaseEvent{eventType: EventToolExecuteResult, timestamp: t},
		Data:      data,
	}
}

// ProviderRequestRetryEventData holds data for provider request retry events.
type ProviderRequestRetryEventData struct {
	SessionID    string `json:"session_id,omitempty"`
	AgentID      string `json:"agent_id,omitempty"`
	ProviderName string `json:"provider_name"`
	ModelName    string `json:"model_name,omitempty"`
	Reason       string `json:"reason,omitempty"`
	Attempt      int    `json:"attempt"`
}

// ProviderRequestRetryEvent represents a provider request retry event.
type ProviderRequestRetryEvent struct {
	BaseEvent
	Data ProviderRequestRetryEventData
}

// NewProviderRequestRetryEvent creates a new ProviderRequestRetryEvent.
//
// Expected:
//   - data contains the provider request retry metadata to include in the event.
//   - ts is optional and, when provided, uses the first non-zero timestamp.
//
// Returns:
//   - A ProviderRequestRetryEvent configured with the supplied data.
//
// Side effects:
//   - Uses the current time when no timestamp override is supplied.
func NewProviderRequestRetryEvent(data ProviderRequestRetryEventData, ts ...time.Time) *ProviderRequestRetryEvent {
	t := time.Now()
	if len(ts) > 0 && !ts[0].IsZero() {
		t = ts[0]
	}
	return &ProviderRequestRetryEvent{
		BaseEvent: BaseEvent{eventType: EventProviderRequestRetry, timestamp: t},
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
	_ Event = (*PromptEvent)(nil)
	_ Event = (*ContextWindowEvent)(nil)
	_ Event = (*ToolReasoningEvent)(nil)
	_ Event = (*ProviderRequestEvent)(nil)
	_ Event = (*ProviderResponseEvent)(nil)
	_ Event = (*ProviderErrorEvent)(nil)
	_ Event = (*AgentSwitchedEvent)(nil)
	_ Event = (*BackgroundTaskStartedEvent)(nil)
	_ Event = (*BackgroundTaskCompletedEvent)(nil)
	_ Event = (*BackgroundTaskFailedEvent)(nil)
	_ Event = (*SessionResumedEvent)(nil)
	_ Event = (*ToolExecuteErrorEvent)(nil)
	_ Event = (*ToolExecuteResultEvent)(nil)
	_ Event = (*ProviderRequestRetryEvent)(nil)
)
