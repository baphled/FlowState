package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/turn"
)

// runnerForSwarm returns the cached *swarm.Runner for swarmID,
// constructing it via the configured factory on first use. Subsequent
// calls with the same id return the same pointer so retry/breaker
// state accumulates across delegations within the swarm run.
//
// Expected:
//   - swarmID is the active swarm.Context.SwarmID; non-empty.
//   - manifest is the manifest backing swarmID; may be nil when the
//     registry could not resolve it (the factory falls back to
//     defaults).
//
// Returns:
//   - The cached or freshly-built *swarm.Runner.
//
// Side effects:
//   - Inserts a new Runner into runnerCache on first miss.
func (d *DelegateTool) runnerForSwarm(swarmID string, manifest *swarm.Manifest) *swarm.Runner {
	if cached, ok := d.runnerCache.Load(swarmID); ok {
		if runner, isRunner := cached.(*swarm.Runner); isRunner {
			return runner
		}
	}
	factory := d.runnerFactory
	if factory == nil {
		factory = defaultRunnerFactory
	}
	runner := factory(manifest)
	actual, _ := d.runnerCache.LoadOrStore(swarmID, runner)
	if existing, ok := actual.(*swarm.Runner); ok {
		return existing
	}
	return runner
}

// RunnerForSwarmIDForTest exposes the cached Runner for swarmID so
// wiring tests can pin runner-cache identity (P0.1 verification).
// Production code never calls this; it is intentionally a thin
// pointer accessor.
//
// Expected: parameters for RunnerForSwarmIDForTest.
// Returns: result of RunnerForSwarmIDForTest.
// Side effects: None.
func (d *DelegateTool) RunnerForSwarmIDForTest(swarmID string) *swarm.Runner {
	if cached, ok := d.runnerCache.Load(swarmID); ok {
		if runner, isRunner := cached.(*swarm.Runner); isRunner {
			return runner
		}
	}
	return nil
}

// manifestForSwarm returns the swarm.Manifest backing swarmID via the
// installed registry, or nil when no registry is wired or no manifest
// is registered. Pulled into a helper so the runner-cache lookup can
// stay focused on the cache contract.
//
// Expected: parameters for manifestForSwarm.
// Returns: result of manifestForSwarm.
// Side effects: None.
func (d *DelegateTool) manifestForSwarm(swarmID string) *swarm.Manifest {
	if d.swarmRegistry == nil {
		return nil
	}
	m, ok := d.swarmRegistry.Get(swarmID)
	if !ok {
		return nil
	}
	return m
}

// tryDispatchSwarmTarget routes delegate calls whose target is a sub-
// swarm id (listed in the active swarm context's Members[]) through
// DispatchSwarmMembers. Returns handled=true exactly when this branch
// took ownership of the call, so Execute can short-circuit and skip
// the agent-engine path entirely.
//
// Meta-Swarm Coordinator Architecture (May 2026) — Phase 3 entry seam.
//
// Expected:
//   - ctx is the lead-side delegation context.
//   - input is the raw tool.Input as Execute received it.
//
// Returns:
//   - result, true, nil — swarm dispatch succeeded; Execute returns
//     this directly.
//   - tool.Result{}, true, err — target IS a swarm but dispatch failed
//     (in-swarm gate, registry miss, etc.). Execute returns the error
//     verbatim so the caller's transcript shows the failure on the
//     swarm-target path rather than reverting to the agent-target
//     error message.
//   - tool.Result{}, false, nil — target is NOT a swarm in the active
//     context; Execute proceeds with the normal agent-target flow.
//
// Side effects:
//   - On the handled=true path, fans out member work through
//     DispatchSwarmMembers (which streams per-member via the resolved
//     streamers).
func (d *DelegateTool) tryDispatchSwarmTarget(ctx context.Context, input tool.Input) (tool.Result, bool, error) {
	if d.swarmRegistry == nil {
		return tool.Result{}, false, nil
	}
	swarmCtx, ok := d.activeSwarmContextForCtx(ctx)
	if !ok || swarmCtx == nil {
		return tool.Result{}, false, nil
	}
	// Lift subagent_type + message off raw input. We deliberately don't
	// call parseDelegationParams here so a swarm-target dispatch never
	// triggers the full handoff/category parse: if the caller mixed a
	// swarm-id target with handoff fields, those are silently dropped
	// (sub-swarm dispatch carries its own chain-prefix derived from
	// the manifest, not the parent's handoff). This is intentional —
	// handoff semantics apply to agent-target calls only.
	subagentType, _ := input.Arguments["subagent_type"].(string)
	message, _ := input.Arguments["message"].(string)
	if subagentType == "" {
		return tool.Result{}, false, nil
	}
	// Agent-target precedence: if the id resolves to an agent in the
	// registry, the agent-target path wins. Mirrors swarm.Resolve's
	// agent-first ordering and the validator's collision rejection.
	if d.registry != nil {
		if _, found := d.registry.GetByNameOrAlias(subagentType); found {
			return tool.Result{}, false, nil
		}
	}
	// Must be in the active swarm's Members[] — the in-swarm gate's
	// shadow rule applies to swarm-id targets identically to agent-id
	// targets. Permissive orchestrators bypass this check: a
	// `delegation.scope: permissive` lead may dispatch any swarm in
	// the registry regardless of the active swarm's roster.
	if !d.delegation.IsPermissive() && !containsAgent(swarmCtx.Members, subagentType) {
		return tool.Result{}, false, nil
	}
	// Must resolve to a swarm in the registry.
	subSwarm, found := d.swarmRegistry.Get(subagentType)
	if !found || subSwarm == nil {
		return tool.Result{}, false, nil
	}

	// Build the child swarm Context. NestSubSwarm carries the
	// parent's chain-prefix and depth forward so the inner runner's
	// errors carry the full parent/child trace.
	childCtx := swarmCtx.NestSubSwarm(subSwarm.ID)
	childCtx.SwarmID = subSwarm.ID
	childCtx.LeadAgent = subSwarm.Lead
	childCtx.Members = append([]string(nil), subSwarm.Members...)
	childCtx.Gates = append([]swarm.GateSpec(nil), subSwarm.Harness.Gates...)

	if err := d.DispatchSwarmMembers(ctx, &childCtx, subSwarm.Members, message); err != nil {
		return tool.Result{}, true, fmt.Errorf("swarm-target dispatch %q failed: %w", subagentType, err)
	}

	chainID := childCtx.ChainPrefix
	output := fmt.Sprintf(
		"Dispatched sub-swarm %q (%d members under %s). Member outputs streamed through the active session.",
		subSwarm.ID, len(subSwarm.Members), subSwarm.Lead,
	)
	if chainID != "" {
		output += fmt.Sprintf("\n\n---\n[coordination_chain] %s\nThe delegated agent may have written structured findings to the coordination store. Use the coordination_store tool to read key \"%s\" or scan for keys with prefix \"%s/\" before proceeding.", chainID, chainID, chainID)
	}
	// Synthesised result. The caller (transcript renderer) only needs
	// confirmation that the swarm fan-out ran; per-member outputs are
	// streamed through DispatchSwarmMembers' progress hooks and end up
	// in the parent session's stream chunks via teeToParentStream.
	return tool.Result{
		Output: output,
		Title:  "delegate → " + subSwarm.ID,
		Metadata: map[string]interface{}{
			"swarm_id":   subSwarm.ID,
			"lead_agent": subSwarm.Lead,
			"members":    subSwarm.Members,
			"chainId":    chainID,
		},
	}, true, nil
}

// DispatchSwarmMembers fans out the swarm-context members through
// swarm.DispatchMembers honouring the manifest's Harness.Parallel +
// Harness.MaxParallel. Each member's per-iteration work is the same
// per-attempt closure executeSync drives for a single delegation:
// resolveStreamer + Stream + collectWithProgress wrapped in the
// per-swarm Runner cached by SwarmID.
//
// The PostMember hook fires post-member gates on the worker goroutine
// before the semaphore slot releases, so peers still in flight do not
// proceed past a failed validation. This is the §T37 contract baked
// into DispatchOptions.PostMember.
//
// MaxParallel is clamped against d.spawnLimits.MaxTotalBudget so a
// manifest cannot fan out beyond the engine-level concurrency
// ceiling. Recursion into sub-swarms (Task 5) shares the same ceiling
// to bound active fan-out across all depths (P0.6).
//
// Expected:
//   - ctx is the lead-side delegation context.
//   - swarmCtx is the active swarm context; non-nil.
//   - members is the roster to fan out across; empty yields nil.
//   - message is the prompt forwarded to every member.
//
// Returns:
//   - nil when every member completes and every post-member gate
//     passes.
//   - The first error otherwise.
//
// Side effects:
//   - Streams per-member work through the resolved streamers.
//   - Fires the PostMember hook for each member as it lands.
func (d *DelegateTool) DispatchSwarmMembers(ctx context.Context, swarmCtx *swarm.Context, members []string, message string) error {
	if swarmCtx == nil || len(members) == 0 {
		return nil
	}
	manifest := d.manifestForSwarm(swarmCtx.SwarmID)
	parallel := false
	maxParallel := 0
	if manifest != nil {
		parallel = manifest.Harness.Parallel
		maxParallel = manifest.Harness.MaxParallel
	}
	maxParallel = d.clampMaxParallelToBudget(maxParallel, len(members))

	memberRunner := d.buildMemberRunner(swarmCtx, message)
	postMember := d.buildPostMemberHook()

	return swarm.DispatchMembers(ctx, members, memberRunner, swarm.DispatchOptions{
		Parallel:    parallel,
		MaxParallel: maxParallel,
		PostMember:  postMember,
	})
}

