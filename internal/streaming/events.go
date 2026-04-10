package streaming

import (
	"encoding/json"
	"fmt"
	"time"
)

// Event type constants identify each discrete event kind within the streaming pipeline.
const (
	EventTypeTextChunk              = "text_chunk"
	EventTypeToolCall               = "tool_call"
	EventTypeDelegation             = "delegation"
	EventTypeProgress               = "progress"
	EventTypeCompletionNotification = "completion_notification"
	EventTypeCoordinationStore      = "coordination_store"
	EventTypeStatusTransition       = "status_transition"
	EventTypePlanArtifact           = "plan_artifact"
	EventTypeReviewVerdict          = "review_verdict"
	EventTypeRecallSearch           = "recall_search"
	EventTypeRecallChainSearch      = "recall_chain_search"
	EventTypeRecallSummarized       = "recall_summarized"
	EventTypeLearningTriggered      = "learning_triggered"
)

// Event represents a discrete typed occurrence within the streaming pipeline.
type Event interface {
	// Type returns the unique string identifier for this event kind.
	Type() string
}

// EventConsumer is an optional interface for consumers that support typed event delivery.
type EventConsumer interface {
	// WriteEvent delivers a typed event to the consumer.
	WriteEvent(Event) error
}

// TextChunkEvent represents raw LLM text output.
type TextChunkEvent struct {
	Content string `json:"content"`
	AgentID string `json:"agentId"`
}

// Type returns the event type identifier for text chunk events.
//
// Expected:
//   - None.
//
// Returns:
//   - The event type identifier.
//
// Side effects:
//   - None.
func (e TextChunkEvent) Type() string { return EventTypeTextChunk }

// MarshalJSON serialises a TextChunkEvent with a type discriminator field.
//
// Expected:
//   - e contains the event payload.
//
// Returns:
//   - JSON bytes containing the event fields plus a "type" discriminator.
//   - An error if serialisation fails.
//
// Side effects:
//   - None.
func (e TextChunkEvent) MarshalJSON() ([]byte, error) {
	type alias TextChunkEvent
	return marshalWithType(e.Type(), alias(e))
}

// ToolCallEvent represents a tool invocation with its arguments, result, and duration.
type ToolCallEvent struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
	Result    string                 `json:"result"`
	Duration  time.Duration          `json:"duration"`
	AgentID   string                 `json:"agentId"`
}

// Type returns the event type identifier for tool call events.
//
// Expected:
//   - None.
//
// Returns:
//   - The event type identifier.
//
// Side effects:
//   - None.
func (e ToolCallEvent) Type() string { return EventTypeToolCall }

// MarshalJSON serialises a ToolCallEvent with a type discriminator field.
//
// Expected:
//   - e contains the event payload.
//
// Returns:
//   - JSON bytes containing the event fields plus a "type" discriminator.
//   - An error if serialisation fails.
//
// Side effects:
//   - None.
func (e ToolCallEvent) MarshalJSON() ([]byte, error) {
	type alias ToolCallEvent
	return marshalWithType(e.Type(), alias(e))
}

