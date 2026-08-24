package engine

import (
	"context"
	"errors"
	"strings"

	"github.com/baphled/flowstate/internal/gates"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/swarm"
)

// publishGateEvaluating publishes a `gate.evaluating` lifecycle marker
// for a non-empty gate batch about to dispatch. Plans/Gate Bus Bridge
// — Engine to SSE and TUI (May 2026): one event per `swarm.Dispatch`
// call, NOT per gate; carries the count of gates and the lifecycle
// point so subscribers can render an "evaluating N gates…" affordance
// without per-gate noise.
//
// Expected:
//   - swarmCtx is the active swarm context.
//   - when is one of "pre" | "post" | "pre-member" | "post-member".
//   - memberID is the agent id for member-scoped lifecycles; empty
//     for swarm-level.
//   - count is the number of gates in the batch (non-zero — callers
//     short-circuit on empty batches before publishing).
//
// Side effects:
//   - Publishes `gate.evaluating` on the bus when wired; otherwise
//     a no-op.
//
// Returns: result of publishGateEvaluating.
func (d *DelegateTool) publishGateEvaluating(ctx context.Context, swarmCtx *swarm.Context, when, memberID string, count int) {
	if d.eventBus == nil {
		return
	}
	d.eventBus.Publish(events.EventGateEvaluating, events.NewGateEvaluatingEvent(events.GateEventData{
		SwarmID:   swarmCtx.SwarmID,
		SessionID: sessionIDFromContext(ctx),
		Lifecycle: when,
		MemberID:  memberID,
		GateCount: count,
	}))
}

// publishGatePassed publishes a `gate.passed` event when a gate batch
// completes without any halt. Plans/Gate Bus Bridge — Engine to SSE
// and TUI (May 2026): one event per `swarm.Dispatch` call on the
// clean path (NOT per gate); per-gate pass events are deliberately
// suppressed by the pass-event policy.
//
// Expected:
//   - swarmCtx is the active swarm context.
//   - when is the lifecycle point.
//   - memberID is the agent id for member-scoped lifecycles; empty
//     for swarm-level.
//   - count is the batch size (non-zero — callers short-circuit on
//     empty batches before publishing).
//
// Side effects:
//   - Publishes `gate.passed` on the bus when wired; otherwise a no-op.
//
// Returns: result of publishGatePassed.
func (d *DelegateTool) publishGatePassed(ctx context.Context, swarmCtx *swarm.Context, when, memberID string, count int) {
	if d.eventBus == nil {
		return
	}
	d.eventBus.Publish(events.EventGatePassed, events.NewGatePassedEvent(events.GateEventData{
		SwarmID:   swarmCtx.SwarmID,
		SessionID: sessionIDFromContext(ctx),
		Lifecycle: when,
		MemberID:  memberID,
		GateCount: count,
	}))
}

// publishGateFailed publishes a `gate.failed` event for a halt-class
// gate failure. Plans/Gate Bus Bridge — Engine to SSE and TUI (May
// 2026): one event per failing gate; carries the typed
// *swarm.GateError fields as plain strings on the bus payload so
// subscribers do not need to thread the typed error type across the
// boundary. Continue-class and warn-class failures stay log-only —
// they do not reach this helper because runSwarmGates /
// dispatchMemberGates only invoke it on `report.Halted`.
//
// Defensive: when err is not a *swarm.GateError (the runner taxonomy
// permits raw context-cancellation surfaces) the helper falls back to
// `Reason: err.Error()` with empty typed fields so the surface still
// renders something meaningful rather than a blank banner.
//
// Expected:
//   - swarmCtx is the active swarm context.
//   - when is the lifecycle point.
//   - memberID is the agent id for member-scoped lifecycles; empty
//     for swarm-level.
//   - err is the report.Err returned by `swarm.Dispatch` on halt;
//     non-nil at every call site.
//
// Side effects:
//   - Publishes `gate.failed` on the bus when wired; otherwise a no-op.
//
// Returns: result of publishGateFailed.
func (d *DelegateTool) publishGateFailed(ctx context.Context, swarmCtx *swarm.Context, when, memberID string, err error) {
	if d.eventBus == nil || err == nil {
		return
	}
	data := events.GateEventData{
		SwarmID:   swarmCtx.SwarmID,
		SessionID: sessionIDFromContext(ctx),
		Lifecycle: when,
		MemberID:  memberID,
	}
	var gateErr *swarm.GateError
	if errors.As(err, &gateErr) {
		data.GateName = gateErr.GateName
		data.GateKind = gateErr.GateKind
		data.Reason = gateErr.Reason
		// Prefer the gate's MemberID when present; member-post failures
		// carry the failing member explicitly on the typed error.
		if gateErr.MemberID != "" {
			data.MemberID = gateErr.MemberID
		}
		if gateErr.Cause != nil {
			data.Cause = gateErr.Cause.Error()
		}
		data.CoordStoreKeys = gateCoordStoreKeys(gateErr, swarmCtx.ChainPrefix)
	} else {
		data.Reason = err.Error()
	}
	d.eventBus.Publish(events.EventGateFailed, events.NewGateFailedEvent(data))
}