// clampMaxParallelToBudget bounds the manifest's MaxParallel against
// the engine's spawn-limit MaxTotalBudget so a single swarm cannot
// monopolise the worker pool. A zero / negative input is treated as
// "no swarm-level cap" and clamped down to the budget.
//
// Expected:
//   - manifestMax is opts.MaxParallel from the manifest (may be 0).
//   - rosterSize is len(members); used as the floor for unset caps.
//
// Returns:
//   - The effective ceiling: min(rosterSize, MaxTotalBudget) when
//     manifestMax <= 0; min(manifestMax, MaxTotalBudget) otherwise.
//
// Side effects:
//   - None.
func (d *DelegateTool) clampMaxParallelToBudget(manifestMax, rosterSize int) int {
	budget := d.spawnLimits.MaxTotalBudget
	cap := manifestMax
	if cap <= 0 {
		cap = rosterSize
	}
	if budget > 0 && cap > budget {
		cap = budget
	}
	return cap
}

// buildMemberRunner returns the swarm.MemberRunner closure that drives
// one member's stream through the per-swarm Runner. The closure is
// shared across every member of the fan-out so retry/breaker state
// accumulates correctly on the cached Runner.
//
// When the member id resolves to another swarm in the registry, the
// closure recurses into DispatchSwarmMembers with a child context
// constructed via Context.NestSubSwarm so the chain prefix and depth
// carry the parent/child trace (Task 5). The recursion shares the
// engine's spawn-limit MaxTotalBudget so active fan-out across all
// depths cannot exceed the configured ceiling (P0.6).
//
// Expected:
//   - swarmCtx is the active swarm context.
//   - message is the prompt forwarded to every member.
//
// Returns:
//   - A swarm.MemberRunner the dispatcher invokes per member.
//
// Side effects:
//   - On invocation: resolves the target engine OR recurses into a
//     nested DispatchSwarmMembers call.
func (d *DelegateTool) buildMemberRunner(swarmCtx *swarm.Context, message string) swarm.MemberRunner {
	memberTimeout := d.memberTimeoutForSwarm(swarmCtx.SwarmID)
	return func(ctx context.Context, memberID string) error {
		if subSwarm := d.resolveSubSwarm(memberID); subSwarm != nil {
			// Sub-swarm recursion: the child swarm carries its own
			// HarnessConfig (and thus its own MemberTimeout). The
			// per-leaf wrap inside the recursive DispatchSwarmMembers
			// owns the deadline; wrapping here would double-cap the
			// child's members with the parent's budget.
			child := swarmCtx.NestSubSwarm(memberID)
			child.LeadAgent = subSwarm.Lead
			child.Members = append([]string(nil), subSwarm.Members...)
			child.Gates = append([]swarm.GateSpec(nil), subSwarm.Harness.Gates...)
			return d.DispatchSwarmMembers(ctx, &child, subSwarm.Members, message)
		}
		// Case-insensitive engine resolution mirrors containsAgent's
		// EqualFold contract at the first hop (in-swarm gate). YAML
		// rosters in PascalCase (Tech-Lead, Senior-Engineer) vs LLM
		// outputs in mixed case (tech-Lead) used to surface here as
		// `no engine for swarm member "tech-Lead"` despite the in-
		// swarm gate accepting the same id one hop earlier. canonicalID
		// is the engines-map key (not memberID) so downstream
		// delegationTarget.agentID carries the canonical case the rest
		// of the engine indexes by — streamers map, session manager,
		// transcript stamping.
		canonicalID, eng, ok := d.lookupEngineByID(memberID)
		if !ok {
			return fmt.Errorf("no engine for swarm member %q", memberID)
		}
		runner := d.runnerForSwarm(swarmCtx.SwarmID, d.manifestForSwarm(swarmCtx.SwarmID))
		target := delegationTarget{
			agentID: canonicalID,
			engine:  eng,
			message: message,
		}
		// HarnessConfig.MemberTimeout caps the per-member await so a
		// stalled child surfaces as context.DeadlineExceeded rather
		// than hanging the parent forever. Zero preserves the
		// historical no-deadline contract; positive wraps the
		// per-member ctx so the error flows out through streamAndCollect
		// and dispatchParallel's first-error cancel cascade unwinds
		// any siblings still in flight. Symptom: session 3255e2ee.
		if memberTimeout > 0 {
			var cancelMemberTimeout context.CancelFunc
			ctx, cancelMemberTimeout = context.WithTimeout(ctx, memberTimeout)
			defer cancelMemberTimeout()
		}
		// Delegation Regression Fix (May 2026) — every `delegate()`
		// invocation from inside the engine MUST result in a child
		// session with the correct parent_id and agent_id. The
		// coordinator's session must NEVER have tool calls stamped
		// with a non-coordinator agentID via AppendMessage.
		//
		// Pre-fix, the swarm-target dispatch path (tryDispatchSwarmTarget
		// → DispatchSwarmMembers → here) bypassed the child-session
		// bootstrap that executeSync does for agent-target delegations,
		// so the accumulator (wrapWithAccumulator → AccumulateStream)
		// called AppendMessage(parentSessionID, {AgentID: memberID,
		// ...}) — stamping personas onto rows in the coordinator's
		// session row store with zero spawned children.
		//
		// Canonical production symptom: session
		// 1e99f552-5223-4c38-8d83-225ea3ba16af.meta.json (May 2026)
		// has tool calls + assistant rows stamped with explorer,
		// librarian, analyst, plan-writer, plan-reviewer — all on the
		// coordinator's session, with `grep -l "parent_id":"1e99f552-..."`
		// returning empty.
		//
		// bootstrapMemberSession mirrors executeSync's child-session
		// resolve+attach+rebind: resolveOrCreateSession spawns the
		// child via CreateWithParentAndChain with parent_id =
		// coordinator and agent_id = memberID, persistChildBrief
		// stamps the brief into the child's row store, attachSessionStore
		// re-points the member engine's row store at the child,
		// session.IDKey is rebound so every downstream
		// sessionIDFromContext(dispatchCtx) call inside streamAndCollect
		// — most importantly wrapWithAccumulator's sessionID arg —
		// resolves to the child id, not the parent's.
		//
		// Plans/Child Session Turn Registry Plumbing (May 2026)
		// §Item 2d: bootstrap also mints a per-member child Turn via
		// StartOrReuse and injects the turn ctx triad into
		// dispatchCtx — the accumulator's turnAwareAppender then
		// fans every persisted child-session message onto the SAME
		// registry the API server's long-poll endpoint reads from.
		// handle.turnID is "" when the registry is nil (legacy test
		// constructor) or StartOrReuse soft-fails; every lifecycle
		// site below short-circuits on the empty id per D7.
		dispatchCtx, handle, cleanup := d.bootstrapMemberSession(ctx, target, swarmCtx.ChainPrefix)
		defer cleanup()

		// Closure-internal attempt counter — Plans/Child Session
		// Turn Registry Plumbing (May 2026) §S10.swarm retry-
		// boundary mechanism. Mirrors executeSync's
		// runStreamThroughRunner closure-counter at delegation.go:
		// 2613-2625 (PR2a §Item 2c option (i)). The runner re-
		// invokes this closure on every retry attempt; on attempts
		// > 0 we wipe handle.turnID's MessagesAdded slice via
		// ResetForRetry so attempt-N+1's chunks do NOT pile on top
		// of attempt-N's stale partial-stream rows.
		//
		// Per-member isolation: the counter is captured fresh per
		// closure invocation (one closure per member), so alpha's
		// retry counter cannot trip a Reset on bravo's Turn even
		// when both members are in flight. This is the load-bearing
		// isolation §S10.swarm pins.
		//
		// Empty handle.turnID and nil registry short-circuit
		// silently — the legacy no-turn-registry path keeps the
		// historical behaviour.
		var result delegationResult
		attempt := 0
		dispatchErr := runner.Dispatch(dispatchCtx, canonicalID, func(innerCtx context.Context, _ string) error {
			if attempt > 0 && d.turnRegistry != nil && handle.turnID != "" {
				// ResetForRetry returns ErrTurnTerminal when the
				// turn has already terminated; the caller-side
				// discipline guarantees we never call it on a
				// terminal turn so the swallow is a backstop, not
				// a load-bearing path. Mirrors PR2a's pattern at
				// delegation.go:2615-2621.
				_ = d.turnRegistry.ResetForRetry(handle.turnID)
			}
			attempt++
			return d.streamAndCollect(innerCtx, target, &result)
		})

		// Plans/Child Session Turn Registry Plumbing (May 2026)
		// §D8 option (ii) — terminal discipline lives INSIDE the
		// per-member closure (not at the cleanup defer) because the
		// per-member ModelInfo only resolves here: target.engine is
		// the member-specific engine, target.engine.LastModel() /
		// LastProvider() carry the member's failover outcome.
		// Hoisting Complete/Fail to the cleanup closure would lose
		// that telemetry — the cleanup closure has no per-member
		// ModelInfo in scope and would have to read LastModel /
		// LastProvider AFTER the engine's per-call state has
		// already been overwritten by the next member in the fan-
		// out's iteration. Mirrors executeSync's terminal-then-
		// cleanup ordering at delegation.go:2529-2536 / 2497-2511.
		if dispatchErr != nil {
			// Fail the per-member child Turn BEFORE the deferred
			// cleanup runs. The handle's empty-turnID guard inside
			// failMemberTurnIfOwned short-circuits on the registry-
			// nil and soft-fail paths so back-compat for the
			// pre-plumbing callsite footprint holds per D7.
			d.failMemberTurnIfOwned(&handle, dispatchErr)
			return dispatchErr
		}

		// Complete the per-member child Turn with the member's
		// resolved (provider, model) pair. handle.ownedByCaller flip
		// is the load-bearing guard for the §S7.5 R2-defence path
		// where a gate failure (or any later surface) fires AFTER
		// Complete: failMemberTurnIfOwned reads ownedByCaller FIRST
		// and short-circuits before invoking Fail on a terminal
		// Turn. Empty turnID short-circuits both the Complete and
		// the ownedByCaller flip — the registry-nil and soft-fail
		// paths leave the historical no-Turn-channel behaviour
		// intact.
		if d.turnRegistry != nil && handle.turnID != "" {
			_ = d.turnRegistry.Complete(handle.turnID, turn.ModelInfo{
				Provider: target.engine.LastProvider(),
				Model:    target.engine.LastModel(),
			})
			handle.ownedByCaller = true
		}
		return nil
	}
}