// DelegationEvent represents a delegation action between agents, including
// visibility metadata such as the model, provider, and progress indicators.
type DelegationEvent struct {
	SourceAgent  string     `json:"source_agent"`
	TargetAgent  string     `json:"target_agent"`
	ChainID      string     `json:"chain_id"`
	Status       string     `json:"status"`
	ModelName    string     `json:"model_name"`
	ProviderName string     `json:"provider_name"`
	Description  string     `json:"description"`
	ToolCalls    int        `json:"tool_calls"`
	LastTool     string     `json:"last_tool"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

// Type returns the event type identifier for delegation events.
//
// Expected:
//   - None.
//
// Returns:
//   - The event type identifier.
//
// Side effects:
//   - None.
func (e DelegationEvent) Type() string { return EventTypeDelegation }

// MarshalJSON serialises a DelegationEvent with a type discriminator field.
//
// Expected:
//   - e contains the event payload.
//
// Returns:
//   - JSON bytes containing the event fields plus a "type" discriminator.
//   - An error if serialisation fails.
//
// Side effects:
//   - None.
func (e DelegationEvent) MarshalJSON() ([]byte, error) {
	type alias DelegationEvent
	return marshalWithType(e.Type(), alias(e))
}

// ProgressEvent represents observable progress for a delegated task.
type ProgressEvent struct {
	TaskID            string        `json:"task_id"`
	ToolCallCount     int           `json:"tool_call_count"`
	LastTool          string        `json:"last_tool"`
	ActiveDelegations int           `json:"active_delegations"`
	ElapsedTime       time.Duration `json:"elapsed_time"`
	AgentID           string        `json:"agent_id"`
}

// Type returns the event type identifier for progress events.
//
// Expected:
//   - None.
//
// Returns:
//   - The event type identifier.
//
// Side effects:
//   - None.
func (e ProgressEvent) Type() string { return EventTypeProgress }

// MarshalJSON serialises a ProgressEvent with a type discriminator field.
//
// Expected:
//   - e contains the event payload.
//
// Returns:
//   - JSON bytes containing the event fields plus a "type" discriminator.
//   - An error if serialisation fails.
//
// Side effects:
//   - None.
func (e ProgressEvent) MarshalJSON() ([]byte, error) {
	type alias ProgressEvent
	return marshalWithType(e.Type(), alias(e))
}

// CompletionNotificationEvent represents a completion notice for a delegated task.
type CompletionNotificationEvent struct {
	TaskID      string        `json:"task_id"`
	Description string        `json:"description"`
	Agent       string        `json:"agent"`
	Duration    time.Duration `json:"duration"`
	Status      string        `json:"status"`
	Result      string        `json:"result"`
	AgentID     string        `json:"agent_id"`
}

// Type returns the event type identifier for completion notification events.
//
// Expected:
//   - None.
//
// Returns:
//   - The event type identifier.
//
// Side effects:
//   - None.
func (e CompletionNotificationEvent) Type() string { return EventTypeCompletionNotification }

// MarshalJSON serialises a CompletionNotificationEvent with a type discriminator field.
//
// Expected:
//   - e contains the event payload.
//
// Returns:
//   - JSON bytes containing the event fields plus a "type" discriminator.
//   - An error if serialisation fails.
//
// Side effects:
//   - None.
func (e CompletionNotificationEvent) MarshalJSON() ([]byte, error) {
	type alias CompletionNotificationEvent
	return marshalWithType(e.Type(), alias(e))
}

// CoordinationStoreEvent represents a coordination store operation.
type CoordinationStoreEvent struct {
	Operation string `json:"operation"`
	Key       string `json:"key"`
	ChainID   string `json:"chainId"`
}

// Type returns the event type identifier for coordination store events.
//
// Expected:
//   - None.
//
// Returns:
//   - The event type identifier.
//
// Side effects:
//   - None.
func (e CoordinationStoreEvent) Type() string { return EventTypeCoordinationStore }

// MarshalJSON serialises a CoordinationStoreEvent with a type discriminator field.
//
// Expected:
//   - e contains the event payload.
//
// Returns:
//   - JSON bytes containing the event fields plus a "type" discriminator.
//   - An error if serialisation fails.
//
// Side effects:
//   - None.
func (e CoordinationStoreEvent) MarshalJSON() ([]byte, error) {
	type alias CoordinationStoreEvent
	return marshalWithType(e.Type(), alias(e))
}

// StatusTransitionEvent represents an agent or phase state change.
type StatusTransitionEvent struct {
	From    string `json:"from"`
	To      string `json:"to"`
	AgentID string `json:"agentId"`
}

// Type returns the event type identifier for status transition events.
//
// Expected:
//   - None.
//
// Returns:
//   - The event type identifier.
//
// Side effects:
//   - None.
func (e StatusTransitionEvent) Type() string { return EventTypeStatusTransition }

// MarshalJSON serialises a StatusTransitionEvent with a type discriminator field.
//
// Expected:
//   - e contains the event payload.
//
// Returns:
//   - JSON bytes containing the event fields plus a "type" discriminator.
//   - An error if serialisation fails.
//
// Side effects:
//   - None.
func (e StatusTransitionEvent) MarshalJSON() ([]byte, error) {
	type alias StatusTransitionEvent
	return marshalWithType(e.Type(), alias(e))
}

// PlanArtifactEvent represents plan content produced by a planner agent.
type PlanArtifactEvent struct {
	Content string `json:"content"`
	Format  string `json:"format"`
	AgentID string `json:"agentId"`
}

// Type returns the event type identifier for plan artifact events.
//
// Expected:
//   - None.
//
// Returns:
//   - The event type identifier.
//
// Side effects:
//   - None.
func (e PlanArtifactEvent) Type() string { return EventTypePlanArtifact }

// MarshalJSON serialises a PlanArtifactEvent with a type discriminator field.
//
// Expected:
//   - e contains the event payload.
//
// Returns:
//   - JSON bytes containing the event fields plus a "type" discriminator.
//   - An error if serialisation fails.
//
// Side effects:
//   - None.
func (e PlanArtifactEvent) MarshalJSON() ([]byte, error) {
	type alias PlanArtifactEvent
	return marshalWithType(e.Type(), alias(e))
}

// ReviewVerdictEvent represents a reviewer verdict on submitted work.
type ReviewVerdictEvent struct {
	Verdict    string   `json:"verdict"`
	Confidence float64  `json:"confidence"`
	Issues     []string `json:"issues"`
	AgentID    string   `json:"agentId"`
}

// Type returns the event type identifier for review verdict events.
//
// Expected:
//   - None.
//
// Returns:
//   - The event type identifier.
//
// Side effects:
//   - None.
func (e ReviewVerdictEvent) Type() string { return EventTypeReviewVerdict }

// MarshalJSON serialises a ReviewVerdictEvent with a type discriminator field.
//
// Expected:
//   - e contains the event payload.
//
// Returns:
//   - JSON bytes containing the event fields plus a "type" discriminator.
//   - An error if serialisation fails.
//
// Side effects:
//   - None.
func (e ReviewVerdictEvent) MarshalJSON() ([]byte, error) {
	type alias ReviewVerdictEvent
	return marshalWithType(e.Type(), alias(e))
}

// RecallSearchEvent represents a semantic search result from recall.
type RecallSearchEvent struct {
	Query     string `json:"query"`
	Results   int    `json:"results"`
	LatencyMS int64  `json:"latencyMs"`
	AgentID   string `json:"agentId"`
}

// Type returns the event type.
//
// Expected:
//   - The receiver contains recall search data.
//
// Returns:
//   - The event type identifier.
//
// Side effects:
//   - None.
func (e RecallSearchEvent) Type() string { return EventTypeRecallSearch }

// MarshalJSON marshals the event to JSON.
//
// Expected:
//   - The receiver contains recall search data ready for serialisation.
//
// Returns:
//   - JSON bytes containing the event fields plus a "type" discriminator.
//   - An error if serialisation fails.
//
// Side effects:
//   - None.
func (e RecallSearchEvent) MarshalJSON() ([]byte, error) {
	type alias RecallSearchEvent
	return marshalWithType(e.Type(), alias(e))
}

// RecallChainSearchEvent represents a cross-agent chain search result from recall.
type RecallChainSearchEvent struct {
	Query     string `json:"query"`
	Results   int    `json:"results"`
	LatencyMS int64  `json:"latencyMs"`
	AgentID   string `json:"agentId"`
}

// Type returns the event type.
//
// Expected:
//   - The receiver contains chain search data.
//
// Returns:
//   - The event type identifier.
//
// Side effects:
//   - None.
func (e RecallChainSearchEvent) Type() string { return EventTypeRecallChainSearch }

// MarshalJSON marshals the event to JSON.
//
// Expected:
//   - The receiver contains chain search data ready for serialisation.
//
// Returns:
//   - JSON bytes containing the event fields plus a "type" discriminator.
//   - An error if serialisation fails.
//
// Side effects:
//   - None.
func (e RecallChainSearchEvent) MarshalJSON() ([]byte, error) {
	type alias RecallChainSearchEvent
	return marshalWithType(e.Type(), alias(e))
}

// RecallSummarizedEvent represents a context summarization result.
type RecallSummarizedEvent struct {
	OriginalTokens int    `json:"originalTokens"`
	SummaryTokens  int    `json:"summaryTokens"`
	LatencyMS      int64  `json:"latencyMs"`
	AgentID        string `json:"agentId"`
}

// Type returns the event type.
//
// Expected:
//   - The receiver contains summarisation data.
//
// Returns:
//   - The event type identifier.
//
// Side effects:
//   - None.
func (e RecallSummarizedEvent) Type() string { return EventTypeRecallSummarized }

// MarshalJSON marshals the event to JSON.
//
// Expected:
//   - The receiver contains summarisation data ready for serialisation.
//
// Returns:
//   - JSON bytes containing the event fields plus a "type" discriminator.
//   - An error if serialisation fails.
//
// Side effects:
//   - None.
func (e RecallSummarizedEvent) MarshalJSON() ([]byte, error) {
	type alias RecallSummarizedEvent
	return marshalWithType(e.Type(), alias(e))
}

// LearningTriggeredEvent signals that a background learning job has been queued.
//
// This event is emitted immediately before Done when the learning loop accepts
// a trigger, giving downstream consumers visibility without blocking the stream.
type LearningTriggeredEvent struct {
	AgentID   string `json:"agentId"`
	TriggerID string `json:"triggerId"`
	Reason    string `json:"reason"`
}

// Type returns the event type identifier for learning triggered events.
//
// Expected:
//   - None.
//
// Returns:
//   - The event type identifier.
//
// Side effects:
//   - None.
func (e LearningTriggeredEvent) Type() string { return EventTypeLearningTriggered }

// MarshalJSON serialises a LearningTriggeredEvent with a type discriminator field.
//
// Expected:
//   - e contains the event payload.
//
// Returns:
//   - JSON bytes containing the event fields plus a "type" discriminator.
//   - An error if serialisation fails.
//
// Side effects:
//   - None.
func (e LearningTriggeredEvent) MarshalJSON() ([]byte, error) {
	type alias LearningTriggeredEvent
	return marshalWithType(e.Type(), alias(e))
}

// MarshalEvent serialises an event to JSON with a type discriminator field.
//
// Expected:
//   - e is a non-nil Event implementation with a valid MarshalJSON method.
//
// Returns:
//   - JSON bytes containing the event fields plus a "type" discriminator.
//   - An error if serialisation fails.
//
// Side effects:
//   - None.
func MarshalEvent(e Event) ([]byte, error) {
	return json.Marshal(e)
}

// UnmarshalEvent deserialises an event from JSON using the type discriminator field.
//
// Expected:
//   - data contains valid JSON with a "type" field identifying the event kind.
//
// Returns:
//   - The concrete Event value matching the type discriminator.
//   - An error if the type is unknown or deserialisation fails.
//
// Side effects:
//   - None.
func UnmarshalEvent(data []byte) (Event, error) {
	var peek struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &peek); err != nil {
		return nil, fmt.Errorf("reading event type: %w", err)
	}

	switch peek.Type {
	case EventTypeTextChunk:
		return unmarshalTyped[TextChunkEvent](data)
	case EventTypeToolCall:
		return unmarshalTyped[ToolCallEvent](data)
	case EventTypeDelegation:
		return unmarshalTyped[DelegationEvent](data)
	case EventTypeProgress:
		return unmarshalTyped[ProgressEvent](data)
	case EventTypeCompletionNotification:
		return unmarshalTyped[CompletionNotificationEvent](data)
	case EventTypeCoordinationStore:
		return unmarshalTyped[CoordinationStoreEvent](data)
	case EventTypeStatusTransition:
		return unmarshalTyped[StatusTransitionEvent](data)
	case EventTypePlanArtifact:
		return unmarshalTyped[PlanArtifactEvent](data)
	case EventTypeReviewVerdict:
		return unmarshalTyped[ReviewVerdictEvent](data)
	case EventTypeRecallSearch:
		return unmarshalTyped[RecallSearchEvent](data)
	case EventTypeRecallChainSearch:
		return unmarshalTyped[RecallChainSearchEvent](data)
	case EventTypeRecallSummarized:
		return unmarshalTyped[RecallSummarizedEvent](data)
	case EventTypeLearningTriggered:
		return unmarshalTyped[LearningTriggeredEvent](data)
	default:
		return nil, fmt.Errorf("unknown event type: %q", peek.Type)
	}
}

// unmarshalTyped deserialises JSON into a concrete event type.
//
// Expected:
//   - data contains a JSON-encoded event payload.
//
// Returns:
//   - The decoded event value.
//   - An error if decoding fails.
//
// Side effects:
//   - None.
func unmarshalTyped[T Event](data []byte) (T, error) {
	var e T
	if err := json.Unmarshal(data, &e); err != nil {
		return e, fmt.Errorf("unmarshalling %T: %w", e, err)
	}
	return e, nil
}

// marshalWithType serialises an event as JSON with a type discriminator field.
//
// Expected:
//   - typeName identifies the event type.
//   - data is a JSON-marshalable event payload.
//
// Returns:
//   - The serialised event JSON.
//   - An error if marshalling fails.
//
// Side effects:
//   - None.
func marshalWithType(typeName string, data interface{}) ([]byte, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshalling event data: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("parsing event fields: %w", err)
	}
	typeBytes, err := json.Marshal(typeName)
	if err != nil {
		return nil, fmt.Errorf("marshalling event type name: %w", err)
	}
	fields["type"] = typeBytes
	return json.Marshal(fields)
}
