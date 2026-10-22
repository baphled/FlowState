// Package execution provides a general-purpose harness evaluation loop for non-planning agents.
//
// This package implements:
//   - A [Loop] that satisfies [harness.Evaluator], running a validate-critique cycle
//     until the output passes or the maximum retry count is reached.
//   - A [RetryStrategy] interface and [DefaultRetryStrategy] for deciding when to retry.
//   - [Outcome] and [OutcomeObserver] types for observing loop results.
//   - Functional [Option] constructors for configuring a [Loop] at creation time.
//
// The execution loop is distinct from the plan harness in that it does not emit
// plan-specific events (e.g. plan_artifact) and does not call hook.DetectPhase.
// It is intended as the foundation for future user-facing harness customisation.
package execution