// bootstrapMemberSession spawns a per-member child session, persists
// the parent's brief into the child, attaches the member engine's
// row store to the child's session id, mints a per-member child Turn
// via StartOrReuse on the spawned child session, and returns a ctx
// rebound to the child sessionID (with masked provider/model overrides
// so the parent's selection does not leak into the member engine) plus
// a per-member turn handle the buildMemberRunner closure uses to
// transition the Turn terminal (Complete on success / Fail on dispatch
// error).
//
// Mirrors the seam executeSync uses for agent-target delegations
// (resolveOrCreateSession → persistChildBrief → attachSessionStore →
// StartOrReuse → context.WithValue(session.IDKey{}, child) +
// turn.WithTurnID + session.WithAccumulatorTurnID +
// session.WithTurnRecorder). Plans/Child Session Turn Registry
// Plumbing (May 2026) §Item 2d (swarm-target plumbing, added in round
// 2 per blocker B1): the ctx triad makes the child engine's
// accumulator fan persisted child-session messages onto the SAME
// registry the API server's long-poll endpoint projects from, closing
// the swarm-path live-UI parity gap that PR2a closed only for the
// single-target path.
//
// Expected:
//   - ctx is the per-member dispatch ctx carrying the coordinator's
//     session id under session.IDKey{}.
//   - target.agentID is the resolved member id.
//   - target.engine is the member's isolated engine.
//   - target.message is the brief the coordinator sent to the member.
//   - chainID is the active swarm's chain prefix; empty falls through
//     to resolveOrCreateSession's synthetic-id branch.
//
// Returns:
//   - dispatchCtx with session.IDKey rebound to the spawned child id,
//     provider/model overrides masked, and (when the registry is
//     wired) the turn ctx triad (turn.WithTurnID +
//     session.WithAccumulatorTurnID + session.WithTurnRecorder)
//     installed so the accumulator's turnAwareAppender fans every
//     persisted child-session message onto the registry's
//     MessagesAdded slice.
//   - handle carries the per-member child Turn id and the
//     ownedByCaller flag the per-member closure flips on Complete.
//     handle.turnID is empty when the registry is nil OR StartOrReuse
//     soft-fails — downstream lifecycle sites short-circuit on the
//     empty id per D7.
//   - cleanup is the closer the caller must defer; it closes the
//     attached file store (no-op when no factory) and seals the
//     spawned child session via closeSessionIfManaged.
//
// Side effects:
//   - May call sessionCreator.CreateWithParentAndChain (or sessionManager's
//     equivalent fallback) — both register a child session in memory and
//     persist its sidecar when a sessionsDir is configured.
//   - May call messageAppender.AppendMessage to persist the brief into
//     the child session row store.
//   - May call attachSessionStore to swap the member engine's
//     FileContextStore to the child id (no-op when storeFactory is nil).
//   - May call d.turnRegistry.StartOrReuse to mint a child Turn keyed
//     on the spawned child sessionID. Nil registry short-circuits.
//
// NOTE: this seam intentionally does NOT publish a `delegation.started`
// / `delegation.completed` event. The swarm-target path emits its
// progress through SwarmEvents (DispatchMembers) and the per-chunk
// transcript via teeToParentStream; layering a second bus event from
// here would double-count delegation lifecycles. Future refactor:
// converge executeSync's per-delegation event publishing with the
// swarm-target path's SwarmEvents under a single emitter — out of
// scope for this commit's contract-closing fix.
func (d *DelegateTool) bootstrapMemberSession(
	ctx context.Context,
	target delegationTarget,
	chainID string,
) (context.Context, memberTurnHandle, func()) {
	childID := d.resolveOrCreateSession(ctx, target.agentID, "", chainID)
	d.persistChildBrief(childID, target.agentID, target.message)
	closeStore := d.attachSessionStore(target.engine, childID)
	dispatchCtx := context.WithValue(ctx, session.IDKey{}, childID)
	// Cascade contract for swarm-member sessions: UI > manifest > global.
	// Same rationale as executeSync — see the resolveChildModelOverride
	// docstring. The coordinator's override (UI tier) does NOT propagate;
	// the child member's own manifest tier wins; fall through to global
	// when the member has no preferred_models. Without this, swarm
	// members silently ran on the engine's global default regardless of
	// what their manifests declared.
	memberProv, memberModel := d.resolveChildModelOverride(target)
	dispatchCtx = context.WithValue(dispatchCtx, session.ProviderOverrideKey{}, memberProv)
	dispatchCtx = context.WithValue(dispatchCtx, session.ModelOverrideKey{}, memberModel)
	// Thread the member's FULL preferred_models chain so failover walks
	// the member's own tiers before the global default. This is the
	// durable fix for the planning-loop halt: a member whose tier-0 is
	// momentarily unreachable now lands on its reliable tier-1/tier-2
	// instead of the global default (zai/glm-4.5), so the post-member
	// gate's structured write succeeds. No-op when the member declares
	// no chain.
	dispatchCtx = session.WithPreferredModels(dispatchCtx, d.resolveChildModelChain(target))

	// Plans/Child Session Turn Registry Plumbing (May 2026) §Item 2d —
	// mint a per-member child Turn keyed on the spawned childID
	// immediately after attachSessionStore. StartOrReuse is the right
	// primitive here per D1 (same rationale as executeSync's §Item 2b
	// site): resolveOrCreateSession may return an existing child
	// session whose prior Turn is stale, and StartOrReuse auto-
	// completes the stale entry before minting fresh. A nil registry
	// (legacy test constructors) short-circuits to the historical
	// "no live channel for child swarm members" behaviour — back-
	// compat for the pre-plumbing callsite footprint per D7.
	var handle memberTurnHandle
	if d.turnRegistry != nil {
		if id, turnErr := d.turnRegistry.StartOrReuse(childID); turnErr == nil {
			handle.turnID = id
			// Inject the child Turn ctx triad. Mirrors executeSync at
			// delegation.go:2467-2473. The accumulator's
			// turnAwareAppender reads the recorder closure off ctx and
			// fans every persisted child-session message (assistant,
			// thinking, tool_call, tool_result, delegation_started,
			// delegation) onto the registry's MessagesAdded slice,
			// which the API server projects to the frontend via
			// FindActiveBySession / handleListV1Sessions. Without the
			// triad the registry would carry only the mint/terminate
			// shell and the swarm-fan-out path would render an empty
			// live channel — defeating the purpose of the plumbing.
			dispatchCtx = turn.WithTurnID(dispatchCtx, id)
			dispatchCtx = session.WithAccumulatorTurnID(dispatchCtx, id)
			dispatchCtx = session.WithTurnRecorder(dispatchCtx, func(sid string, msg session.Message) {
				_ = d.turnRegistry.Append(sid, msg)
			})
		}
		// StartOrReuse's err surface is empty in practice (auto-
		// complete cannot fail; mint cannot fail without OOM). On the
		// soft-fail path handle.turnID stays "" and every downstream
		// Turn lifecycle site at the per-member closure short-
		// circuits — the swarm fan-out still completes, only the
		// live channel stays dark, matching pre-plumbing behaviour.
	}

	cleanup := func() {
		closeStore()
		d.closeSessionIfManaged(childID)
	}
	return dispatchCtx, handle, cleanup
}

// failMemberTurnIfOwned mirrors executeSync's `failChildTurnIfOwned`
// closure at the per-member layer. The handle's `ownedByCaller` flag
// flips to true the moment Complete fires on the per-member happy
// path; this helper reads the flag FIRST and short-circuits before
// invoking turnRegistry.Fail. Single-source-of-correctness defence
// (PR2a §S4.2 R2): the Fail-side ErrTurnTerminal silent-swallow at
// turn.go:796-797 is the backstop, NOT a substitute for caller-side
// discipline. Plans/Child Session Turn Registry Plumbing (May 2026)
// §S7.5 spec asserts Fail call-count remains 0 across the
// gate-after-Complete suffix on the swarm path identical to the
// single-target path.
//
// Expected:
//   - handle is the per-member handle returned by
//     bootstrapMemberSession; nil-safe via the empty-turnID guard.
//   - cause is the failure that triggered the call (dispatch error,
//     per-member timeout, runner-level terminal error).
//
// Side effects:
//   - May call d.turnRegistry.Fail when handle.ownedByCaller is false
//     AND the registry is wired AND the handle carries a non-empty
//     turnID. Every other case short-circuits silently.
//
// Returns: result of failMemberTurnIfOwned.
func (d *DelegateTool) failMemberTurnIfOwned(handle *memberTurnHandle, cause error) {
	if handle == nil || handle.ownedByCaller {
		return
	}
	if d.turnRegistry == nil || handle.turnID == "" {
		return
	}
	_ = d.turnRegistry.Fail(handle.turnID, cause)
}

