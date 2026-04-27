// Package swarm — gate-policy dispatch surface (addendum §7 A1 + A6).
//
// Where gates.go houses the runner registry, the schema lookup, and
// the lifecycle filters, this file owns the "how" of running a slice
// of gates: precedence ordering (A6), per-gate timeout, and
// failurePolicy interpretation (A1).
//
// The Dispatch helper exists so the engine package's delegation code
// has a single entry point for "run these gates with the spec'd
// semantics" instead of inlining the precedence sort + timeout +
// policy switch at every lifecycle site. Tests pin the externally-
// observable behaviour (which configurations halt vs continue vs
// warn) against Dispatch directly without bringing the engine into
// scope.
package swarm

import (
	"context"
	"errors"
	"sort"
	"time"
)

// EffectivePrecedence returns the precedence the dispatcher should
// apply to gate. An unset Precedence falls back to DefaultPrecedence
// (MEDIUM) so legacy manifests preserve their declaration-order
// semantics.
//
// Expected:
//   - gate is any GateSpec.
//
// Returns:
//   - gate.Precedence when set; DefaultPrecedence otherwise.
//
// Side effects:
//   - None.
func EffectivePrecedence(gate GateSpec) Precedence {
	if gate.Precedence == "" {
		return DefaultPrecedence
	}
	return gate.Precedence
}

// EffectiveFailurePolicy returns the failure policy the dispatcher
// should apply to gate. An unset FailurePolicy falls back to
// DefaultFailurePolicy (halt) so manifests written before A1 retain
// today's halt-on-failure behaviour.
//
// Expected:
//   - gate is any GateSpec.
//
// Returns:
//   - gate.FailurePolicy when set; DefaultFailurePolicy otherwise.
//
// Side effects:
//   - None.
func EffectiveFailurePolicy(gate GateSpec) FailurePolicy {
	if gate.FailurePolicy == "" {
		return DefaultFailurePolicy
	}
	return gate.FailurePolicy
}

// precedenceOrder maps a Precedence to a sortable rank where smaller
// values run earlier. CRITICAL=0, HIGH=1, MEDIUM=2, LOW=3 mirrors the
// A6 table and lets sort.SliceStable preserve manifest order on ties.
//
// Expected:
//   - p is any Precedence; unknown values rank as MEDIUM so a
//     hand-edited slice with a stale value still sorts predictably
//     (validateGatePrecedence catches the same value on load).
//
// Returns:
//   - The integer rank.
//
// Side effects:
//   - None.
func precedenceOrder(p Precedence) int {
	switch p {
	case PrecedenceCritical:
		return 0
	case PrecedenceHigh:
		return 1
	case PrecedenceLow:
		return 3
	default:
		return 2
	}
}

// SortGatesByPrecedence returns a fresh slice of gates ordered by
// precedence (CRITICAL first, LOW last) with stable preservation of
// manifest order within each precedence tier (per the modal-registry
// pattern A6 generalises). Callers who need a non-mutating ordering
// should use this rather than sort.SliceStable inline so the rule
// stays in one place.
//
// Expected:
//   - gates may be nil or empty; an empty slice is returned in that
//     case so callers can range over the result without a nil-guard.
//
// Returns:
//   - A new slice of the same length as gates with the precedence
//     order applied.
//
// Side effects:
//   - None (the input slice is not mutated).
func SortGatesByPrecedence(gates []GateSpec) []GateSpec {
	out := make([]GateSpec, len(gates))
	copy(out, gates)
	sort.SliceStable(out, func(i, j int) bool {
		return precedenceOrder(EffectivePrecedence(out[i])) < precedenceOrder(EffectivePrecedence(out[j]))
	})
	return out
}

// GateFailure records a single gate failure that did NOT halt the
// dispatch (because failurePolicy was continue or warn). Surfaced via
// DispatchReport so callers can render warnings without re-deriving
// them from log output.
type GateFailure struct {
	// GateName is the manifest-supplied name of the failing gate.
	GateName string
	// GateKind is the kind string ("builtin:result-schema" etc.) for
	// log filtering by family.
	GateKind string
	// Policy is the EffectiveFailurePolicy under which the failure
	// was tolerated. Set so callers can distinguish a continue
	// failure from a warn failure even when both are non-halting.
	Policy FailurePolicy
	// Err is the underlying error returned by the runner.
	Err error
}