// gateExtKindPrefix is the manifest prefix the swarm validator
// requires on ext-gate kinds (e.g. "ext:relevance-gate"). Mirrors
// `gateKindExtPrefix` in internal/swarm/manifest.go (unexported there);
// re-declared here so this seam can identify ext gates without a
// public-API change to the swarm package.
const gateExtKindPrefix = "ext:"

// gateCoordStoreKeys reads the gate's declared `Inputs` from the
// gate-input registry (Plans/Multi-Key Gate Inputs (May 2026)) and
// returns the resolved coord-store keys in the order the manifest
// declared them. Returns nil for legacy single-key gates and for
// builtin gates that have no Inputs registered. The keys carry the
// "what was checked?" affordance to surfaces.
//
// Expected: parameters for gateCoordStoreKeys.
// Returns: result of gateCoordStoreKeys.
// Side effects: None.
func gateCoordStoreKeys(gateErr *swarm.GateError, chainPrefix string) []string {
	if gateErr == nil {
		return nil
	}
	if !strings.HasPrefix(gateErr.GateKind, gateExtKindPrefix) {
		return nil
	}
	gateName := strings.TrimPrefix(gateErr.GateKind, gateExtKindPrefix)
	inputs, ok := swarm.LookupGateInputs(gateName)
	if !ok || len(inputs) == 0 {
		return nil
	}
	keys := make([]string, 0, len(inputs))
	for _, spec := range inputs {
		member := spec.Member
		if member == gates.TargetPlaceholder {
			member = gateErr.MemberID
		}
		keys = append(keys, joinGateKey(chainPrefix, member, spec.OutputKey))
	}
	return keys
}

// joinGateKey builds a coord-store key from chainPrefix / member /
// outputKey, skipping empty segments — mirrors the unexported joinKey
// helper at internal/swarm/gate_result_schema.go:187 so the bus
// payload's CoordStoreKeys formatting matches the gate-runner's
// lookup formatting verbatim. Re-declared rather than exported because
// the helper is shared only at this seam.
//
// Expected: parameters for joinGateKey.
// Returns: result of joinGateKey.
// Side effects: None.
func joinGateKey(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "/")
}

// publishDelegationEvent publishes a delegation lifecycle event onto the
// installed bus (when one is wired). The status string drives the topic
// selection: "started" → `delegation.started`, "completed" →
// `delegation.completed`, anything else → `delegation.failed`.
//
// Expected:
//   - status is one of "started", "completed", "failed".
//   - data carries the delegation lifecycle payload; ChildSessionID must
//     be populated by the caller (the publish sites fire post-resolve).
//
// Side effects:
//   - Publishes onto the bus when wired; otherwise a no-op.
//
// Returns: result of publishDelegationEvent.
func (d *DelegateTool) publishDelegationEvent(status string, data events.DelegationEventData) {
	if d.eventBus == nil {
		return
	}
	data.Status = status
	switch status {
	case "started":
		d.eventBus.Publish(events.EventDelegationStarted, events.NewDelegationStartedEvent(data))
	case "completed":
		d.eventBus.Publish(events.EventDelegationCompleted, events.NewDelegationCompletedEvent(data))
	case "progress":
		d.eventBus.Publish(events.EventDelegationProgress, events.NewDelegationProgressEvent(data))
	default:
		d.eventBus.Publish(events.EventDelegationFailed, events.NewDelegationFailedEvent(data))
	}
}

// RunnerFactory builds the *swarm.Runner the dispatch loop installs
// for a given manifest. The runner is constructed once per swarm id
// and cached; the factory is consulted only on the first dispatch
// against a swarm context. A nil manifest signals "no swarm context"
// and the factory MUST return a Runner with default retry/breaker
// values so the no-swarm path still gets retry semantics.
type RunnerFactory func(*swarm.Manifest) *swarm.Runner

// defaultRunnerFactory returns the all-defaults factory used when the
// app forgets to inject one via WithRunnerFactory. Constructs a Runner
// from Manifest.EffectiveRetryPolicy / EffectiveCircuitBreaker (so
// addendum-A2 defaults apply consistently with the manifest helpers)
// or, when manifest is nil, a Runner from zero-value RetryPolicy /
// CircuitBreakerConfig that the swarm package fills in with its own
// defaults.
//
// Expected: parameters for defaultRunnerFactory.
// Returns: result of defaultRunnerFactory.
// Side effects: None.
func defaultRunnerFactory(m *swarm.Manifest) *swarm.Runner {
	if m == nil {
		return swarm.NewRunner(swarm.RetryPolicy{}, swarm.CircuitBreakerConfig{})
	}
	return swarm.NewRunner(m.EffectiveRetryPolicy(), m.EffectiveCircuitBreaker())
}