// memberTimeoutForSwarm returns the HarnessConfig.MemberTimeout for
// the given swarm id, or zero when no manifest is registered. Pulled
// into a helper so the buildMemberRunner closure can capture the
// duration once at construction time rather than re-resolving the
// manifest on every member invocation.
//
// Expected:
//   - swarmID is the active swarm context id.
//
// Returns:
//   - The configured MemberTimeout, or zero when no registry / no
//     manifest / no field set (the no-deadline default).
//
// Side effects:
//   - None.
func (d *DelegateTool) memberTimeoutForSwarm(swarmID string) time.Duration {
	m := d.manifestForSwarm(swarmID)
	if m == nil {
		return 0
	}
	return m.Harness.MemberTimeout
}

// activeMemberTimeout returns the HarnessConfig.MemberTimeout from the
// active swarm context's manifest, or zero when no swarm context is
// active. Used by the single-target Execute path so a delegation
// invoked from inside a swarm (the common case) inherits the swarm's
// configured per-member deadline; standalone delegations (no swarm
// context) keep the historical no-deadline contract.
//
// Returns:
//   - The configured MemberTimeout, or zero in any of: no swarm
//     context active, no registry installed, no manifest registered,
//     MemberTimeout unset.
//
// Side effects:
//   - None.
//
// Expected: parameters for activeMemberTimeout.
func (d *DelegateTool) activeMemberTimeout() time.Duration {
	swarmCtx, ok := d.activeSwarmContext()
	if !ok || swarmCtx == nil {
		return 0
	}
	return d.memberTimeoutForSwarm(swarmCtx.SwarmID)
}

// resolveSubSwarm returns the manifest for memberID when the swarm
// registry has a swarm with that id AND no agent engine matches the
// id. The agent-vs-swarm precedence mirrors swarm.Resolve: agents win
// at the registry boundary so a swarm whose id collides with an
// agent stays callable as the agent (the validator already prevents
// this collision globally).
//
// Expected:
//   - memberID is one entry from the swarm-context roster.
//
// Returns:
//   - The child manifest when memberID resolves to a swarm and not an
//     agent.
//   - nil when memberID is an agent or unknown.
//
// Side effects:
//   - None.
func (d *DelegateTool) resolveSubSwarm(memberID string) *swarm.Manifest {
	if d.swarmRegistry == nil {
		return nil
	}
	if eng, ok := d.engines[memberID]; ok && eng != nil {
		return nil
	}
	m, ok := d.swarmRegistry.Get(memberID)
	if !ok {
		return nil
	}
	return m
}

// buildPostMemberHook wires DispatchOptions.PostMember to the
// engine's existing post-member gate dispatcher. The hook fires on
// the worker goroutine so peers still in flight see a gate failure
// before they release the semaphore slot — matches the §T37
// contract.
//
// Returns:
//   - The MemberPostHook closure; nil when no gate runner is wired
//     so the dispatcher skips the hook entirely.
//
// Side effects:
//   - On invocation: calls dispatchPostMemberGates on the engine.
//
// Expected: parameters for buildPostMemberHook.
func (d *DelegateTool) buildPostMemberHook() swarm.MemberPostHook {
	if d.gateRunner == nil {
		return nil
	}
	return func(ctx context.Context, memberID string, runErr error) error {
		// A member that already failed must not be gate-validated:
		// DispatchMembers records the member error itself, so gating
		// a failed member would misattribute the failure as a gate
		// failure. Structured so the failed path falls through to a
		// single terminal nil return rather than returning nil from
		// inside the error branch.
		var gateErr error
		if runErr == nil {
			// Parallel dispatch (DispatchMembers) does not thread the
			// lead's per-member chainID into the hook, so pass "" here:
			// the result-schema runner then suffix-scans for any
			// "<chain>/<suffix>" key rather than pinning one chainID.
			// Planning-loop runs sequentially (parallel: false) via
			// executeSync, which passes the concrete chainID — this hook
			// path only fires for genuinely-parallel swarms.
			gateErr = d.dispatchPostMemberGates(ctx, memberID, "")
		}
		return gateErr
	}
}

// salvageMemberOutputIfMissing is the FINAL-attempt floor under the
// post-member gate: when a single-output member produced content in its
// REPLY but never wrote it to the coord-store (the gpt-4o signature —
// narrate the artefact, skip the coordination_store(set) call), this
// writes the raw reply into the gate's resolved output_key so the gate
// can validate the content that already exists in the turn instead of
// failing the whole swarm over a missing key.
//
// Scope (all three are hard guards so the floor never guesses):
//   - A gate runner and an active swarm context must exist; otherwise
//     there is no key to resolve and salvage is a no-op.
//   - The member must have EXACTLY ONE post-member result-schema gate
//     carrying a non-empty OutputKey. A member with zero such gates has
//     no key to salvage into; a member with two-or-more is multi-output,
//     and salvage refuses to guess which slot the reply belongs to (those
//     members must write explicitly). All planning-loop / plan-sme
//     members are single-output, so production passes this guard.
//   - The resolved key must be MISSING or empty. A member that wrote its
//     own (richer) output keeps it — salvage only fills an empty slot,
//     never overwrites an explicit write.
//
// The reply is salvaged RAW (not formatDelegationOutput, which wraps in
// <task_result> tags that would pollute the markdown/prose the gate and
// downstream consumers read). A no-content reply is still written, but
// the post-member gate then fails closed (prose / render-mirror reject
// empty / whitespace), so the salvage NEVER publishes a junk plan — it
// only RECOVERS when the reply actually carries the artefact.
//
// Expected:
//   - ctx is the delegation context (carries the swarm scope).
//   - memberID is the agent id whose stream just completed.
//   - chainID is the lead-allocated chain identifier threaded through to
//     resolve {chainID}-templated output keys against the SAME namespace.
//   - reply is the member's final response text (result.response).
//
// Side effects:
//   - At most one Set on the coordination store, only when the guards
//     above all pass and the resolved key is currently empty.
//
// Returns: result of salvageMemberOutputIfMissing.
func (d *DelegateTool) salvageMemberOutputIfMissing(ctx context.Context, memberID, chainID, reply string) {
	if d.gateRunner == nil || d.coordinationStore == nil {
		return
	}
	swarmCtx, ok := d.activeSwarmContextForCtx(ctx)
	if !ok || swarmCtx == nil {
		return
	}
	// Single-output scope guard: gather the member's post-member result-
	// schema gates that carry an explicit OutputKey. Salvage proceeds ONLY
	// when there is exactly one — a multi-output member must write its own
	// keys (we will not guess which slot a single reply fills).
	matches := swarm.MemberGatesFor(swarmCtx.Gates, swarm.LifecyclePostMember, memberID)
	var resultSchemaWithKey []swarm.GateSpec
	for _, g := range matches {
		if g.Kind == "builtin:result-schema" && g.OutputKey != "" {
			resultSchemaWithKey = append(resultSchemaWithKey, g)
		}
	}
	if len(resultSchemaWithKey) != 1 {
		return
	}
	gate := resultSchemaWithKey[0]
	// Resolve the concrete key via the SAME logic the result-schema gate
	// reads from (CandidateKeys → candidateKeys) so the salvage write key
	// can never drift from the gate read key. The first candidate is the
	// canonical (highest-priority) slot the gate probes.
	keys := swarm.CandidateKeys(gate, swarm.GateArgs{
		SwarmID:     swarmCtx.SwarmID,
		ChainPrefix: swarmCtx.ChainPrefix,
		ChainID:     chainID,
		MemberID:    memberID,
		CoordStore:  d.coordinationStore,
	})
	if len(keys) == 0 {
		return
	}
	key := keys[0]
	// Only fill an EMPTY slot — never overwrite the member's own explicit
	// write (which is typically richer than the bare reply text). A present-
	// but-empty value is treated as missing so a member that wrote a hollow
	// "" can still be salvaged. A store read error is conservative: leave the
	// slot alone and let the gate report the real failure.
	if existing, err := d.coordinationStore.Get(key); err == nil && hasSubstantiveOutput(existing) {
		return
	} else if err != nil && !errors.Is(err, coordination.ErrKeyNotFound) {
		return
	}
	// Salvage the RAW reply verbatim. A no-content reply is written too, but
	// the post-member gate then rejects it (fail-closed), so no junk ships.
	_ = d.coordinationStore.Set(key, []byte(reply))
}

// hasSubstantiveOutput reports whether the stored value has meaningful
// content, as opposed to being empty, whitespace-only, or a bare empty
// JSON object/array. Mirrors the result-schema gate's non-empty predicate
// so the salvage slot stays in sync with the gate read key.
//
// Expected: parameters for hasSubstantiveOutput.
// Returns: result of hasSubstantiveOutput.
// Side effects: None.
func hasSubstantiveOutput(val []byte) bool {
	trimmed := strings.TrimSpace(string(val))
	return trimmed != "" && trimmed != "{}" && trimmed != "[]"
}

