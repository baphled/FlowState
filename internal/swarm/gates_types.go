package swarm

// GateRunner executes the gates of a manifest against a supplied
// command executor and reports the overall decision.
type GateRunner struct {
	// execCommand runs one gate command; it mirrors exec.Command.
	execCommand func(name string, args ...string) gateOutput
}

// gateOutput carries the execution error of one gate command.
type gateOutput struct {
	err error
}

// GateOutcome is the observed result of one gate check.
type GateOutcome struct {
	// Gate is the gate that was checked.
	Gate Gate
	// Passed reports whether the gate command exited zero.
	Passed bool
}

// GatesDecision is the aggregated result of evaluating every gate.
type GatesDecision struct {
	// Accepted is true only when every gate passed and at least one
	// gate was checked.
	Accepted bool
	// Outcomes lists per-gate results in manifest order.
	Outcomes []GateOutcome
}