// DispatchReport carries the externally-observable outcome of a
// Dispatch call. Halted is true when a halt-policy gate failed; in
// that case Err is the failing error and HaltedBy names the gate.
// Failures records every gate that failed under continue-policy;
// Warnings records every gate that failed under warn-policy. The
// fields are intentionally redundant (a halt-policy failure does NOT
// appear in Failures) so callers can read the report with a single
// switch on Halted.
type DispatchReport struct {
	// Halted is true when a halt-policy gate failed and dispatch
	// stopped before reaching the end of the gate slice.
	Halted bool
	// HaltedBy is the gate name that caused the halt, populated
	// only when Halted is true.
	HaltedBy string
	// Err is the halting error; populated only when Halted is true.
	Err error
	// Failures is the ordered list of gate failures tolerated under
	// FailurePolicyContinue.
	Failures []GateFailure
	// Warnings is the ordered list of gate failures tolerated under
	// FailurePolicyWarn. Held in its own slice so UI surfaces can
	// render warn vs continue with different glyphs.
	Warnings []GateFailure
}

// Dispatch runs every gate in gates against runner with the spec'd
// precedence + timeout + failurePolicy semantics. Gates are first
// sorted by precedence (CRITICAL first, LOW last; stable on ties);
// each gate then runs with its Timeout applied as a context deadline
// when set; on failure, EffectiveFailurePolicy decides whether
// dispatch halts (halt), continues recording the failure (continue),
// or continues recording a warning (warn).
//
// Expected:
//   - ctx is the parent context the runner inherits. Cancellation on
//     the parent propagates through the per-gate timeout context.
//   - runner is the GateRunner the dispatcher calls. nil short-
//     circuits to a no-op so callers do not need a nil-guard at
//     every dispatch site.
//   - gates may be nil or empty; an empty DispatchReport is returned
//     in that case.
//   - args is the per-gate GateArgs envelope; passed verbatim to
//     each runner call.
//
// Returns:
//   - A populated DispatchReport. Halted reports whether the swarm
//     runner should stop; Err carries the halting error when so.
//
// Side effects:
//   - Calls runner.Run for each gate with a possibly-derived context.
func Dispatch(ctx context.Context, runner GateRunner, gates []GateSpec, args GateArgs) DispatchReport {
	report := DispatchReport{}
	if runner == nil || len(gates) == 0 {
		return report
	}
	ordered := SortGatesByPrecedence(gates)
	for _, gate := range ordered {
		err := runGateWithTimeout(ctx, runner, gate, args)
		if err == nil {
			continue
		}
		switch EffectiveFailurePolicy(gate) {
		case FailurePolicyContinue:
			report.Failures = append(report.Failures, GateFailure{
				GateName: gate.Name,
				GateKind: gate.Kind,
				Policy:   FailurePolicyContinue,
				Err:      err,
			})
		case FailurePolicyWarn:
			report.Warnings = append(report.Warnings, GateFailure{
				GateName: gate.Name,
				GateKind: gate.Kind,
				Policy:   FailurePolicyWarn,
				Err:      err,
			})
		default:
			report.Halted = true
			report.HaltedBy = gate.Name
			report.Err = err
			return report
		}
	}
	return report
}

// runGateWithTimeout invokes runner.Run for gate with ctx wrapped in
// a context.WithTimeout when gate.Timeout is positive. Pulled into a
// helper so Dispatch's loop body stays focused on the policy switch;
// the deadline-derivation rule is single-source-of-truth here.
//
// On timeout the returned error is a *GateError carrying
// context.DeadlineExceeded as its cause so callers can errors.Is
// against the sentinel and the structured surface (gate name, kind,
// when, member) is preserved for log/UI rendering.
//
// Expected:
//   - ctx is the parent context.
//   - runner is non-nil (Dispatch enforces).
//   - gate is one entry from the ordered slice.
//   - args is the per-gate envelope passed verbatim.
//
// Returns:
//   - nil when the runner reports pass.
//   - The runner's error when no timeout is set.
//   - A *GateError wrapping context.DeadlineExceeded on timeout.
//
// Side effects:
//   - Calls runner.Run; may derive a timeout-bound child context.
func runGateWithTimeout(ctx context.Context, runner GateRunner, gate GateSpec, args GateArgs) error {
	if gate.Timeout <= 0 {
		return runner.Run(ctx, gate, args)
	}
	gateCtx, cancel := context.WithTimeout(ctx, gate.Timeout)
	defer cancel()
	err := runner.Run(gateCtx, gate, args)
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &GateError{
			GateName: gate.Name,
			GateKind: gate.Kind,
			When:     gate.When,
			SwarmID:  args.SwarmID,
			MemberID: args.MemberID,
			Reason:   gateTimeoutReason(gate.Timeout),
			Cause:    err,
		}
	}
	return err
}

// gateTimeoutReason renders the canonical "timed out after Xs" reason
// string surfaced on a GateError when a per-gate timeout fires. Held
// in a helper so the format stays stable across log filters and UI
// matchers.
//
// Expected:
//   - timeout is the configured per-gate Timeout that fired.
//
// Returns:
//   - The formatted reason string.
//
// Side effects:
//   - None.
func gateTimeoutReason(timeout time.Duration) string {
	return "gate timed out after " + timeout.String()
}