// dispatchPostMemberGates fires every post-member gate on the active
// swarm context whose Target matches memberID. Thin wrapper over
// dispatchMemberGates kept for call-site readability — executeSync
// still reads "post-member dispatch happens here" at a glance.
//
// Expected:
//   - ctx is the delegation context.
//   - memberID is the agent id whose stream just completed.
//
// Returns:
//   - nil when no gate runner is wired, no swarm context is in flight,
//     no gates target memberID, or every matching gate passes.
//   - The first *swarm.GateError otherwise.
//
// Side effects:
//   - Calls each matching gate's runner, which may read from the
//     coordination store.
//
// chainID is the lead-allocated coordination chain identifier for this
// delegation (target.chainID). It is threaded onto GateArgs.ChainID so
// {chainID}-templated output keys in the manifest resolve against the
// SAME namespace the member wrote to. Empty when the lead has not
// allocated one — the result-schema runner then suffix-scans.
// dispatchPostMemberGates evaluates every post-member gate for memberID
// WITHOUT publishing gate.failed. The post-member dispatcher is invoked
// once per retry attempt by the bounded re-delegation loop (executeSync,
// PostMemberGateMaxAttempts); publishing gate.failed here would emit one
// event per failed attempt — N events for a single logical halt. The
// loop owns the failure publication and calls publishGateFailed exactly
// once when the budget is exhausted. gate.evaluating / gate.passed
// success-path semantics are unchanged: a passing gate still publishes
// gate.passed every attempt (the success path is not retried).
func (d *DelegateTool) dispatchPostMemberGates(ctx context.Context, memberID, chainID string) error {
	return d.dispatchMemberGatesSilent(ctx, swarm.LifecyclePostMember, memberID, chainID)
}

// dispatchPreMemberGates fires every pre-member gate on the active
// swarm context whose Target matches memberID. Called by executeSync
// just before the targeted member's Stream is invoked so the gate
// runner can validate prerequisite coord-store keys exist. Phase 2
// of T-swarm-3.
//
// Expected:
//   - ctx is the delegation context.
//   - memberID is the agent id whose stream is about to start.
//
// Returns:
//   - nil under the same conditions as dispatchPostMemberGates.
//   - The first *swarm.GateError otherwise.
//
// Side effects:
//   - See dispatchMemberGates.
//
// chainID is the lead-allocated chain identifier (target.chainID),
// threaded through for {chainID}-templated output-key resolution.
func (d *DelegateTool) dispatchPreMemberGates(ctx context.Context, memberID, chainID string) error {
	return d.dispatchMemberGates(ctx, swarm.LifecyclePreMember, memberID, chainID)
}

// dispatchMemberGates fires every gate on the active swarm context
// whose When matches when ("pre-member" or "post-member") and whose
// Target matches memberID. Single helper underlying both
// dispatchPreMemberGates and dispatchPostMemberGates so the lifecycle
// fan-out logic (gate-runner / swarm-context / coord-store
// resolution) lives in exactly one place.
//
// Expected:
//   - ctx is the delegation context.
//   - when is a member-level lifecycle point.
//   - memberID is the agent id the runner is wrapping.
//
// Returns:
//   - nil when no gate runner is wired, no swarm context is in flight,
//     no gates match the (when, memberID) pair, or every matching gate
//     passes.
//   - The first *swarm.GateError otherwise.
//
// Side effects:
//   - Calls each matching gate's runner.
//   - Plans/Gate Bus Bridge — Engine to SSE and TUI (May 2026):
//     publishes gate.evaluating before dispatch, gate.failed per
//     halting gate, gate.passed once on a clean batch. Pass-event
//     policy: halt-class only on gate.failed.
func (d *DelegateTool) dispatchMemberGates(ctx context.Context, when, memberID, chainID string) error {
	swarmCtx, err := d.dispatchMemberGatesSilentCtx(ctx, when, memberID, chainID)
	if err != nil {
		// The pre-member dispatcher is not retried, so a single halt maps
		// to a single gate.failed — publish it here. swarmCtx is non-nil
		// whenever err is non-nil (the silent core only returns an error
		// after resolving the active swarm context).
		d.publishGateFailed(ctx, swarmCtx, when, memberID, err)
	}
	return err
}

// dispatchMemberGatesSilent evaluates every matching gate exactly as
// dispatchMemberGates does — publishing gate.evaluating before dispatch
// and gate.passed on a clean batch — but does NOT publish gate.failed on
// a halt. It returns the first *swarm.GateError so the caller can decide
// when to publish the failure. This is the path the bounded post-member
// retry loop uses so a single logical halt produces exactly one
// gate.failed (published once on budget exhaustion) instead of one per
// attempt. Side effects and nil-return conditions otherwise match
// dispatchMemberGates.
//
// Expected: parameters for dispatchMemberGatesSilent.
// Returns: result of dispatchMemberGatesSilent.
// Side effects: None.
func (d *DelegateTool) dispatchMemberGatesSilent(ctx context.Context, when, memberID, chainID string) error {
	_, err := d.dispatchMemberGatesSilentCtx(ctx, when, memberID, chainID)
	return err
}

// dispatchMemberGatesSilentCtx is the shared evaluation core. It returns
// the resolved swarm context alongside the gate error so the publishing
// wrapper (dispatchMemberGates) can attribute a gate.failed without
// re-resolving the context. The returned context is non-nil whenever the
// error is non-nil; both are nil/zero on the no-op and pass paths.
//
// Expected: parameters for dispatchMemberGatesSilentCtx.
// Returns: result of dispatchMemberGatesSilentCtx.
// Side effects: None.
func (d *DelegateTool) dispatchMemberGatesSilentCtx(ctx context.Context, when, memberID, chainID string) (*swarm.Context, error) {
	if d.gateRunner == nil {
		return nil, nil
	}
	swarmCtx, ok := d.activeSwarmContextForCtx(ctx)
	if !ok {
		return nil, nil
	}
	matches := swarm.MemberGatesFor(swarmCtx.Gates, when, memberID)
	if len(matches) == 0 {
		return nil, nil
	}
	args := swarm.GateArgs{
		SwarmID:     swarmCtx.SwarmID,
		ChainPrefix: swarmCtx.ChainPrefix,
		ChainID:     chainID,
		MemberID:    memberID,
		CoordStore:  d.coordinationStore,
	}
	d.publishGateEvaluating(ctx, swarmCtx, when, memberID, len(matches))
	report := swarm.Dispatch(ctx, d.gateRunner, matches, args)
	if report.Halted {
		return swarmCtx, report.Err
	}
	d.publishGatePassed(ctx, swarmCtx, when, memberID, len(matches))
	return swarmCtx, nil
}

// dispatchPreSwarmGatesOnce fires every pre-swarm gate on the active
// swarm context exactly once per swarm id. The "once" guarantee is
// the key contract for pre-swarm: even though the dispatcher is
// called from executeSync (which fires per delegation), the swarm
// runner spec promises pre-swarm fires ONCE at swarm start.
// prefiredSwarmIDs records firings; subsequent calls are no-ops for
// the same swarm.
//
// Expected:
//   - ctx is the delegation context (the lead's tool-call context).
//
// Returns:
//   - nil when no swarm is in flight, no pre-swarm gates exist on
//     the manifest, the gates have already fired this swarm run, or
//     every gate passes.
//   - The first *swarm.GateError otherwise. On failure, the swarm
//     id is NOT marked as fired so a retry from a fresh delegation
//     would attempt the gates again (Phase 2 still halts fail-fast,
//     so the retry only matters when an upstream caller catches the
//     error and re-invokes; future Phase 3 retry/rollback work will
//     rely on this invariant).
//
// Side effects:
//   - Calls each matching gate's runner under the swarm lifecycle
//     mutex.
func (d *DelegateTool) dispatchPreSwarmGatesOnce(ctx context.Context) error {
	if d.gateRunner == nil {
		return nil
	}
	swarmCtx, ok := d.activeSwarmContextForCtx(ctx)
	if !ok {
		return nil
	}
	if !d.markPreSwarmFiring(swarmCtx.SwarmID) {
		return nil
	}
	if err := d.runSwarmGates(ctx, swarmCtx, swarm.LifecyclePreSwarm); err != nil {
		d.unmarkPreSwarmFiring(swarmCtx.SwarmID)
		return err
	}
	return nil
}

