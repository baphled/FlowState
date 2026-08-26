package execution

import "github.com/baphled/flowstate/internal/harness"

// StopReason explains why the evaluation loop terminated.
type StopReason string

const (
	// StopReasonPassed indicates the output satisfied the validator.
	StopReasonPassed StopReason = "passed"
	// StopReasonMaxRetries indicates the loop reached its retry limit without passing.
	StopReasonMaxRetries StopReason = "max_retries"
	// StopReasonCancelled indicates the context was cancelled before the loop finished.
	StopReasonCancelled StopReason = "cancelled"
)

// Outcome captures the final result of a single execution loop run.
type Outcome struct {
	// Result is the last EvaluationResult produced by the loop.
	Result *harness.EvaluationResult
	// StopReason explains why the loop stopped.
	StopReason StopReason
	// Attempts is the total number of evaluation attempts made.
	Attempts int
}

// OutcomeObserver receives the final outcome of an execution loop run.
//
// Implementations must be safe to call from any goroutine.
type OutcomeObserver interface {
	// OnOutcome is called exactly once when the loop terminates.
	OnOutcome(outcome Outcome)
}
