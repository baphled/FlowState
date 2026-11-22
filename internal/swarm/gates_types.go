package swarm

import "context"

// GateRunner executes the gates of a manifest against a supplied
// command executor and reports the overall decision.
type GateRunner struct {
	// execCommand runs one gate command and returns its execution error.
	execCommand GateExecutor
}

// GateExecutor runs one gate command. It must stop the command and
// return promptly once ctx is cancelled; a nil error means exit zero.
type GateExecutor func(ctx context.Context, name string, args []string) error

// GateOutcome is the observed result of one gate check.
type GateOutcome struct {
	// Gate is the gate that was checked.
	Gate Gate
	// Passed reports whether the gate command exited zero.
	Passed bool
	// Interrupted reports that the caller's context was done when the
	// gate command returned an error: the gate was cut short, not judged.
	Interrupted bool
}

// GatesDecision is the aggregated result of evaluating every gate.
type GatesDecision struct {
	// Accepted is true only when every gate passed and at least one
	// gate was checked.
	Accepted bool
	// Outcomes lists per-gate results in manifest order.
	Outcomes []GateOutcome
	// Interrupted is true when cancellation cut the evaluation short:
	// a gate was skipped or a running gate was interrupted. Gates that
	// all finished on their own before a late cancellation leave it false.
	Interrupted bool
}