// FlushSwarmLifecycle fires the post-swarm gates on the active swarm
// context (if any) and forgets the pre-swarm "fired" flag for that
// swarm id. The swarm-runner caller (cli.runPrompt / chat intent
// after the lead's stream returns) invokes this so a swarm-level
// `when: post` gate can validate the final aggregated state.
//
// Phase 2 still uses a single DelegateTool per app instance, so a
// long-lived process running multiple back-to-back swarm sessions
// MUST call FlushSwarmLifecycle between them. Skipping the flush
// would leak pre-swarm fired-state from the previous run and
// suppress the next session's pre-swarm dispatch.
//
// Expected:
//   - ctx is the swarm-runner's outer context (the same one driving
//     the lead's Stream).
//
// Returns:
//   - nil when no gate runner is wired, no swarm is in flight, or
//     every post-swarm gate passes.
//   - The first *swarm.GateError otherwise.
//
// Side effects:
//   - Calls each post-swarm gate's runner.
//   - Clears the pre-swarm fired marker for the swarm id.
func (d *DelegateTool) FlushSwarmLifecycle(ctx context.Context) error {
	if d.gateRunner == nil {
		return nil
	}
	swarmCtx, ok := d.activeSwarmContextForCtx(ctx)
	if !ok {
		return nil
	}
	defer d.unmarkPreSwarmFiring(swarmCtx.SwarmID)

	// Resolve the chainID for this run through the SAME authoritative
	// resolver the member-write preamble and the wave validator use, so the
	// publisher and post-swarm gate target the identical namespace the
	// members wrote under (the core regression: member writes under X, the
	// post-phase looks under Y). Precedence:
	//   1. Engine-assigned chainID (swarmCtx.ChainIDAssigned) — authoritative,
	//      overrides anything an LLM/member captured. This is the engine-owned
	//      namespace per ADR - Engine-Owned Workflow Mechanics.
	//   2. The chainID captured at member-dispatch time (already routed
	//      through the resolver, hence already engine-owned when one was
	//      assigned) — covers legacy seeded chains where the lead supplied
	//      an explicit chain and no per-run id was stamped.
	//   3. Otherwise empty — the publisher keeps its suffix-scan fallback so
	//      legacy seeded-chain runs are unaffected.
	chainID, _ := d.resolveSwarmChainNamespace(ctx, d.swarmChainIDForID(swarmCtx.SwarmID), false)

	// Deterministically publish the planning loop's approved plan to the
	// vault BEFORE the post-swarm honesty gate fires. This removes the LLM
	// from the persistence path: the only prior publish path was an agent
	// emitting a write tool call, which hits the synthesis-hang (Defect 2)
	// and never fires, leaving the user with "no plan in Obsidian". A
	// code-level write cannot be talked past by a stalled model. The
	// publisher is a no-op when there is no plan to publish or no output
	// dir is configured; a write failure is surfaced so the post-swarm
	// gate then fails rather than silently passing on a half-published
	// loop. See swarm.PublishPlanToVault.
	//
	// CHAIN-SAFE PUBLISH (the cross-chain-publish footgun): publish ONLY
	// for a swarm that declares an artifact-published post-swarm gate — the
	// publisher exists solely to feed that gate. Before this guard the
	// publish ran for EVERY swarm, so a non-planning swarm (e.g.
	// mental-health-swarm pinning a differing chain_prefix with no per-run
	// chain) resolved an empty chain and the publisher suffix-scanned a
	// FOREIGN chain's "*/plan", aborting the whole run. A swarm with no
	// such gate now never touches the publish path, so it can never publish
	// (or suffix-scan) into a chain it does not own.
	if err := d.publishPlanForSwarm(swarmCtx, chainID); err != nil {
		return err
	}

	return d.runSwarmGatesForChain(ctx, swarmCtx, swarm.LifecyclePostSwarm, chainID)
}

// publishPlanForSwarm writes the planning loop's approved plan from the
// coordination store to the vault via the deterministic publisher. It is
// a no-op when no coordination store or plan_output_dir is wired (the
// historical pre-publish behaviour for callers that have not opted in),
// or when there is simply no plan to publish.
//
// Expected:
//   - swarmCtx is the active swarm context; its Gates decide whether this
//     swarm publishes at all.
//   - chainID is the lead-allocated chain for this run (Bug 1). When
//     non-empty the publisher reads "<chainID>/plan" directly.
//
// Returns:
//   - nil when the publish succeeds or there is nothing to publish.
//   - The publisher's error when a write or record attempt fails — NOT
//     swallowed, so FlushSwarmLifecycle propagates it and the post-swarm
//     honesty gate (which would otherwise verify the publication) is not
//     reached on a corrupt half-write.
//
// Side effects:
//   - On a successful publish: one vault file written and one coord-store
//     "<chainID>/plan_publication" key set.
func (d *DelegateTool) publishPlanForSwarm(swarmCtx *swarm.Context, chainID string) error {
	if d.coordinationStore == nil || d.planOutputDir == "" {
		return nil
	}
	// Chain-safe publish (the footgun guard): publish ONLY when the swarm
	// declares an artifact-published gate — the publisher's whole reason to
	// exist is to feed that gate. A swarm without one has no plan to ship,
	// so this is a clean no-op rather than a publish that could suffix-scan
	// a foreign chain.
	if swarmCtx == nil || !swarm.DeclaresArtifactPublishedGate(swarmCtx.Gates) {
		return nil
	}
	// Belt-and-braces against the empty-chain suffix-scan: a publishing
	// swarm whose run never resolved an owned chain (no engine assignment,
	// no caller chain) must NOT fall through to PublishPlanToVault's
	// suffix-scan fallback, which would grab a foreign "*/plan". The
	// pinned-prefix per-run assignment (swarm.AssignRunChainID) means a
	// real run always has a chain here; an empty value is therefore an
	// honest no-op, never a cross-run scan.
	if strings.TrimSpace(chainID) == "" {
		return nil
	}
	if _, err := swarm.PublishPlanToVault(d.coordinationStore, d.planOutputDir, chainID); err != nil {
		return err
	}
	return nil
}

// runSwarmGates dispatches every swarm-level gate on swarmCtx whose
// When matches when. Used by both dispatchPreSwarmGatesOnce and
// FlushSwarmLifecycle; centralising the args / loop here keeps the
// callers focused on their own lifecycle invariants.
//
// Expected:
//   - swarmCtx is the active swarm context.
//   - when is "pre" or "post".
//
// Returns:
//   - nil when no gates match or every gate passes.
//   - The first *swarm.GateError otherwise.
//
// Side effects:
//   - Calls each matching gate's runner. MemberID is empty on the
//     args because swarm-level gates have no per-member fan-out.
//   - Plans/Gate Bus Bridge — Engine to SSE and TUI (May 2026):
//     publishes gate.evaluating before dispatch, gate.failed per
//     halting gate, gate.passed once on a clean batch.
func (d *DelegateTool) runSwarmGates(ctx context.Context, swarmCtx *swarm.Context, when string) error {
	return d.runSwarmGatesForChain(ctx, swarmCtx, when, "")
}

// runSwarmGatesForChain is runSwarmGates with an explicit chainID threaded
// onto GateArgs.ChainID (Bug 1). When chainID is non-empty a gate whose
// output_key carries a "{chainID}" template (e.g. the artifact-published
// honesty gate's "{chainID}/plan") resolves against the SAME chain the
// deterministic publisher just wrote, rather than suffix-scanning an
// arbitrary stale "*/plan" key. Empty chainID preserves the historical
// suffix-scan fallback for the pre-swarm caller and for runs where the
// lead never supplied a chain.
//
// Expected:
//   - swarmCtx is the active swarm context.
//   - when is "pre" or "post".
//   - chainID is the lead-allocated chain, or "" for the suffix-scan
//     fallback.
//
// Returns:
//   - nil when no gates match or every gate passes.
//   - The first *swarm.GateError otherwise.
//
// Side effects:
//   - Calls each matching gate's runner; publishes gate bus events.
func (d *DelegateTool) runSwarmGatesForChain(ctx context.Context, swarmCtx *swarm.Context, when, chainID string) error {
	matches := swarm.SwarmGatesFor(swarmCtx.Gates, when)
	if len(matches) == 0 {
		return nil
	}
	args := swarm.GateArgs{
		SwarmID:     swarmCtx.SwarmID,
		ChainPrefix: swarmCtx.ChainPrefix,
		ChainID:     chainID,
		CoordStore:  d.coordinationStore,
	}
	d.publishGateEvaluating(ctx, swarmCtx, when, "", len(matches))
	report := swarm.Dispatch(ctx, d.gateRunner, matches, args)
	if report.Halted {
		d.publishGateFailed(ctx, swarmCtx, when, "", report.Err)
		return report.Err
	}
	d.publishGatePassed(ctx, swarmCtx, when, "", len(matches))
	return nil
}

// markPreSwarmFiring records that pre-swarm gates are firing for
// swarmID. The returned bool reports whether the caller should
// proceed with the dispatch (true) or skip because another caller has
// already fired (false). The map is lazily initialised so the zero-
// value DelegateTool stays usable.
//
// Expected:
//   - swarmID is the active swarm's id.
//
// Returns:
//   - true when the caller should proceed with the dispatch.
//   - false when pre-swarm has already fired for this swarm id.
//
// Side effects:
//   - Mutates prefiredSwarmIDs under swarmLifecycleMu.
func (d *DelegateTool) markPreSwarmFiring(swarmID string) bool {
	d.swarmLifecycleMu.Lock()
	defer d.swarmLifecycleMu.Unlock()
	if d.prefiredSwarmIDs == nil {
		d.prefiredSwarmIDs = make(map[string]bool)
	}
	if d.prefiredSwarmIDs[swarmID] {
		return false
	}
	d.prefiredSwarmIDs[swarmID] = true
	return true
}

// unmarkPreSwarmFiring clears the pre-swarm fired marker for
// swarmID. Called from FlushSwarmLifecycle on swarm end and from the
// pre-swarm dispatcher's failure path so a subsequent retry attempts
// the gates again.
//
// Expected:
//   - swarmID is the active swarm's id.
//
// Side effects:
//   - Mutates prefiredSwarmIDs under swarmLifecycleMu.
//
// Returns: result of unmarkPreSwarmFiring.
func (d *DelegateTool) unmarkPreSwarmFiring(swarmID string) {
	d.swarmLifecycleMu.Lock()
	defer d.swarmLifecycleMu.Unlock()
	delete(d.prefiredSwarmIDs, swarmID)
}

