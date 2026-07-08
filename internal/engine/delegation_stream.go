package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/turn"
)

// teeToParentStream forwards each in-band content chunk from a delegated
// member's stream to the parent's outChan live, in source order. The src
// channel is also returned (drained one-for-one) so downstream collectors
// (accumulator, harness-events, response builder) can run their own
// pipelines unchanged.
//
// Pre-2026-05 the tee buffered the entire child stream into a
// strings.Builder and emitted exactly one consolidated `**[<agentID>]**`
// chunk after the source closed. That choice produced two production
// failures: (1) a 322-second silent SSE wire during a delegation in
// session 05ece3e7 — the parent UI saw nothing for the entire child run;
// (2) a non-blocking send on the trailing emission silently dropped the
// whole buffered output when the parent channel was momentarily full at
// the close instant. Drop #3 of the streaming signal-drop fix removes
// both: each chunk forwards live, with a context-aware bounded send so
// backpressure surfaces as deadline rather than silent loss.
//
// Skipped chunks:
//   - Done: would prematurely close the parent stream.
//   - DelegationInfo-only: lifecycle events emitted separately by
//     executeSync via the delegation event bus.
//   - streaming.IsControlEvent EventTypes (harness_attempt_start, etc.):
//     out-of-band metadata destined for the status-line consumer; pre-fix
//     these leaked structured JSON like `{"attempt":1,"maxRetries":1}` into
//     the assistant turn (session 2d8dc0ac messages 169/180/185/190).
//
// Attribution rationale: per-chunk attribution to the child agent is
// handled by the existing delegation event bus (`delegation.{started,
// completed,failed}` topics — see [[Delegation Bus Bridge — Engine to SSE
// (May 2026)]]) and by the chunk-side `DelegationInfo` populated at the
// `*Delegating to <agent>…*` boundary marker. The chunks themselves do
// not need additional attribution. Interleaved output when multiple
// members run in parallel is a rendering concern for the UI to solve via
// per-agent stream lanes — not by stalling the wire.
func teeToParentStream(ctx context.Context, agentID string, src <-chan provider.StreamChunk) <-chan provider.StreamChunk {
	parentOut, ok := streamOutputFromContext(ctx)
	if !ok {
		return src
	}
	_ = agentID // retained for log/diagnostic attribution; chunks use the bus for source-agent identity
	out := make(chan provider.StreamChunk, cap(src)+1)
	go func() {
		defer close(out)
		for chunk := range src {
			// Forward the chunk down the internal pipeline first (the
			// downstream collector / accumulator depends on every chunk
			// flowing through). This must always happen, even if we
			// later choose not to mirror the chunk to the parent.
			//
			// ctx-aware: if the downstream collector has stopped
			// draining `out` (delegation turn cancel cascade, SSE
			// consumer disconnect, parent ctx done), the bare `out <-`
			// would park for the full life of src — which in
			// production stays alive until the per-attempt stream
			// timeout fires. Mirrors the M2 fix on
			// internal/plugin/failover/stream_hook.go's prepend*
			// wrappers (commit 38fc705f).
			select {
			case out <- chunk:
			case <-ctx.Done():
				return
			}

			// Decide whether this chunk should also flow to the parent
			// stream (the user-visible SSE wire). The skip rules are
			// the same set the buffered version applied — preserved
			// verbatim so the chat_ui_leak_test contracts (Leak A
			// filtering) remain green.
			if chunk.Done || chunk.DelegationInfo != nil {
				continue
			}
			if streaming.IsControlEvent(chunk.EventType) {
				continue
			}
			if chunk.Content == "" && chunk.Thinking == "" {
				// Tool-call chunks, error chunks, ProgressEvents, etc.
				// already flow through the engine's own paths to the
				// parent (the SSE handler dispatches ToolCall, Error,
				// and Event independently). Mirroring an empty chunk
				// to the parent achieves nothing.
				continue
			}

			// Bounded send: a context-aware deadline replaces the old
			// silent-drop `default:` branch. If the parent channel
			// stays full for the full deadline the chunk is dropped,
			// but the drop is logged via the broker's metrics path
			// (Drop #4) once it lands. Until that observability is in
			// place this branch falls back to a context-checked send
			// so a stalled consumer eventually surfaces as a cancelled
			// context rather than an unbounded block.
			select {
			case parentOut <- chunk:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// wrapWithAccumulator wraps the raw chunk stream through session.AccumulateStream
// when a messageAppender is configured, storing accumulated messages into the
// child session identified by sessionID.
//
// Expected:
//   - ctx bounds the accumulator goroutine so a cancelled delegation turn
//     drops promptly instead of parking on rawCh. Passed through from the
//     delegation call site's own context.
//   - rawCh is the stream from target.engine.Stream.
//   - sessionID is the child session identifier returned by createChildSession.
//   - agentID identifies the delegated agent.
//
// Returns:
//   - The (possibly wrapped) chunk channel.
//
// Side effects:
//   - When messageAppender is set, spawns a goroutine that calls AppendMessage.
func (d *DelegateTool) wrapWithAccumulator(
	ctx context.Context,
	rawCh <-chan provider.StreamChunk,
	sessionID, agentID string,
) <-chan provider.StreamChunk {
	if d.messageAppender == nil {
		return rawCh
	}
	return session.AccumulateStream(ctx, d.messageAppender, sessionID, agentID, rawCh)
}

// persistChildBrief writes the parent's delegation brief into the child
// session as a user-role message stamped with the delegated agent's ID.
//
// This restores symmetry between what the parent sent (target.message into
// engine.Stream) and what the child session persists. Without it the child
// session's message log opens with an assistant turn responding to a
// brief that has no recorded predecessor — making the session unreplayable
// in isolation and erasing the parent's stated intent from the audit trail.
//
// Expected:
//   - sessionID identifies the child session created or resolved for the
//     delegation.
//   - agentID identifies the delegated agent (matches the AgentID stamp on
//     subsequent assistant messages in the same session).
//   - message is the brief the parent passed to the delegate tool's
//     "message" argument.
//
// Side effects:
//   - When messageAppender is configured and message is non-empty, appends a
//     single user-role Message to the child session via AppendMessage.
//   - No-op when messageAppender is nil, when sessionID is empty, or when
//     message is empty (defensive — empty briefs would be a separate bug).
func (d *DelegateTool) persistChildBrief(sessionID, agentID, message string) {
	if d.messageAppender == nil || sessionID == "" || message == "" {
		return
	}
	d.messageAppender.AppendMessage(sessionID, session.Message{
		Role:    "user",
		Content: message,
		AgentID: agentID,
	})
}

// withHarnessEvents wires harness lifecycle events into outChan for harness-enabled targets.
// When an explicit Streamer is registered for the target, it tees EventType chunks from src
// to outChan (the registered Streamer emits its own harness events already).
// When no explicit Streamer is registered and the target has HarnessEnabled, it injects a
// synthetic harness_attempt_start event before forwarding chunks.
// When HarnessEnabled is false or hasOutput is false it returns src unchanged.
//
// Expected:
//   - src is the stream from resolveStreamer.
//   - outChan and hasOutput control event delivery to the parent stream.
//   - target provides the manifest HarnessEnabled flag and agentID.
//
// Returns:
//   - A channel carrying all chunks from src for use by wrapWithAccumulator.
//
// Side effects:
//   - Spawns a goroutine that closes the returned channel on completion.
func (d *DelegateTool) withHarnessEvents(
	ctx context.Context,
	target delegationTarget,
	src <-chan provider.StreamChunk,
	outChan chan<- provider.StreamChunk,
	hasOutput bool,
) <-chan provider.StreamChunk {
	if !hasOutput || !target.engine.Manifest().HarnessEnabled {
		return src
	}
	out := make(chan provider.StreamChunk, 64)
	go d.runHarnessEventLoop(ctx, target.agentID, src, outChan, out)
	return out
}

// runHarnessEventLoop is the goroutine body for withHarnessEvents.
// It emits a harness_attempt_start chunk when no explicit Streamer is registered,
// then forwards all chunks from src to out whilst tee-ing EventType chunks to outChan.
//
// Expected:
//   - agentID is used to look up any registered explicit Streamer.
//   - src, outChan, and out are non-nil, live channels.
//
// Returns:
//   - Nothing; closes out on completion.
//
// Side effects:
//   - Closes out.
func (d *DelegateTool) runHarnessEventLoop(
	ctx context.Context,
	agentID string,
	src <-chan provider.StreamChunk,
	outChan chan<- provider.StreamChunk,
	out chan<- provider.StreamChunk,
) {
	defer close(out)
	explicit := d.streamers != nil && d.streamers[agentID] != nil
	if !explicit {
		select {
		case outChan <- provider.StreamChunk{EventType: "harness_attempt_start"}:
		case <-ctx.Done():
			return
		}
	}
	for chunk := range src {
		if explicit && chunk.EventType != "" {
			select {
			case outChan <- chunk:
			case <-ctx.Done():
				return
			}
		}
		select {
		case out <- chunk:
		case <-ctx.Done():
			return
		}
	}
}

// formatDelegationOutput wraps the delegated agent's aggregated
// response in a `<task_result>` block. The block is the LLM-visible
// signal that this content is the sub-agent's response (not a
// continuation of the lead's own thinking) so callers downstream of
// the model — log filters, transcript renderers — can scan for the
// boundary tags without false positives.
//
// Notably absent: the historical "task_id: <sessionID> (for resuming
// to continue this task if needed)" header. That header was
// LLM-misleading: synchronous delegations returned a *child session*
// id under the same `task_id:` label that asynchronous delegations
// used for *background-task* ids. The lead would see the header,
// reflexively call `background_output` against the value, and get
// "task not found" because the sync session id was never registered
// with the BackgroundTaskManager. The session id is still surfaced
// to engine consumers via the tool result's Metadata["sessionId"]
// field — the model just never sees it.
//
// Expected:
//   - text is the aggregated response from the delegated agent.
//
// Returns:
//   - A formatted string with the response wrapped in a task_result
//     block.
//
// Side effects:
//   - None.
func formatDelegationOutput(text string) string {
	return fmt.Sprintf("<task_result>\n%s\n</task_result>", text)
}

// FormatDelegationOutput is the exported form of formatDelegationOutput for testing.
//
// Expected:
//   - text is the aggregated response from the delegated agent.
//
// Returns:
//   - A formatted string with the response wrapped in a task_result block.
//
// Side effects:
//   - None.
func FormatDelegationOutput(text string) string {
	return formatDelegationOutput(text)
}

// unwrapTaskResult returns the inner text of a delegation `<task_result>`
// block, or s unchanged when the wrapper is absent. The wrapper is the
// LLM-visible boundary marker emitted by formatDelegationOutput; chat-UI
// renderers and the persisted message store should never see it because the
// structural metadata (Role=tool, ToolName=delegate) already tells the
// reader that this is a sub-agent response. Persistence-side stripping
// keeps tool.Result.Output wrapped (LLM-bound on the same turn via
// appendToolResultsBatchToMessages) while session.Messages content stays
// clean — fixing the leak captured in session 2d8dc0ac messages
// 167/178/183/188 (May 2026 chat-UI leak triage).
//
// Expected:
//   - s is the raw tool result content; may or may not be wrapped.
//
// Returns:
//   - The unwrapped text when s exactly matches "<task_result>\n...\n</task_result>".
//   - s unchanged otherwise (never partial-strips).
//
// Side effects:
//   - None.
func unwrapTaskResult(s string) string {
	const open = "<task_result>\n"
	const close = "\n</task_result>"
	if !strings.HasPrefix(s, open) || !strings.HasSuffix(s, close) {
		return s
	}
	return s[len(open) : len(s)-len(close)]
}

// UnwrapTaskResult is the exported form of unwrapTaskResult for callers
// outside this package (engine.storeToolResult, engine tool-result emission).
//
// Expected:
//   - s is the raw tool result content; may or may not be wrapped.
//
// Returns:
//   - The unwrapped text when s carries the canonical task_result wrapper.
//   - s unchanged otherwise.
//
// Side effects:
//   - None.
func UnwrapTaskResult(s string) string {
	return unwrapTaskResult(s)
}

// executeSync runs delegation synchronously, blocking until complete.
//
// Expected:
//   - target is the resolved delegation target.
//   - baseInfo contains delegation metadata.
//   - outChan and hasOutput for streaming events.
//
// Returns:
//   - A tool.Result with the delegation result.
//   - An error if delegation fails.
//
// Side effects:
//   - Emits delegation events.
func (d *DelegateTool) executeSync(
	ctx context.Context,
	target delegationTarget,
	baseInfo provider.DelegationInfo,
	outChan chan<- provider.StreamChunk,
	hasOutput bool,
) (tool.Result, error) {
	if hasOutput {
		label := "\n*Delegating to **" + target.agentID + "**…*\n"
		select {
		case outChan <- provider.StreamChunk{Content: label}:
		default:
		}
	}

	parentSessionID := sessionIDFromContext(ctx)

	if gateErr := d.dispatchPreSwarmGatesOnce(ctx); gateErr != nil {
		d.emitDelegationEvent(outChan, hasOutput, baseInfo, "failed")
		// Pre-resolve failure: ChildSessionID is unknown at this seam,
		// so the bus event carries an empty child id. Subscribers that
		// require a child id (the SSE click-through path) treat empty
		// as "no navigable target" and fall back to surfacing the
		// failure on the parent timeline.
		d.publishDelegationEvent("failed", buildDelegationEventData(baseInfo, parentSessionID, "", gateErr.Error(), target.loadSkills))
		return tool.Result{}, gateErr
	}
	if gateErr := d.dispatchPreMemberGates(ctx, target.agentID, baseInfo.ChainID); gateErr != nil {
		d.emitDelegationEvent(outChan, hasOutput, baseInfo, "failed")
		d.publishDelegationEvent("failed", buildDelegationEventData(baseInfo, parentSessionID, "", gateErr.Error(), target.loadSkills))
		return tool.Result{}, gateErr
	}

	// Forward the authoritative chainID from the DelegationInfo so the
	// spawned child session is stamped with the same chain identifier that
	// downstream SwarmEvents / DelegationInfo wire events carry. This lets
	// the Vue chatStore rebuild its (chainId → childSessionId) map from
	// the persisted session list after a hard reload (closes the cold-
	// reload hole left by a488b858).
	delegateSessionID := d.resolveOrCreateSession(ctx, target.agentID, target.requestedSession, baseInfo.ChainID)
	// Persist the parent's brief as a user-role message on the child
	// session before the stream begins, so the child's session record is
	// replayable in isolation. Without this the child session contains
	// only assistant / tool_call / tool_result messages and the brief
	// (target.message) is lost — see Bug Fixes/Delegation Brief
	// Persistence (May 2026) for the original symptom.
	d.persistChildBrief(delegateSessionID, target.agentID, target.message)
	// Chunk-side started event fires post-resolve so the stream
	// chunk carries the populated TargetSessionID for the accumulator
	// to stamp on the persisted delegation_started message.
	baseInfo.TargetSessionID = delegateSessionID
	d.emitDelegationEvent(outChan, hasOutput, baseInfo, "started")
	// Bus-side `delegation.started` also fires post-resolve with the
	// populated ChildSessionID. Both chunk and bus now carry the id.
	d.publishDelegationEvent("started", buildDelegationEventData(baseInfo, parentSessionID, delegateSessionID, "", target.loadSkills))

	// Start the progress heartbeat — emits delegation.progress events
	// every 30s while the child runs. Cancelled when executeSync returns.
	progressCtx, progressCancel := context.WithCancel(ctx)
	defer progressCancel()
	go d.emitProgressHeartbeat(progressCtx, baseInfo, parentSessionID, delegateSessionID, target.loadSkills)

	closeStore := d.attachSessionStore(target.engine, delegateSessionID)
	defer closeStore()

	// Plans/Child Session Turn Registry Plumbing (May 2026) §Item 2b —
	// mint a child Turn keyed on delegateSessionID immediately after
	// publishDelegationEvent("started", ...). StartOrReuse (NOT Start)
	// is the right primitive here per D1: resolveOrCreateSession may
	// return an existing child session whose prior Turn is stale, and
	// StartOrReuse auto-completes the stale entry before minting fresh.
	// A nil registry (legacy test constructors) short-circuits to the
	// historical "no live channel for children" behaviour.
	var childTurnID string
	if d.turnRegistry != nil {
		if id, turnErr := d.turnRegistry.StartOrReuse(delegateSessionID); turnErr == nil {
			childTurnID = id
		}
		// StartOrReuse's err surface is empty in practice (auto-complete
		// cannot fail; mint cannot fail without OOM). On the soft-fail
		// path childTurnID stays "" and every downstream Turn lifecycle
		// site below short-circuits — delegation still completes, only
		// the live channel stays dark, matching pre-fix behaviour.
	}
	// turnOwnedByWrap is set true the moment executeSync calls Complete
	// on the happy path below. failChildTurnIfOwned reads it FIRST and
	// short-circuits before invoking turnRegistry.Fail. This is the
	// single-source-of-correctness defence per §Item 2c B5 resolution:
	// the post-Complete dispatchPostMemberGates failure path therefore
	// NEVER invokes Fail on a terminal turn under correct discipline.
	// The Fail-side ErrTurnTerminal silent-swallow at turn.go:796-797
	// is demoted to backstop status, NOT a substitute for caller-side
	// discipline.
	turnOwnedByWrap := false
	failChildTurnIfOwned := func(cause error) {
		if turnOwnedByWrap || d.turnRegistry == nil || childTurnID == "" {
			return
		}
		_ = d.turnRegistry.Fail(childTurnID, cause)
	}

	delegateCtx := context.WithValue(ctx, session.IDKey{}, delegateSessionID)
	delegateCtx = swarm.WithScope(delegateCtx, nil)
	delegateCtx = session.WithPriorMessages(delegateCtx, nil)
	// Cascade contract for child sessions: UI > manifest > global.
	//
	// The parent session's override (UI tier) must NOT propagate into the
	// child — typing in the parent's model picker should not silently
	// rewire every delegate. Resolve the child's own manifest tier here
	// instead: look up target.agentID in the agent registry and stamp the
	// first PreferredModels entry as the child's override. When the
	// child has no manifest preference (or the registry is unwired in a
	// legacy test surface), fall through with empty overrides so the
	// engine uses its configured failover preferences (global tier).
	//
	// Without this, every delegate child ran on the engine's
	// LastProvider/LastModel — the global default — even when the child
	// agent declared a different preferred_models in its manifest. This
	// reproduced as planner sessions correctly running on
	// anthropic/claude-sonnet-4-7 but delegated librarian/explorer
	// children silently dropping to zai/glm-4.6, which then emitted
	// malformed delegate tool args.
	delegateProv, delegateModel := d.resolveChildModelOverride(target)
	delegateCtx = context.WithValue(delegateCtx, session.ProviderOverrideKey{}, delegateProv)
	delegateCtx = context.WithValue(delegateCtx, session.ModelOverrideKey{}, delegateModel)
	// Thread the child's FULL preferred_models chain so the failover
	// layer exhausts the agent's own tiers (tier-1, tier-2, …) before
	// cascading to the global config default. Without this, a tier-0
	// failure drops straight to the global default and the child's
	// reliable secondary models are never tried. No-op when the agent
	// declares no chain.
	delegateCtx = session.WithPreferredModels(delegateCtx, d.resolveChildModelChain(target))
	// Inject the child Turn ctx triad — mirrors dispatcher.go:639-643 at
	// the parent-session layer. The accumulator's turnAwareAppender
	// reads the recorder closure off ctx and fans every persisted
	// child-session message (assistant, thinking, tool_call,
	// tool_result, delegation_started, delegation) onto the registry's
	// MessagesAdded slice, which the API server projects to the
	// frontend via FindActiveBySession / handleListV1Sessions.
	if d.turnRegistry != nil && childTurnID != "" {
		delegateCtx = turn.WithTurnID(delegateCtx, childTurnID)
		delegateCtx = session.WithAccumulatorTurnID(delegateCtx, childTurnID)
		delegateCtx = session.WithTurnRecorder(delegateCtx, func(id string, msg session.Message) {
			_ = d.turnRegistry.Append(id, msg)
		})
	}
	// HarnessConfig.MemberTimeout caps the delegate-await loop so a
	// stalled child cannot hang the parent forever. Zero (the default)
	// preserves the historical no-deadline contract; a positive value
	// wraps the per-call ctx so DeadlineExceeded flows back through the
	// existing dispatch-failure branch below (and through
	// dispatchParallel's first-error cancel cascade in the swarm path).
	// Symptom: session 3255e2ee — coordinator hung indefinitely on a
	// silent executor child.
	if memberTimeout := d.activeMemberTimeout(); memberTimeout > 0 {
		var cancelMemberTimeout context.CancelFunc
		delegateCtx, cancelMemberTimeout = context.WithTimeout(delegateCtx, memberTimeout)
		defer cancelMemberTimeout()
	}

	// Post-member gate retry loop. A post-member result-schema gate that
	// fails because the member narrated-but-did-not-write (the synthesis-
	// hang signature) is NO LONGER terminal on the first miss. Instead we
	// re-dispatch the SAME member on the SAME chain — appending the gate's
	// directive (which already carries "perform the coordination_store
	// write, do not narrate it") to the member's prompt — up to
	// PostMemberGateMaxAttempts times. This unifies the member-gate path
	// with the wave-fan-in harness, which already re-prompts with a
	// directive (internal/plan/harness/waves.go buildWaveFeedback,
	// internal/app/harness_adapter.go waveRetryFloor). Only after the
	// budget is exhausted does the GateError become terminal — the
	// honest-fail outcome is preserved, just deferred past the retries.
	//
	// A stream-dispatch error (Stream init / collect failure) is NOT a
	// gate miss and is NEVER retried here: it returns terminally inside
	// the loop, matching the pre-fix dispatch-failure semantics.
	// completeChildTurn marks the child Turn Completed and flips
	// turnOwnedByWrap. A cleanly-drained stream always Completes the Turn —
	// even when the post-member gate subsequently rejects the output —
	// because the stream itself succeeded; a gate rejection is NOT a stream
	// failure. The turnOwnedByWrap=true flip is the load-bearing B5/S4.2
	// defence that short-circuits failChildTurnIfOwned so turnRegistry.Fail
	// is NEVER invoked on a Turn whose stream drained cleanly.
	completeChildTurn := func(providerName, modelName string) {
		if d.turnRegistry != nil && childTurnID != "" {
			_ = d.turnRegistry.Complete(childTurnID, turn.ModelInfo{
				Provider: providerName,
				Model:    modelName,
			})
			turnOwnedByWrap = true
		}
	}

	// Post-member gate retry loop. A post-member result-schema gate that
	// fails because the member narrated-but-did-not-write (the synthesis-
	// hang signature) is NO LONGER terminal on the first miss. Instead we
	// re-dispatch the SAME member on the SAME chain — appending the gate's
	// directive (which already carries "perform the coordination_store
	// write, do not narrate it") to the member's prompt — up to
	// PostMemberGateMaxAttempts times. This unifies the member-gate path
	// with the wave-fan-in harness, which already re-prompts with a
	// directive (internal/plan/harness/waves.go buildWaveFeedback,
	// internal/app/harness_adapter.go waveRetryFloor). Only after the
	// budget is exhausted does the GateError become terminal — the
	// honest-fail outcome is preserved, just deferred past the retries.
	//
	// A stream-dispatch error (Stream init / collect failure) is NOT a
	// gate miss and is NEVER retried here: it returns terminally inside
	// the loop, matching the pre-fix dispatch-failure semantics.
	var result delegationResult
	var modelName, providerName string
	var completedAt time.Time
	// forcedToolChoice carries the per-attempt tool_choice override the
	// PREVIOUS iteration's gate failure decided to force on re-delegation.
	// Empty on the first attempt so multi-step members (explorer/librarian)
	// can read/search before writing; set to "tool:coordination_store" on a
	// narration-without-write retry so a marginal model is FORCED to emit
	// the write the gate's directive only asked for in prose.
	var forcedToolChoice string
	for attempt := 1; ; attempt++ {
		if attempt > 1 && d.turnRegistry != nil && childTurnID != "" {
			// Re-dispatch attempt: clear the prior attempt's partial
			// messages so the next stream does not pile on stale rows.
			// ErrTurnTerminal (a concurrent timeout completed the Turn)
			// falls through — the re-dispatch still runs and the final
			// completeChildTurn / failChildTurnIfOwned guards short-circuit.
			_ = d.turnRegistry.ResetForRetry(childTurnID)
		}
		// Apply the corrective forced tool_choice for THIS attempt only.
		// The engine reads it off ctx and stamps it onto the outbound
		// ChatRequest.ToolChoice; per-turn so the first attempt and any
		// later attempts that didn't decide to force stay unconstrained.
		attemptCtx := session.WithToolChoiceOverride(delegateCtx, forcedToolChoice)
		// Pair the forced tool_choice with a capable-model override on the
		// SAME corrective retry. Forcing tool_choice made the marginal model
		// (zai/glm-4.5) EMIT the coordination_store write — but glm-4.5 cannot
		// reliably emit a single clean JSON object for a result-schema bundle
		// (it appends a second object / trailing junk, surfacing downstream as
		// `invalid character ',' after top-level value`). So the retry also
		// ESCALATES the struggling member onto its OWN preferred_models chain
		// head (the most-capable tier the member declares) — not the lead's
		// current model, which is itself glm when anthropic + openai are down
		// and would re-route glm → glm (a no-op). The engine's failover then
		// cascades down the member's chain to a reachable tier if the head is
		// unreachable, so "only glm reachable" is unchanged from today. With no
		// member chain, correctiveRetryModel falls back to the lead's resolved
		// pair (prior behaviour). This only fires on the forced-tool corrective
		// retry, never on attempt 1 (forcedToolChoice is empty there), so
		// multi-step members keep their own manifest-resolved model on the
		// first pass. Empty values are a no-op: the child keeps the
		// manifest-tier override resolveChildModelOverride already stamped.
		if forcedToolChoice != "" {
			if prov, model := d.correctiveRetryModel(target); prov != "" || model != "" {
				if prov != "" {
					attemptCtx = context.WithValue(attemptCtx, session.ProviderOverrideKey{}, prov)
				}
				if model != "" {
					attemptCtx = context.WithValue(attemptCtx, session.ModelOverrideKey{}, model)
				}
			}
		}
		result = delegationResult{}
		dispatchErr := d.runStreamThroughRunner(attemptCtx, target, &result, childTurnID)
		if dispatchErr != nil {
			completedAt = time.Now().UTC()
			baseInfo.ToolCalls = result.toolCalls
			baseInfo.LastTool = result.lastTool
			baseInfo.CompletedAt = &completedAt
			d.emitDelegationEvent(outChan, hasOutput, baseInfo, "failed")
			d.publishDelegationEvent("failed", buildDelegationEventData(baseInfo, parentSessionID, delegateSessionID, dispatchErr.Error(), target.loadSkills))
			// Fail the child Turn BEFORE closeSessionIfManaged so the
			// byActiveSession entry clears before the session itself is
			// torn down — mirrors dispatcher.go:929-936 terminal-then-
			// cleanup ordering at the parent-session layer. The guard
			// (turnOwnedByWrap, registry nil, childTurnID empty) is the
			// single source of correctness; ErrTurnTerminal silent-swallow
			// inside Fail is the backstop.
			failChildTurnIfOwned(dispatchErr)
			// Record the actually-used (provider, model) on the child session
			// BEFORE sealing. A stream-dispatch failure (the synthesis-hang
			// signature) never flushes an assistant message that carries
			// ModelName, so the appendSessionMessage promotion never fires and
			// the sidecar would otherwise seal with null model/provider —
			// leaving model attribution to the contended shared flowstate.log.
			// LastProvider/LastModel report the resolved pair the request was
			// dispatched to (post-override, post-failover) even on the failure
			// path. See recordChildModelAttribution.
			d.recordChildModelAttribution(delegateSessionID, target.engine.LastProvider(), target.engine.LastModel())
			// Bug fix (May 2026 — Session Seal Persistence Hole): the success
			// branch below seals the child session via closeSessionIfManaged
			// (line ~2016), but the dispatch-failure path returned without
			// sealing. The child stayed "active" both in memory and (with
			// sessionsDir wired) on disk, cluttering the UI and risking
			// replay collisions on reload. Mirror the success-branch seal.
			d.closeSessionIfManaged(delegateSessionID)
			return tool.Result{}, dispatchErr
		}

		modelName = target.engine.LastModel()
		providerName = target.engine.LastProvider()
		// FINAL-attempt salvage floor. The member already had its clean
		// attempt-1 write chance plus the forced-tool-choice corrective
		// retry above (which escalates a narrate-without-write miss into a
		// FORCED coordination_store call). If even that produced no key, but
		// the member's reply CARRIES the artefact (the gpt-4o signature:
		// narrate-the-plan-in-the-reply, skip the tool call), salvage the raw
		// reply into the gate's output_key so the post-member gate can
		// validate the content that already exists in this turn. Runs ONLY on
		// the final attempt — attempts 1..N-1 keep their write-or-retry
		// behaviour so a well-behaved model still writes via the tool. A
		// no-content reply leaves the gate failing closed (prose / render-
		// mirror reject empty), so salvage NEVER publishes a junk plan.
		if attempt == PostMemberGateMaxAttempts {
			d.salvageMemberOutputIfMissing(ctx, target.agentID, baseInfo.ChainID, result.response)
		}
		gateErr := d.dispatchPostMemberGates(ctx, target.agentID, baseInfo.ChainID)
		if gateErr == nil {
			if d.gateRunner == nil && !hasSubstantiveOutput([]byte(result.response)) {
				if attempt < PostMemberGateMaxAttempts {
					if prov, model := d.correctiveRetryModel(target); prov != "" || model != "" {
						if prov != "" {
							delegateCtx = context.WithValue(delegateCtx, session.ProviderOverrideKey{}, prov)
						}
						if model != "" {
							delegateCtx = context.WithValue(delegateCtx, session.ModelOverrideKey{}, model)
						}
					}
					target.message = appendPlainDirective(target.message)
					forcedToolChoice = ""
					continue
				}
				slog.Warn("delegate response empty or non-substantive after retries",
					"agent", target.agentID,
					"model", modelName,
					"provider", providerName,
					"tool_calls", result.toolCalls,
					"last_tool", result.lastTool,
				)
			}
			break
		}
		// The member produced no usable output. Re-delegate while budget
		// remains; only fail loudly once exhausted. Append the directive
		// so the retry tells the member to PERFORM the write, not narrate
		// it. The Turn stays Running (no completeChildTurn yet) and is
		// reset at the top of the next iteration.
		if attempt < PostMemberGateMaxAttempts {
			target.message = appendGateDirective(target.message, gateErr)
			// Escalate from a prose ask to a hard force: on a narration-
			// without-write miss the next attempt FORCES the required
			// coordination_store write via tool_choice. A marginal model
			// (zai/glm-4.5) that ignored the text directive across every
			// attempt in production cannot ignore a forced tool call. Empty
			// when the failure is not the narration signature, leaving the
			// retry unconstrained.
			forcedToolChoice = forcedToolChoiceForGate(gateErr)
			continue
		}
		// Budget exhausted — the GateError is now terminal. Honest-fail
		// preserved: the run dies loudly with the stage + reason, just
		// after the bounded retries. The stream drained cleanly on this
		// last attempt, so the Turn is Completed (NOT Failed) and the
		// turnOwnedByWrap guard keeps turnRegistry.Fail call-count at 0 —
		// the gate rejection is not a stream failure (B5/S4.2 contract).
		//
		// Publish gate.failed EXACTLY ONCE here, on budget exhaustion.
		// dispatchPostMemberGates evaluates silently per attempt (no
		// gate.failed publication) so a single logical halt does not emit
		// one event per retry — the surface (SSE bridge / TUI subscriber)
		// sees the one halt the run actually ends on. swarmCtx resolves to
		// the active context the gate ran against; nil only when the gate
		// runner is unwired, in which case gateErr would have been nil.
		if swarmCtx, ok := d.activeSwarmContextForCtx(ctx); ok {
			d.publishGateFailed(ctx, swarmCtx, swarm.LifecyclePostMember, target.agentID, gateErr)
		}
		completedAt = time.Now().UTC()
		baseInfo.ToolCalls = result.toolCalls
		baseInfo.LastTool = result.lastTool
		baseInfo.CompletedAt = &completedAt
		d.emitDelegationEvent(outChan, hasOutput, baseInfo, "failed")
		d.publishDelegationEvent("failed", buildDelegationEventData(baseInfo, parentSessionID, delegateSessionID, gateErr.Error(), target.loadSkills))
		completeChildTurn(providerName, modelName)
		failChildTurnIfOwned(gateErr)
		// Stamp the actually-used pair on the child session before sealing so
		// a narrated-but-not-written gate failure (the synthesis-hang the gate
		// retries guard against) still records which model ran. modelName /
		// providerName were captured from LastModel/LastProvider after the
		// last stream attempt above.
		d.recordChildModelAttribution(delegateSessionID, providerName, modelName)
		d.closeSessionIfManaged(delegateSessionID)
		return tool.Result{}, gateErr
	}

	// Accepted attempt: the post-member gate passed. Emit the terminal
	// success events and seal the child Turn / session exactly once.
	completedAt = time.Now().UTC()
	baseInfo.ModelName = modelName
	baseInfo.ProviderName = providerName
	baseInfo.ToolCalls = result.toolCalls
	baseInfo.LastTool = result.lastTool
	baseInfo.CompletedAt = &completedAt
	d.emitDelegationEvent(outChan, hasOutput, baseInfo, "completed")
	d.publishDelegationEvent("completed", buildDelegationEventData(baseInfo, parentSessionID, delegateSessionID, "", target.loadSkills))
	// Complete the child Turn BEFORE closeSessionIfManaged so the
	// byActiveSession entry clears in the terminal-then-cleanup order
	// (matches dispatcher.go:933-936).
	completeChildTurn(providerName, modelName)
	// The happy path already promotes (provider, model) onto the session via
	// the assistant-message-append path; stamping here is idempotent and
	// backstops the rare case where the flushed assistant message lacked a
	// ModelName but LastModel/LastProvider know the resolved pair.
	d.recordChildModelAttribution(delegateSessionID, providerName, modelName)
	d.closeSessionIfManaged(delegateSessionID)

	chainID := baseInfo.ChainID
	responseText := result.response
	if chainID != "" {
		responseText += fmt.Sprintf("\n\n---\n[coordination_chain] %s\nThe delegated agent may have written structured findings to the coordination store. Use the coordination_store tool to read key \"%s\" or scan for keys with prefix \"%s/\" before proceeding.", chainID, chainID, chainID)
	}
	return tool.Result{
		Output: formatDelegationOutput(responseText),
		Title:  target.message,
		Metadata: map[string]interface{}{
			"sessionId": delegateSessionID,
			"model":     modelName,
			"provider":  providerName,
			"chainId":   chainID,
		},
	}, nil
}

// runStreamThroughRunner dispatches the target's Stream + collect
// pipeline through the per-swarm-context Runner when one is in flight,
// or through the historical CircuitBreaker on the no-swarm path.
//
// On the swarm path the closure is the work the runner re-invokes per
// retry attempt: a fresh Stream call followed by collectWithProgress.
// CategorisedErrors flow through unchanged so the runner sees the
// retry/terminal verdict the source layer attached. Plain errors get
// CategoryUnknown which the runner treats as terminal — this is the
// addendum-A3 contract that uncategorised errors NEVER silently
// retry.
//
// On the no-swarm path the historical delegation.CircuitBreaker still
// records failures and successes; the swarm.Runner is OUT of the
// dispatch chain (per the multi-expert review's OQ.3 resolution).
//
// Expected:
//   - delegateCtx is the per-call context with the delegate session id.
//   - target is the resolved delegation target.
//   - result is an out-parameter the caller reads after success or
//     failure — toolCalls / lastTool flow through it for emit metadata.
//
// Returns:
//   - nil on success; a wrapped error otherwise.
//
// Side effects:
//   - Calls Stream on the resolved streamer and drains its chunks.
//   - Mutates the historical breaker on the no-swarm path only.
//   - Caches a Runner per active swarm id on first use.
func (d *DelegateTool) runStreamThroughRunner(delegateCtx context.Context, target delegationTarget, result *delegationResult, childTurnID string) error {
	swarmCtx, hasSwarm := d.activeSwarmContext()
	if !hasSwarm {
		return d.runStreamWithLegacyBreaker(delegateCtx, target, result)
	}

	runner := d.runnerForSwarm(swarmCtx.SwarmID, d.manifestForSwarm(swarmCtx.SwarmID))
	// Closure-internal attempt counter — Plans/Child Session Turn
	// Registry Plumbing (May 2026) §Item 2c retry-boundary mechanism
	// (option (i) per the PR1 disclosure). The runner re-invokes this
	// closure on every retry attempt; on attempts > 0 we wipe
	// childTurnID's MessagesAdded slice via ResetForRetry so
	// attempt-N+1's chunks do NOT pile on top of attempt-N's stale
	// partial-stream rows (provider-side message IDs are NOT generally
	// stable across retries, so the id-keyed upsert at Append cannot
	// collapse them — see D5 rationale).
	//
	// Empty childTurnID and nil registry short-circuit silently — the
	// legacy no-turn-registry path keeps the historical behaviour.
	attempt := 0
	dispatchErr := runner.Dispatch(delegateCtx, target.agentID, func(ctx context.Context, _ string) error {
		if attempt > 0 && d.turnRegistry != nil && childTurnID != "" {
			// ResetForRetry returns ErrTurnTerminal when the turn has
			// already reached Completed/Failed (e.g. a concurrent
			// timeout). Treat as "turn already ended, no retry
			// needed" and fall through — matches the docstring's
			// caller contract.
			_ = d.turnRegistry.ResetForRetry(childTurnID)
		}
		attempt++
		return d.streamAndCollect(ctx, target, result)
	})
	return dispatchErr
}

// runStreamWithLegacyBreaker preserves the historical no-swarm-context
// dispatch semantics: the legacy delegation.CircuitBreaker tracks
// process-wide failure counts and the error wrapper matches the
// pre-Task-1 surface so existing tests (and any caller that does not
// install a swarm context) behave identically.
//
// Expected:
//   - delegateCtx is the per-call context.
//   - target is the resolved delegation target.
//   - result is the out-parameter populated by collectWithProgress.
//
// Returns:
//   - nil on success.
//   - "delegation failed: %w" on Stream error; the underlying error
//     from collectWithProgress otherwise.
//
// Side effects:
//   - Calls RecordSuccess / RecordFailure on the historical breaker.
func (d *DelegateTool) runStreamWithLegacyBreaker(delegateCtx context.Context, target delegationTarget, result *delegationResult) error {
	if err := checkDelegationCandidates(target.engine); err != nil {
		d.circuitBreaker.RecordFailure()
		return fmt.Errorf("delegation failed: %w", err)
	}
	chunks, err := d.resolveStreamer(target.agentID, target.engine).Stream(delegateCtx, target.agentID, target.message)
	if err != nil {
		d.circuitBreaker.RecordFailure()
		return fmt.Errorf("delegation failed: %w", err)
	}
	if d.teeChildContent {
		chunks = teeToParentStream(delegateCtx, target.agentID, chunks)
	}
	chunks = d.withHarnessEvents(delegateCtx, target, chunks, nil, false)
	chunks = d.wrapWithAccumulator(delegateCtx, chunks, sessionIDFromContext(delegateCtx), target.agentID)
	res, collectErr := d.collectWithProgress(delegateCtx, chunks, time.Now())
	*result = res
	if collectErr != nil {
		d.circuitBreaker.RecordFailure()
		return collectErr
	}
	d.circuitBreaker.RecordSuccess()
	return nil
}

// streamAndCollect is the per-attempt closure body the swarm Runner
// re-invokes under retry. A streamer panic surfaces as a
// CategoryTerminal CategorisedError so the runner halts at attempt 1
// (P1.3); a Stream-error and a collectWithProgress-error pass through
// unchanged so any caller-categorisation reaches the runner intact.
//
// Expected:
//   - ctx is the runner's per-attempt context.
//   - target is the resolved delegation target.
//   - result is the out-parameter populated on success or partial
//     completion (so tool-call counts surface even on failure).
//
// Returns:
//   - nil when the stream drained cleanly.
//   - A categorised error wrapping the underlying cause otherwise.
//
// Side effects:
//   - Calls Stream on the resolved streamer and drains its chunks.
func (d *DelegateTool) streamAndCollect(ctx context.Context, target delegationTarget, result *delegationResult) (retErr error) {
	defer func() {
		if r := recover(); r != nil {
			retErr = &swarm.CategorisedError{
				Category: swarm.CategoryTerminal,
				MemberID: target.agentID,
				Cause:    fmt.Errorf("streamer panic: %v", r),
			}
		}
	}()

	if err := checkDelegationCandidates(target.engine); err != nil {
		return &swarm.CategorisedError{
			Category: swarm.CategoryTerminal,
			MemberID: target.agentID,
			Cause:    err,
		}
	}
	ctx = session.WithPriorMessages(ctx, nil)
	chunks, err := d.resolveStreamer(target.agentID, target.engine).Stream(ctx, target.agentID, target.message)
	if err != nil {
		return err
	}
	if d.teeChildContent {
		chunks = teeToParentStream(ctx, target.agentID, chunks)
	}
	chunks = d.withHarnessEvents(ctx, target, chunks, nil, false)
	chunks = d.wrapWithAccumulator(ctx, chunks, sessionIDFromContext(ctx), target.agentID)
	res, collectErr := d.collectWithProgress(ctx, chunks, time.Now())
	*result = res
	return collectErr
}

// executeAsync runs delegation asynchronously, returning immediately with a task ID.
//
// Expected:
//   - target is the resolved delegation target.
//   - baseInfo contains delegation metadata.
//   - outChan and hasOutput for streaming events.
//
// Returns:
//   - A tool.Result containing the task ID.
//   - An error if background mode is disabled or task launch fails.
//
// Side effects:
//   - Spawns a goroutine for the delegation.
//   - Emits delegation events for started status.
func (d *DelegateTool) executeAsync(
	ctx context.Context,
	target delegationTarget,
	baseInfo provider.DelegationInfo,
	outChan chan<- provider.StreamChunk,
	hasOutput bool,
) (tool.Result, error) {
	if d.backgroundManager == nil {
		return tool.Result{}, errBackgroundModeDisabled
	}

	parentSessionID := sessionIDFromContext(ctx)
	// Forward the authoritative chainID so the async-spawned child session
	// is stamped with the chain identifier carried on the DelegationInfo
	// event. Same rationale as the executeSync path — cold-reload
	// reconstruction needs chain_id on the persisted Session to rebuild
	// the (chainId → childSessionId) map.
	taskID := d.createChildSession(ctx, target.agentID, baseInfo.ChainID)
	if taskID == "" {
		taskID = fmt.Sprintf("task-%s-%d", target.agentID, time.Now().UTC().UnixNano())
	}

	baseInfo.TargetSessionID = taskID
	d.emitDelegationEvent(outChan, hasOutput, baseInfo, "started")
	// Bus-side `delegation.started` fires post-resolve so the payload
	// carries the populated ChildSessionID (== taskID for the async path).
	d.publishDelegationEvent("started", buildDelegationEventData(baseInfo, parentSessionID, taskID, "", target.loadSkills))

	bgProv, bgModel := d.resolveChildModelOverride(target)
	bgChain := d.resolveChildModelChain(target)
	d.backgroundManager.Launch(context.WithoutCancel(ctx), taskID, target.agentID, target.message, func(ctx context.Context) (string, error) {
		delegateCtx := context.WithValue(ctx, session.IDKey{}, taskID)
		delegateCtx = swarm.WithScope(delegateCtx, nil)
		// Same cascade contract as the synchronous delegate path — child
		// manifest's PreferredModels[0] wins; empty falls through to the
		// engine's global default. See resolveChildModelOverride.
		delegateCtx = context.WithValue(delegateCtx, session.ProviderOverrideKey{}, bgProv)
		delegateCtx = context.WithValue(delegateCtx, session.ModelOverrideKey{}, bgModel)
		// Thread the full chain so background-delegate failover walks the
		// agent's own tiers before the global default. See
		// resolveChildModelChain. No-op when the agent declares no chain.
		delegateCtx = session.WithPreferredModels(delegateCtx, bgChain)
		result, err := d.executeBackgroundTask(delegateCtx, target, baseInfo, parentSessionID, outChan, hasOutput)
		if err != nil {
			return "", err
		}
		return result, nil
	})

	return tool.Result{
		Output: fmt.Sprintf(`{"task_id": %q, "status": "running"}`, taskID),
		Title:  target.message,
		Metadata: map[string]interface{}{
			"sessionId": taskID,
		},
	}, nil
}

// executeBackgroundTask performs the actual delegation within a background goroutine.
//
// Expected:
//   - ctx is the task context with cancellation support; carries the
//     child task session id under session.IDKey{}.
//   - target is the resolved delegation target.
//   - baseInfo contains delegation metadata.
//   - parentSessionID is the parent session that issued the delegate
//     tool call; carried through from executeAsync because the async
//     ctx no longer carries the parent id.
//   - outChan and hasOutput for streaming events.
//
// Returns:
//   - The delegation result string on success.
//   - An error if delegation fails.
//
// Side effects:
//   - Emits delegation events for completed or failed status.
//   - Publishes `delegation.completed` / `delegation.failed` on the bus
//     when one is wired.
func (d *DelegateTool) executeBackgroundTask(
	ctx context.Context,
	target delegationTarget,
	baseInfo provider.DelegationInfo,
	parentSessionID string,
	outChan chan<- provider.StreamChunk,
	hasOutput bool,
) (string, error) {
	taskID := sessionIDFromContext(ctx)
	// Mirror the sync path: persist the brief on the child task session
	// before streaming so async delegations stay replayable too.
	d.persistChildBrief(taskID, target.agentID, target.message)
	closeStore := d.attachSessionStore(target.engine, taskID)

	ctx = session.WithPriorMessages(ctx, nil)
	chunks, err := d.resolveStreamer(target.agentID, target.engine).Stream(ctx, target.agentID, target.message)
	if err != nil {
		closeStore()
		d.circuitBreaker.RecordFailure()
		completedAt := time.Now().UTC()
		baseInfo.CompletedAt = &completedAt
		d.emitDelegationEvent(outChan, hasOutput, baseInfo, "failed")
		d.publishDelegationEvent("failed", buildDelegationEventData(baseInfo, parentSessionID, taskID, err.Error(), target.loadSkills))
		// Bug fix (May 2026 — Session Seal Persistence Hole): mirror the
		// sync-dispatcher fix on the async runner. The success branch
		// (line ~2790) seals the child via closeSessionIfManaged, but the
		// Stream-error and collect-error paths skipped it.
		d.closeSessionIfManaged(taskID)
		return "", fmt.Errorf("delegation failed: %w", err)
	}

	chunks = d.wrapWithAccumulator(ctx, chunks, taskID, target.agentID)

	result, err := d.collectDelegationResult(chunks)
	closeStore()
	if err != nil {
		d.circuitBreaker.RecordFailure()
		completedAt := time.Now().UTC()
		baseInfo.ToolCalls = result.toolCalls
		baseInfo.LastTool = result.lastTool
		baseInfo.CompletedAt = &completedAt
		d.emitDelegationEvent(outChan, hasOutput, baseInfo, "failed")
		d.publishDelegationEvent("failed", buildDelegationEventData(baseInfo, parentSessionID, taskID, err.Error(), target.loadSkills))
		// Bug fix (May 2026 — Session Seal Persistence Hole): see the
		// twin comment on the Stream-error path above. The async runner's
		// success branch is the only place that previously sealed.
		d.closeSessionIfManaged(taskID)
		return "", err
	}

	if result.truncated {
		slog.Warn("delegation result truncated", "bytes", len(result.response), "max", maxDelegationResultBytes)
	}

	d.circuitBreaker.RecordSuccess()
	completedAt := time.Now().UTC()
	baseInfo.ModelName = target.engine.LastModel()
	baseInfo.ProviderName = target.engine.LastProvider()
	baseInfo.ToolCalls = result.toolCalls
	baseInfo.LastTool = result.lastTool
	baseInfo.CompletedAt = &completedAt
	d.emitDelegationEvent(outChan, hasOutput, baseInfo, "completed")
	d.publishDelegationEvent("completed", buildDelegationEventData(baseInfo, parentSessionID, taskID, "", target.loadSkills))

	d.closeSessionIfManaged(taskID)

	return result.response, nil
}

// closeSessionIfManaged closes the named session via the session manager when one is configured,
// then runs a deliverable check that warns when a delegated session completes without having
// written any keys to the coordination_store.
//
// The deliverable check is purely observational: it logs a slog.Warn when a session carrying both
// a non-empty ParentID and ChainID (the signature of a delegated session with coordination
// expectations) leaves zero keys under its chain prefix in the coordination_store. This surfaces
// the synthesis-hang failure mode where an agent announces work it never performed — the session
// is still sealed as "completed", but the operator and downstream coordinator now have a signal.
//
// Expected:
//   - sessionID identifies the session to close.
//
// Side effects:
//   - Closes the session in the session manager if one is set.
//   - Suppresses ErrSessionNotFound; other errors are silently discarded.
//   - Emits a slog.Warn when a delegated session wrote zero coordination_store keys or when the
//     store check itself fails. The close ALWAYS proceeds regardless of the check outcome.
func (d *DelegateTool) closeSessionIfManaged(sessionID string) {
	if d.sessionManager == nil {
		return
	}
	if err := d.sessionManager.CloseSession(sessionID); err != nil && !errors.Is(err, session.ErrSessionNotFound) {
		_ = err
	}
	d.warnIfDelegatedSessionLeftNoCoordinationKeys(sessionID)
}

// warnIfDelegatedSessionLeftNoCoordinationKeys logs a warning when a delegated session (one with
// both a ParentID and a ChainID) completed without writing any keys to the coordination_store under
// its chain prefix. The check is observational only — it never blocks the session close and always
// runs after CloseSession so the seal is never delayed.
//
// Expected:
//   - sessionID identifies the just-closed session to inspect.
//
// Side effects:
//   - Emits slog.Warn entries; mutates no session or store state.
func (d *DelegateTool) warnIfDelegatedSessionLeftNoCoordinationKeys(sessionID string) {
	if d.coordinationStore == nil {
		return
	}
	sess, err := d.sessionManager.GetSession(sessionID)
	if err != nil {
		return
	}
	if sess.ParentID == "" || sess.ChainID == "" {
		return
	}
	keys, listErr := d.coordinationStore.List(sess.ChainID + "/")
	if listErr != nil {
		slog.Warn("delegated session coordination_store check failed",
			"session", sessionID,
			"agent_id", sess.AgentID,
			"chain_id", sess.ChainID,
			"parent_id", sess.ParentID,
			"error", listErr,
		)
		return
	}
	if len(keys) == 0 {
		slog.Warn("delegated session completed without writing any coordination_store keys — the agent may have announced work it never performed",
			"session", sessionID,
			"agent_id", sess.AgentID,
			"chain_id", sess.ChainID,
			"parent_id", sess.ParentID,
		)
	}
}

// recordChildModelAttribution stamps the actually-used (provider, model) pair
// onto the child session and persists the .meta.json sidecar so attribution is
// deterministic from the session record alone, never inferred from the
// contended shared flowstate.log.
//
// The happy path already promotes the pair onto the session when an assistant
// message flushes with a populated ModelName (manager.go appendSessionMessage).
// But a delegate that hangs or stream-errors before flushing such a message —
// the synthesis-hang signature — never reaches that path, so its sealed
// .meta.json records CurrentModelID / CurrentProviderID null. This helper
// closes that hole: it sources the pair from the engine's post-resolution
// LastProvider() / LastModel() (which reflect the override + failover outcome,
// i.e. what the request was actually sent to — not the configured default) and
// writes it on every seal site, including the failure / gate-exhausted paths.
//
// Empty values are tolerated and skipped (no manager, no resolved pair, or a
// legacy test surface without a session manager) so the call is a safe no-op
// when attribution cannot be determined.
//
// Expected:
//   - sessionID identifies the child session to stamp.
//   - providerID / modelID are the actually-used pair; empty values short-circuit.
//
// Side effects:
//   - Calls sessionManager.UpdateSessionModel, which persists the sidecar.
func (d *DelegateTool) recordChildModelAttribution(sessionID, providerID, modelID string) {
	if d.sessionManager == nil || sessionID == "" {
		return
	}
	if providerID == "" && modelID == "" {
		return
	}
	if err := d.sessionManager.UpdateSessionModel(sessionID, providerID, modelID); err != nil &&
		!errors.Is(err, session.ErrSessionNotFound) {
		_ = err
	}
}

// collectDelegationResult aggregates streamed chunks from the delegated agent.
//
// Expected:
//   - chunks is the stream returned by the target engine.
//
// Returns:
//   - The concatenated response text.
//   - The number of chunks observed.
//   - The most recent tool name seen in the stream.
//   - An error if the stream yields a chunk error.
//
// Side effects:
//   - Reads from the streamed chunk channel until it closes or returns an error.
func (d *DelegateTool) collectDelegationResult(chunks <-chan provider.StreamChunk) (delegationResult, error) {
	var response strings.Builder
	toolCalls := 0
	lastTool := ""
	var truncated bool
	for chunk := range chunks {
		toolCalls++
		if chunk.ToolCall != nil && chunk.ToolCall.Name != "" {
			lastTool = chunk.ToolCall.Name
		}
		if chunk.Error != nil {
			return delegationResult{}, fmt.Errorf("delegation stream error: %w", chunk.Error)
		}
		if streaming.IsControlEvent(chunk.EventType) {
			continue
		}
		if truncated {
			continue
		}
		if chunk.Content != "" {
			response.WriteString(chunk.Content)
		} else if chunk.ToolResult != nil && chunk.ToolResult.IsError {
			response.WriteString(chunk.ToolResult.Content)
		}
		if response.Len() > maxDelegationResultBytes {
			truncated = true
		}
	}
	if truncated {
		response.WriteString("[...truncated]")
	}

	return delegationResult{response: response.String(), toolCalls: toolCalls, lastTool: lastTool, truncated: truncated}, nil
}

// collectWithProgress aggregates delegation chunks and periodically emits ProgressEvents.
//
// Expected:
//   - ctx carries the parent output channel for progress delivery.
//   - chunks is the stream channel from the child engine.
//   - startedAt is the delegation start time.
//
// Returns:
//   - A delegationResult with accumulated response, tool call count, and last tool name.
//   - An error if any chunk carries a stream error.
//
// Side effects:
//   - Emits ProgressEvents every 5 tool calls or every 5 seconds via deliverProgressEvent.
func (d *DelegateTool) collectWithProgress(
	ctx context.Context,
	chunks <-chan provider.StreamChunk,
	startedAt time.Time,
) (delegationResult, error) {
	var response strings.Builder
	toolCalls := 0
	lastTool := ""
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	const progressInterval = 5

	for {
		select {
		case <-ctx.Done():
			return delegationResult{}, ctx.Err()
		case chunk, ok := <-chunks:
			if !ok {
				return delegationResult{response: response.String(), toolCalls: toolCalls, lastTool: lastTool}, nil
			}
			toolCalls++
			if chunk.ToolCall != nil && chunk.ToolCall.Name != "" {
				lastTool = chunk.ToolCall.Name
			}
			if chunk.Error != nil {
				return delegationResult{}, fmt.Errorf("delegation stream error: %w", chunk.Error)
			}
			// streaming.IsControlEvent gate — see collectDelegationResult.
			if streaming.IsControlEvent(chunk.EventType) {
				continue
			}
			if chunk.Content != "" {
				response.WriteString(chunk.Content)
			} else if chunk.ToolResult != nil && chunk.ToolResult.IsError {
				response.WriteString(chunk.ToolResult.Content)
			}
			if toolCalls%progressInterval == 0 {
				d.deliverProgressEvent(ctx, toolCalls, lastTool, startedAt)
			}
		case <-ticker.C:
			d.deliverProgressEvent(ctx, toolCalls, lastTool, startedAt)
		}
	}
}