// appendGateDirective appends the gate failure's directive to a member's
// prompt for re-delegation. The directive names the failing gate and
// surfaces the GateError.Reason verbatim so custom DD-schema messages
// (word-count shortfall, missing sections, citation ratio) reach the
// member as actionable retry feedback, then directs the member to
// revise and re-write to the coordination_store. This covers BOTH
// failure families uniformly:
//   - No-output failures: the result-schema runner's Reason already
//     carries the actionable text ("perform the coordination_store
//     write, do not narrate it"), so the member receives an explicit
//     instruction to PERFORM the write.
//   - Schema-validation failures: the member DID write to the store
//     but the payload failed a custom validator; the Reason names
//     the shortfall (e.g. "Your response was 120 words. Minimum
//     required: 300 words."), so the directive tells the member to
//     revise and overwrite the existing store key.
//
// A nil error, a non-GateError, or an empty Reason leaves the message
// unchanged.
//
// Expected:
//   - message is the member's current prompt (target.message).
//   - gateErr is the *swarm.GateError the post-member gate returned.
//
// Returns:
//   - The message with the directive appended on a fresh paragraph, or
//     message unchanged when there is nothing actionable to append.
//
// Side effects:
//   - None.
func appendGateDirective(message string, gateErr error) string {
	var ge *swarm.GateError
	if !errors.As(gateErr, &ge) || ge.Reason == "" {
		return message
	}
	directive := "Gate '" + ge.GateName + "' rejected your output. " + ge.Reason +
		" Please revise your response and write the corrected output to the coordination_store."
	if message == "" {
		return directive
	}
	return message + "\n\n" + directive
}

// AppendGateDirective is the exported seam over appendGateDirective for the
// features/engine/gate_amendment_directive.feature BDD glue. Behaviour is
// identical to the unexported function.
//
// Expected:
//   - message is the member's current prompt.
//   - gateErr is the *swarm.GateError the post-member gate returned.
//
// Returns:
//   - The message with the directive appended, or message unchanged.
//
// Side effects:
//   - None.
func AppendGateDirective(message string, gateErr error) string {
	return appendGateDirective(message, gateErr)
}

// appendPlainDirective appends a directive to produce substantive output
// to a plain delegate's prompt before re-delegation. Unlike the gate retry
// path (which instructs the member to write to the coordination_store), the
// plain delegation path has no post-member gate — the response was simply
// empty or non-substantive (narration without analysis).
//
// Expected:
//   - message is the delegate's current prompt (target.message).
//
// Returns:
//   - The message with the directive appended on a fresh paragraph, or the
//     directive alone when the original message is empty.
//
// Side effects:
//   - None.
func appendPlainDirective(message string) string {
	directive := "Your previous response was empty or lacked substantive content. Provide a detailed, thorough analysis with specific findings — do not simply narrate your process."
	if message == "" {
		return directive
	}
	return message + "\n\n" + directive
}

// forcedCoordinationStoreTool is the coordination_store write tool the
// corrective retry forces. Kept as a local const (the source of truth is
// internal/tool/coordination.toolName) so the engine package does not
// import the tool package solely for this string; a drift guard lives in
// the delegation specs.
const forcedCoordinationStoreTool = "coordination_store"

// forcedToolChoiceForGate decides the tool_choice to FORCE on a corrective
// re-delegation given the post-member gate failure. The synthesis-hang
// signature is a member that NARRATED its coordination_store write but
// emitted no tool call — the result-schema runner surfaces this as a
// "no member output found ... narrated the write but emitted no tool call"
// reason (internal/swarm/gate_result_schema.go noOutputDirective). For
// that signature we return "tool:coordination_store" so even a marginal
// model is compelled to emit the write the prose directive only asked for.
//
// A schema validation failure also triggers the forced write: the member
// DID write a coordination_store key but the payload failed the result-
// schema gate (e.g. the vault-explorer in session 579c829d wrote an empty
// placeholder before gathering data, then the final structured write was
// lost to tool_use_no_calls). Forcing the write on retry compels the model
// to overwrite the stale key with a schema-conforming payload. Without this
// the retry loop re-dispatches with no tool_choice override, the marginal
// model produces tool_use_no_calls again, and the run exhausts its budget.
//
// A coord-store-unavailable surface or a timeout returns empty: forcing
// the write tool would not help and could mask the real fault.
//
// Expected:
//   - gateErr is the *swarm.GateError the post-member gate returned.
//
// Returns:
//   - "tool:coordination_store" for the narration-without-write signature
//     or a schema-validation failure on a coordination_store payload;
//     "" otherwise (leave the retry unconstrained).
//
// Side effects:
//   - None.
func forcedToolChoiceForGate(gateErr error) string {
	var ge *swarm.GateError
	if !errors.As(gateErr, &ge) || ge.Reason == "" {
		return ""
	}
	// The result-schema runner's no-output reason names the coordination_store
	// write and the missing tool call. Key off that signature so we only
	// force when forcing is the right correction.
	// Schema validation failures also trigger the force: the member wrote
	// a coordination_store key but the payload was incomplete or malformed,
	// and forcing the write is the correct recovery.
	reason := strings.ToLower(ge.Reason)
	if strings.Contains(reason, "coordination_store") ||
		(strings.Contains(reason, "no member output") && strings.Contains(reason, "tool call")) ||
		strings.Contains(reason, "schema validation failed") {
		return "tool:" + forcedCoordinationStoreTool
	}
	return ""
}