// resolveSwarmChainNamespace is the SINGLE authoritative chain-namespace
// resolver for a swarm run. Every load-bearing site that answers "under
// what coord-store prefix does this run's data live" routes through here so
// the member-write preamble, the wave fan-in validator, the swarm gates,
// and the deterministic publisher all agree on ONE value.
//
// The recurring planning-loop doom-loop was exactly a disagreement between
// these sites: the planner LLM free-formed a chainID (with a slash) in its
// delegate prose; members + the publisher followed that free-form value
// while the wave validator resolved the engine-assigned swarm namespace —
// so the validator looked under X while the deliverable lived under Y and
// the loop exhausted its retries. Routing all sites through one resolver
// closes that divergence class at the source.
//
// Resolution order (engine-authority first — ADR Engine-Owned Workflow
// Mechanics, forward decision):
//
//  1. Engine-assigned chainID — when the dispatcher stamped a per-run id at
//     swarm start (swarmCtx.ChainIDAssigned, set by AssignRunChainID), that
//     value is AUTHORITATIVE and OVERRIDES any caller/LLM-supplied chainID.
//     This stops the planner free-forming a namespace the validator never
//     sees: inside an engine-owned swarm run the LLM cannot pick the chain.
//  2. Caller-supplied chainID — honoured (SLUGIFIED) only when the run has
//     no engine-assigned id (a legacy seeded static chain_prefix, a CLI
//     run, or a validate-harness fixture). Slugifying renders a free-form
//     value key-safe so it can never fracture "<chainID>/<suffix>" parsing.
//  3. Static swarm ChainPrefix — a swarm in flight whose prefix was never
//     stamped (a legacy seeded chain). This branch fires ONLY for the
//     member-dispatch path (staticPrefixFallback=true), preserving the
//     pre-fix member-target resolution. The post-swarm publisher passes
//     staticPrefixFallback=false so it keeps its suffix-scan fallback for
//     seeded-chain runs rather than targeting the static prefix verbatim.
//  4. Empty — no engine assignment, no caller chain, and the static-prefix
//     fallback is disabled. The caller (member dispatch) substitutes a
//     standalone delegation id; the publisher keeps its suffix-scan.
//
// Returns the resolved namespace and whether it is "owned" (engine-assigned
// or caller-supplied) — owned chains drive the member preamble and the
// recorded-chain capture; an empty/unowned result leaves the legacy
// suffix-scan fallback in place.
//
// Side effects:
//   - None (reads the ctx-scoped swarm context).
//
// Expected: parameters for resolveSwarmChainNamespace.
// Returns: result of resolveSwarmChainNamespace.
func (d *DelegateTool) resolveSwarmChainNamespace(ctx context.Context, callerChainID string, staticPrefixFallback bool) (chainID string, owned bool) {
	swarmCtx, inSwarm := d.activeSwarmContextForCtx(ctx)
	if inSwarm && swarmCtx != nil && swarmCtx.ChainIDAssigned && swarmCtx.ChainPrefix != "" {
		// Engine owns the chainID — the LLM/caller value is ignored so
		// every site reads the same authoritative namespace.
		return swarm.SlugifyChainID(swarmCtx.ChainPrefix), true
	}
	if slug := swarm.SlugifyChainID(callerChainID); slug != "" {
		// No engine assignment: honour the caller's choice (CLI, tests,
		// validate-harness, legacy seeded chains) but slugify it so a
		// free-form value can never break key parsing.
		return slug, true
	}
	if staticPrefixFallback && inSwarm && swarmCtx != nil && swarmCtx.ChainPrefix != "" {
		// Member-dispatch only: a swarm in flight with only a static prefix
		// (no per-run stamp, no caller chain) targets that prefix — the
		// pre-fix behaviour. The publisher disables this so it suffix-scans.
		return swarm.SlugifyChainID(swarmCtx.ChainPrefix), true
	}
	return "", false
}

// resolveMemberChainID resolves the chainID for a single member dispatch
// via the authoritative resolver, falling back to a standalone delegation
// id when neither an engine-assigned nor a caller-supplied chain exists.
// The member-dispatch path enables the static-prefix fallback so a swarm in
// flight with only a static prefix still targets it (pre-fix behaviour). The
// owned flag (chainIDFromCaller) gates the preamble injection + the
// post-swarm chain capture exactly as before, so backwards-compatible call
// sites that supply no chain still see no preamble.
//
// Side effects:
//   - None.
//
// Expected: parameters for resolveMemberChainID.
// Returns: result of resolveMemberChainID.
func (d *DelegateTool) resolveMemberChainID(ctx context.Context, callerChainID string) (chainID string, fromCaller bool) {
	if resolved, owned := d.resolveSwarmChainNamespace(ctx, callerChainID, true); owned {
		return resolved, true
	}
	return newDelegationChainID(), false
}

// recordSwarmChainID captures the lead-allocated chainID for swarmID so
// the post-swarm phase can thread the SAME chain to the deterministic
// publisher and the honesty gate (Bug 1 — wrong-chain targeting). The
// swarm.Context is immutable and shared across concurrent member
// closures, so the chainID is recorded here on the DelegateTool keyed by
// swarm id rather than mutating the context. A blank chainID is ignored
// so an auto-generated fallback never overwrites a real lead-supplied one
// (Execute only records when the caller actually supplied a chainID).
//
// Expected:
//   - swarmID is the active swarm's id.
//   - chainID is the caller-supplied chain; blank is a no-op.
//
// Side effects:
//   - Mutates swarmChainIDs under swarmLifecycleMu (lazy-init).
//
// Returns: result of recordSwarmChainID.
func (d *DelegateTool) recordSwarmChainID(swarmID, chainID string) {
	if swarmID == "" || strings.TrimSpace(chainID) == "" {
		return
	}
	d.swarmLifecycleMu.Lock()
	defer d.swarmLifecycleMu.Unlock()
	if d.swarmChainIDs == nil {
		d.swarmChainIDs = make(map[string]string)
	}
	d.swarmChainIDs[swarmID] = chainID
}

// swarmChainIDForID returns the most recent lead-allocated chainID
// recorded for swarmID, or "" when none was captured (the publisher then
// falls back to the suffix-scan). Read under swarmLifecycleMu so a
// concurrent member dispatch's recordSwarmChainID write is observed
// safely.
//
// Expected:
//   - swarmID is the active swarm's id.
//
// Returns:
//   - The captured chainID, or "" when none is recorded.
//
// Side effects:
//   - None (read-only under the lifecycle mutex).
func (d *DelegateTool) swarmChainIDForID(swarmID string) string {
	d.swarmLifecycleMu.Lock()
	defer d.swarmLifecycleMu.Unlock()
	return d.swarmChainIDs[swarmID]
}

// activeSwarmContext returns the swarm.Context installed on the lead
// engine, if any. T-swarm-2 wires the context onto the lead engine
// (the engine whose id matches the DelegateTool's sourceAgentID); the
// dispatcher reads it from there so post-member gates see the same
// gate slice the runner authored.
//
// DEPRECATED: prefer activeSwarmContextForCtx — the ctx-aware variant
// consults swarm.ScopeFromContext first, which is immune to cross-
// session mutation of the shared dispatchEngine.swarmContext field
// (planner session 39de3ab5-6173-4baf-9e20-7514a326bd3c leak). This
// no-ctx variant remains for callsites that haven't yet threaded ctx
// through and for legacy test surfaces.
//
// Expected:
//   - d.sourceAgentID names the lead engine in d.engines.
//
// Returns:
//   - The lead engine's swarm context and true when set.
//   - (nil, false) when no lead engine is registered, or when the
//     lead engine has no swarm context (single-agent mode).
//
// Side effects:
//   - None.
func (d *DelegateTool) activeSwarmContext() (*swarm.Context, bool) {
	// Owner-engine path is the canonical lookup: the DelegateTool is
	// installed on the lead's engine via eng.AddTool, and the lead's
	// engine is exactly where DispatchSwarm sets the swarm context.
	// Reading from d.engines[d.sourceAgentID] doesn't work here because
	// buildDelegateMaps explicitly EXCLUDES the lead from the targets
	// map (see app.go's `if agentManifest.ID == excludeID { continue }`)
	// — the lead can never delegate to itself, so its engine is absent
	// from that map by design. Without WithOwnerEngine wiring we'd
	// silently fall through and gates would never fire.
	if d.ownerEngine != nil {
		if swarmCtx := d.ownerEngine.SwarmContext(); swarmCtx != nil {
			return swarmCtx, true
		}
	}
	// Backwards-compatible fallback for tests that wired d.engines
	// without WithOwnerEngine: try the targets map. This path stays
	// inert for lead-style DelegateTools (their id is the excluded
	// one) but covers the historical "sub-DelegateTool inside a target
	// engine" case where the target's id IS in the map.
	if d.engines == nil {
		return nil, false
	}
	leadEngine, ok := d.engines[d.sourceAgentID]
	if !ok || leadEngine == nil {
		return nil, false
	}
	swarmCtx := leadEngine.SwarmContext()
	if swarmCtx == nil {
		return nil, false
	}
	return swarmCtx, true
}

// activeSwarmContextForCtx returns the per-turn swarm context. The
// dispatch boundary (dispatcher / orchestrator / swarm.DispatchSwarm)
// attaches the turn's scope via swarm.WithScope; this lookup reads
// that scope back so the gate's view of the active swarm is immune
// to any mid-turn writes to the SHARED dispatchEngine.swarmContext
// field. Pre-fix, the cross-session race on the shared engine
// surfaced as `agent not in delegation allowlist: "plan-writer" not
// in swarm "meta-swarm" members` in a SOLO planner session whose
// dispatcher had correctly installed planning-loop (session
// 39de3ab5-6173-4baf-9e20-7514a326bd3c).
//
// Resolution order:
//
//  1. swarm.ScopeFromContext(ctx) — when the dispatcher attached a
//     scope (scoped=true), that decision is authoritative. A nil
//     *Context inside the scope means "this turn is standalone";
//     callers MUST NOT fall back to engine state in that case
//     because the dispatcher explicitly chose no-swarm.
//  2. Engine-state fallback — legacy callers / tests that haven't
//     migrated ctx wiring continue to work via the
//     activeSwarmContext() path. This branch is the one the
//     pre-existing tests exercise.
//
// Expected:
//   - ctx may be nil; treated as "no scope attached" → engine-state
//     fallback.
//
// Returns:
//   - The active swarm context and true when one is in flight.
//   - (nil, false) when the turn is standalone OR no swarm is set.
//
// Side effects:
//   - None.
func (d *DelegateTool) activeSwarmContextForCtx(ctx context.Context) (*swarm.Context, bool) {
	if sc, scoped := swarm.ScopeFromContext(ctx); scoped {
		// Dispatcher attached a per-turn scope. Honour it verbatim —
		// nil sc means standalone (do not fall through to engine
		// state, which may be polluted by a cross-session write).
		if sc == nil {
			return nil, false
		}
		return sc, true
	}
	// No scope attached → legacy engine-state fallback.
	return d.activeSwarmContext()
}

