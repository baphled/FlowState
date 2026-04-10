package learning

import "time"

// TriggerKind classifies why a learning trigger was raised.
type TriggerKind string

const (
	// TriggerKindFailure indicates the trigger was raised because evaluation failed.
	TriggerKindFailure TriggerKind = "failure"
	// TriggerKindNovelty indicates the trigger was raised because novel output was detected.
	TriggerKindNovelty TriggerKind = "novelty"
)

// TriggerSource identifies which component raised the trigger.
type TriggerSource string

const (
	// TriggerSourceExecutionLoop indicates the trigger originated from the execution harness loop.
	TriggerSourceExecutionLoop TriggerSource = "execution_loop"
	// TriggerSourceLearningHook indicates the trigger originated from the learning hook.
	TriggerSourceLearningHook TriggerSource = "learning_hook"
)

// Trigger represents a single learning signal raised by an agent evaluation.
type Trigger struct {
	// ID is a unique identifier for this trigger, used for deduplication and event correlation.
	ID string
	// AgentID identifies the agent whose output raised this trigger.
	AgentID string
	// Kind classifies the reason for the trigger.
	Kind TriggerKind
	// Source identifies which component raised the trigger.
	Source TriggerSource
	// Output is the raw agent output associated with the trigger.
	Output string
	// RaisedAt is the time the trigger was created.
	RaisedAt time.Time
}

// TriggerSink accepts learning triggers for asynchronous processing.
//
// Implementations must be safe to call concurrently.
// Notify must never block: it should drop the trigger silently when the
// internal buffer is full.
type TriggerSink interface {
	// Notify enqueues a trigger for background processing.
	// It returns immediately; overflow is dropped silently.
	Notify(t Trigger)
}
