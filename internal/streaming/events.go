package streaming

import (
	"encoding/json"
	"time"
)

// Event is the base interface for all typed events in the streaming system.
// Each concrete event type must implement EventType() to identify itself.
type Event interface {
	// EventType returns the type identifier for this event.
	EventType() string
}

// TextChunkEvent represents raw LLM output text chunks.
type TextChunkEvent struct {
	Content string `json:"content"`
}

// EventType returns the event type identifier.
//
// Returns:
//   - The string "text_chunk".
//
// Side effects:
//   - None.
func (e *TextChunkEvent) EventType() string {
	return "text_chunk"
}

// MarshalJSON serialises the event with a type discriminator field.
//
// Returns:
//   - JSON bytes including a "type" field, or an error.
//
// Side effects:
//   - None.
func (e *TextChunkEvent) MarshalJSON() ([]byte, error) {
	type Alias TextChunkEvent
	return json.Marshal(&struct {
		Type string `json:"type"`
		Alias
	}{
		Type:  e.EventType(),
		Alias: Alias(*e),
	})
}

// ToolCallEvent represents a tool invocation with its result and duration.
type ToolCallEvent struct {
	Name     string         `json:"name"`
	Args     map[string]any `json:"args"`
	Result   string         `json:"result"`
	Duration time.Duration  `json:"duration"`
}

// EventType returns the event type identifier.
//
// Returns:
//   - The string "tool_call".
//
// Side effects:
//   - None.
func (e *ToolCallEvent) EventType() string {
	return "tool_call"
}

// MarshalJSON serialises the event with a type discriminator field.
//
// Returns:
//   - JSON bytes including a "type" field, or an error.
//
// Side effects:
//   - None.
func (e *ToolCallEvent) MarshalJSON() ([]byte, error) {
	type Alias ToolCallEvent
	return json.Marshal(&struct {
		Type string `json:"type"`
		Alias
	}{
		Type:  e.EventType(),
		Alias: Alias(*e),
	})
}

// DelegationEvent represents a delegation hop between agents in a chain.
type DelegationEvent struct {
	Source  string `json:"source"`
	Target  string `json:"target"`
	ChainID string `json:"chain_id"`
	Status  string `json:"status"` // started, completed, failed
}

// EventType returns the event type identifier.
//
// Returns:
//   - The string "delegation".
//
// Side effects:
//   - None.
func (e *DelegationEvent) EventType() string {
	return "delegation"
}

// MarshalJSON serialises the event with a type discriminator field.
//
// Returns:
//   - JSON bytes including a "type" field, or an error.
//
// Side effects:
//   - None.
func (e *DelegationEvent) MarshalJSON() ([]byte, error) {
	type Alias DelegationEvent
	return json.Marshal(&struct {
		Type string `json:"type"`
		Alias
	}{
		Type:  e.EventType(),
		Alias: Alias(*e),
	})
}

// CoordinationStoreEvent represents a coordination store operation.
type CoordinationStoreEvent struct {
	Operation string `json:"operation"` // get, set
	Key       string `json:"key"`
	ChainID   string `json:"chain_id"`
}

// EventType returns the event type identifier.
//
// Returns:
//   - The string "coordination_store".
//
// Side effects:
//   - None.
func (e *CoordinationStoreEvent) EventType() string {
	return "coordination_store"
}

// MarshalJSON serialises the event with a type discriminator field.
//
// Returns:
//   - JSON bytes including a "type" field, or an error.
//
// Side effects:
//   - None.
func (e *CoordinationStoreEvent) MarshalJSON() ([]byte, error) {
	type Alias CoordinationStoreEvent
	return json.Marshal(&struct {
		Type string `json:"type"`
		Alias
	}{
		Type:  e.EventType(),
		Alias: Alias(*e),
	})
}

// StatusTransitionEvent represents an agent or phase status transition.
type StatusTransitionEvent struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// EventType returns the event type identifier.
//
// Returns:
//   - The string "status_transition".
//
// Side effects:
//   - None.
func (e *StatusTransitionEvent) EventType() string {
	return "status_transition"
}

// MarshalJSON serialises the event with a type discriminator field.
//
// Returns:
//   - JSON bytes including a "type" field, or an error.
//
// Side effects:
//   - None.
func (e *StatusTransitionEvent) MarshalJSON() ([]byte, error) {
	type Alias StatusTransitionEvent
	return json.Marshal(&struct {
		Type string `json:"type"`
		Alias
	}{
		Type:  e.EventType(),
		Alias: Alias(*e),
	})
}

// PlanArtifactEvent represents a plan or artifact content.
type PlanArtifactEvent struct {
	Content string `json:"content"`
	Format  string `json:"format"` // json, yaml, markdown, text
}

// EventType returns the event type identifier.
//
// Returns:
//   - The string "plan_artifact".
//
// Side effects:
//   - None.
func (e *PlanArtifactEvent) EventType() string {
	return "plan_artifact"
}

// MarshalJSON serialises the event with a type discriminator field.
//
// Returns:
//   - JSON bytes including a "type" field, or an error.
//
// Side effects:
//   - None.
func (e *PlanArtifactEvent) MarshalJSON() ([]byte, error) {
	type Alias PlanArtifactEvent
	return json.Marshal(&struct {
		Type string `json:"type"`
		Alias
	}{
		Type:  e.EventType(),
		Alias: Alias(*e),
	})
}

// ReviewVerdictEvent represents a review verdict with confidence and issues.
type ReviewVerdictEvent struct {
	Verdict    string   `json:"verdict"`    // approve, reject
	Confidence float64  `json:"confidence"` // 0.0 to 1.0
	Issues     []string `json:"issues"`
}

// EventType returns the event type identifier.
//
// Returns:
//   - The string "review_verdict".
//
// Side effects:
//   - None.
func (e *ReviewVerdictEvent) EventType() string {
	return "review_verdict"
}

// MarshalJSON serialises the event with a type discriminator field.
//
// Returns:
//   - JSON bytes including a "type" field, or an error.
//
// Side effects:
//   - None.
func (e *ReviewVerdictEvent) MarshalJSON() ([]byte, error) {
	type Alias ReviewVerdictEvent
	return json.Marshal(&struct {
		Type string `json:"type"`
		Alias
	}{
		Type:  e.EventType(),
		Alias: Alias(*e),
	})
}