// emitDelegationEvent sends a DelegationInfo chunk to the output channel when available.
//
// Expected:
//   - hasOutput indicates whether delegation events should be published.
//   - base contains the delegation metadata to reuse for the emitted chunk.
//
// Side effects:
//   - Attempts a non-blocking send to the output channel if it's still open.
//   - Silently drops events if the channel is full or closed (common when parent context is cancelled).
//   - Recovers from panic if the channel was closed by the parent context.
//
// Returns: result of emitDelegationEvent.
func (d *DelegateTool) emitDelegationEvent(
	outChan chan<- provider.StreamChunk, hasOutput bool,
	base provider.DelegationInfo, status string,
) {
	if !hasOutput {
		return
	}

	defer func() {
		if recover() == nil {
			return
		}
	}()

	info := base
	info.Status = status
	select {
	case outChan <- provider.StreamChunk{DelegationInfo: &info}:
	default:
	}
}

// deliverProgressEvent sends a ProgressEvent to the parent output channel when available.
//
// Expected:
//   - ctx carries the parent output channel via WithStreamOutput.
//   - toolCalls is the current count of tool invocations.
//   - lastTool is the name of the most recently invoked tool.
//   - startedAt is the time delegation began.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Attempts a non-blocking send to the parent output channel.
//   - Silently drops the event if the channel is full or absent.
func (d *DelegateTool) deliverProgressEvent(ctx context.Context, toolCalls int, lastTool string, startedAt time.Time) {
	outChan, ok := streamOutputFromContext(ctx)
	if !ok {
		return
	}

	activeDelegations := 0
	if d.backgroundManager != nil {
		activeDelegations = d.backgroundManager.ActiveCount()
	}

	ev := streaming.ProgressEvent{
		ToolCallCount:     toolCalls,
		LastTool:          lastTool,
		ActiveDelegations: activeDelegations,
		ElapsedTime:       time.Since(startedAt),
	}

	defer func() {
		if recover() == nil {
			return
		}
	}()
	select {
	case outChan <- provider.StreamChunk{Event: ev}:
	default:
	}
}

// formatRejection builds the human-readable body that follows the
// `agent not in delegation allowlist:` sentinel wrap. The shape mirrors
// the prompt block rendered at engine.go:2147-2202 so the model sees
// the same roster format it was instructed against:
//
//	"foo" not in swarm "dev-swarm" members:
//	  - `Tech-Lead`
//	  - `Researcher`
//
// or for the standalone (non-swarm) branch:
//
//	"foo" not in standalone allowlist:
//	  - `senior-engineer`
//
// The function additionally appends a self-correcting hint when:
//   - swarmCtx is non-nil and the rejected target is a member of some
//     OTHER known swarm in the registry (`Hint: 'foo' is a member of
//     swarm 'sub'. Delegate to 'sub' instead.`); or
//   - the rejected target is not in any other swarm but IS a known
//     agent (`Hint: 'foo' exists as an agent but isn't in this swarm's
//     roster.`).
//
// When the target is unknown to both registries, no hint is added so
// the bare rejection stays the right shape for a typo.
//
// The body is returned without the wrapped sentinel so callers can
// continue to use `fmt.Errorf("%w: %s", errAgentNotInAllowlist, body)`
// and `errors.Is(err, errAgentNotInAllowlist)` checks survive.
//
// Expected:
//   - swarmCtx is nil for the standalone branch, non-nil otherwise.
//   - roster is the active list (swarmCtx.Members or
//     d.delegation.DelegationAllowlist); may be empty.
//   - targetID is the rejected agent id; non-empty.
//
// Returns:
//   - A multi-line string ready to slot in after the wrapped sentinel.
//
// Side effects:
//   - None (read-only access to d.swarmRegistry and d.registry).
func (d *DelegateTool) formatRejection(swarmCtx *swarm.Context, roster []string, targetID string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "%q not in ", targetID)
	switch {
	case swarmCtx != nil && swarmCtx.SwarmID != "":
		fmt.Fprintf(&b, "swarm %q members:", swarmCtx.SwarmID)
	case swarmCtx != nil:
		// Defensive: a swarm context with an empty SwarmID. Should
		// not happen in production (NewContext always sets it), but
		// keep the label honest rather than rendering empty quotes.
		b.WriteString("swarm members:")
	default:
		b.WriteString("standalone allowlist:")
	}

	if len(roster) == 0 {
		b.WriteString("\n  (roster is empty)")
	}
	for _, id := range roster {
		b.WriteString("\n  - `")
		b.WriteString(id)
		b.WriteString("`")
	}

	if hint := d.rejectionHint(swarmCtx, targetID); hint != "" {
		b.WriteString("\n")
		b.WriteString(hint)
	}
	return b.String()
}

// formatPermissiveRejection builds the rejection body for a permissive
// orchestrator whose target was not found in either registry. The
// shape differs from formatRejection because the failure mode is
// different: a permissive lead is not constrained by a roster, so
// listing the swarm Members[] or static allowlist would be misleading.
// The message names the permissive scope explicitly so the user knows
// the target really doesn't exist anywhere — not that it was scope-
// restricted.
//
// Example output:
//
//	"foo" not found in agent or swarm registry; this orchestrator declares delegation.scope: permissive (no scope restriction).
//
// The wrapped sentinel (`errAgentNotInAllowlist`) is preserved so
// `errors.Is(err, errAgentNotInAllowlist)` checks at the call sites
// continue to work — the rejection IS an allowlist-style decision,
// just one made against the full registry rather than a scoped roster.
//
// Expected:
//   - swarmCtx may be nil (standalone permissive delegation).
//   - targetID is the rejected agent or swarm id; non-empty.
//
// Returns:
//   - A multi-line string ready to slot in after the wrapped sentinel.
//
// Side effects:
//   - None.
func (d *DelegateTool) formatPermissiveRejection(swarmCtx *swarm.Context, targetID string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%q not found in agent or swarm registry", targetID)
	if swarmCtx != nil && swarmCtx.SwarmID != "" {
		fmt.Fprintf(&b, " (active swarm %q)", swarmCtx.SwarmID)
	}
	b.WriteString("; this orchestrator declares delegation.scope: permissive (no scope restriction). Check the target id spelling — the rejection is a registry miss, not a roster gate.")
	return b.String()
}

// rejectionHint produces a single-line nudge to help the model self-
// correct when its delegate target was rejected. Three cases:
//
//  1. Inside a swarm, the rejected target is a member of some OTHER
//     known swarm — return `Hint: '<target>' is a member of swarm
//     '<sub>'. Delegate to '<sub>' instead.`. This is the meta-
//     coordinator failure mode: the rejected leaf agents were members
//     of sub-swarms already listed in the parent's roster.
//
//  2. The rejected target is a known agent but not in any other
//     swarm — return `Hint: '<target>' exists as an agent but isn't
//     in this swarm's roster.`. Tells the model the id is valid but
//     out-of-scope rather than misspelled.
//
//  3. Unknown to both registries — return the empty string so the
//     bare rejection stays the right shape for a typo.
//
// The swarm-membership check skips the *active* swarm itself (a
// target rejected against its own swarm is by construction not a
// member, so suggesting its own swarm would be useless noise) and
// short-circuits on the first match — multi-swarm membership picks
// the first id seen via Registry.List (which is sorted, so the
// behaviour is deterministic across processes).
//
// Expected:
//   - swarmCtx may be nil (standalone branch).
//   - targetID is the rejected agent id; non-empty.
//
// Returns:
//   - A non-empty hint line when (1) or (2) match; empty string
//     otherwise.
//
// Side effects:
//   - None (read-only access via Registry.List and Registry.Get /
//     GetByNameOrAlias).
func (d *DelegateTool) rejectionHint(swarmCtx *swarm.Context, targetID string) string {
	// Case 1: target is a member of another known swarm.
	if d.swarmRegistry != nil {
		var activeSwarmID string
		if swarmCtx != nil {
			activeSwarmID = swarmCtx.SwarmID
		}
		for _, m := range d.swarmRegistry.List() {
			if m == nil {
				continue
			}
			if activeSwarmID != "" && strings.EqualFold(m.ID, activeSwarmID) {
				continue
			}
			if containsAgent(m.Members, targetID) {
				return fmt.Sprintf("Hint: %q is a member of swarm %q. Delegate to %q instead.",
					targetID, m.ID, m.ID)
			}
		}
	}

	// Case 2: target is a known agent (but not in any other swarm).
	if d.registry != nil {
		if _, ok := d.registry.Get(targetID); ok {
			return fmt.Sprintf("Hint: %q exists as an agent but isn't in this swarm's roster.", targetID)
		}
		if _, ok := d.registry.GetByNameOrAlias(targetID); ok {
			return fmt.Sprintf("Hint: %q exists as an agent but isn't in this swarm's roster.", targetID)
		}
	}

	// Case 3: unknown — no hint.
	return ""
}
