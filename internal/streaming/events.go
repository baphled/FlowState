package streaming

import (
	"encoding/json"
	"fmt"
	"time"
)

// Event type constants identify each discrete event kind within the streaming pipeline.
const (
	EventTypeTextChunk         = "text_chunk"
	EventTypeToolCall          = "tool_call"
	EventTypeDelegation        = "delegation"
	EventTypeCoordinationStore = "coordination_store"
	EventTypeStatusTransition  = "status_transition"
	EventTypePlanArtifact      = "plan_artifact"
	EventTypeReviewVerdict     = "review_verdict"
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

// DelegationEvent represents a delegation action between agents.
type DelegationEvent struct {
	SourceAgent string `json:"sourceAgent"`
	TargetAgent string `json:"targetAgent"`
	ChainID     string `json:"chainId"`
	Status      string `json:"status"`
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
	case EventTypeCoordinationStore:
		return unmarshalTyped[CoordinationStoreEvent](data)
	case EventTypeStatusTransition:
		return unmarshalTyped[StatusTransitionEvent](data)
	case EventTypePlanArtifact:
		return unmarshalTyped[PlanArtifactEvent](data)
	case EventTypeReviewVerdict:
		return unmarshalTyped[ReviewVerdictEvent](data)
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
