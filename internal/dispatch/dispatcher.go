// Package dispatch hosts the canonical "user-message → engine-stream"
// service mediating every FlowState entry point (the API's /api/chat,
// /messages, the WebSocket session handler, and the CLI run / chat
// commands). Per the "Dispatcher Service Unification (May 2026)" plan
// in the FlowState vault, the package replaces the parallel resolve-
// and-dispatch helpers that drifted across the two HTTP handlers and
// closes two latent surfaces (S1 WS r.Context() coupling, S2 swarm-
// lifecycle back-to-back POST race).
//
// The dispatcher exposes TWO methods returning TWO compile-time-distinct
// handle types so the sync/async dichotomy between session-anchored and
// ephemeral flows is enforced by the type system rather than convention:
//
//   - DispatchEphemeral — backs /api/chat. No session anchor. Returns
//     EphemeralHandle{Done <-chan error}; the handler MUST await Done
//     to know when to write the SSE finaliser.
//   - DispatchSessioned — backs /messages and (post-Phase 4) the WS
//     handler. Session anchor required. Returns SessionedHandle with
//     Snapshot only — no Done channel — so the handler cannot block
//     on stream completion, preserving the async-POST contract from
//     commit e4bf9632.
//
// Phase 1 ships DispatchEphemeral fully wired and leaves DispatchSessioned
// as a stub; Phase 2 migrates /messages.
package dispatch

import (
	"context"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
)

// DispatchRequest is the surface-agnostic input to a dispatch call. The
// SessionID field is populated for sessioned dispatch and empty for
// ephemeral; the type system does not enforce that asymmetry — the
// dichotomy is carried by the two return types (SessionedHandle vs
// EphemeralHandle) instead. ScanMentions enables left-to-right scan
// for the first @-mention that resolves to a swarm; agent @-mentions
// and unknown @-mentions fall through to AgentID, mirroring
// internal/orchestrator/orchestrator.go::resolve.
type DispatchRequest struct {
	// SessionID is the persistent session anchor for DispatchSessioned.
	// Empty for DispatchEphemeral.
	SessionID string
	// AgentID is the surface's baseline agent or swarm id. For
	// DispatchEphemeral this is the request body's agent_id; for
	// DispatchSessioned this is the session's persisted agent_id.
	AgentID string
	// Content is the user's raw input text.
	Content string
	// AttachmentIDs is the optional attachment list (Phase 2 only — the
	// ephemeral path on /api/chat does not surface attachments today).
	AttachmentIDs []string
	// ScanMentions enables @-mention scanning of Content. The first
	// @-mention that resolves to a swarm overrides AgentID for this
	// call only.
	ScanMentions bool
}

// SessionedHandle is what DispatchSessioned returns. Snapshot is
// returned synchronously after the user message is appended to the
// session; there is NO Done channel — the streamer runs in a
// Dispatcher-owned goroutine and the handler MUST NOT block on stream
// completion. Compile-time guarantee: a /messages handler holding this
// type cannot await the stream because there is no field to await on.
// Preserves the async-POST contract from commit e4bf9632.
type SessionedHandle struct {
	// Snapshot is the session as it stood AFTER appending the user
	// message and BEFORE the assistant turn streams.
	Snapshot session.Session
}

// EphemeralHandle is what DispatchEphemeral returns. There is no
// Snapshot because no session exists. The handler MUST await Done to
// know when the streamer has finished so the SSE finaliser ([DONE])
// can be written safely. Done emits one error value (nil on clean
// completion, non-nil on stream failure) and then closes.
type EphemeralHandle struct {
	// Done emits the terminal stream error (or nil) and then closes.
	Done <-chan error
}

// Streamer mirrors streaming.Streamer locally so test fakes that
// satisfy only this surface can be wired through Dispatcher without
// dragging the wider engine surface. Production wires *engine.Engine
// here via streaming.Streamer satisfaction.
type Streamer = streaming.Streamer

// SessionManager is the slice of internal/session that DispatchSessioned
// will need post-Phase-2. Phase 1 declares the interface so the
// Dispatcher's constructor signature is stable across phases, but
// DispatchSessioned does not consume it yet.
type SessionManager interface {
	SnapshotSession(id string) (session.Session, error)
}

// Dispatcher is the single owner of the "user input → engine stream"
// lifecycle. Wiring is constructor-injected; nil-tolerance is per-method
// because tests for the ephemeral path do not need the session manager
// and vice versa.
type Dispatcher struct {
	streamer       Streamer
	dispatchEngine swarm.DispatchEngine
	swarmRegistry  *swarm.Registry
	agentRegistry  *agent.Registry
	sessionManager SessionManager
}

// New wires a Dispatcher. All dependencies are nullable for test
// minimality; the entry methods enforce their own readiness:
//
//   - DispatchEphemeral requires streamer; swarmRegistry/agentRegistry/
//     dispatchEngine are nil-tolerant (a nil swarmRegistry short-circuits
//     to plain-agent streaming via streaming.Run, matching the API's
//     legacy fallback at internal/api/server.go::handleChat).
//   - DispatchSessioned (Phase 2+) requires sessionManager.
//
// Expected:
//   - streamer is the underlying producer. Typically *engine.Engine.
//   - dispatchEngine is the swarm-lifecycle surface; nil disables
//     SetSwarmContext / FlushSwarmLifecycle / RestoreManifest.
//   - swarmRegistry is the swarm lookup; nil disables swarm dispatch
//     and Dispatcher falls through to plain-agent streaming.
//   - agentRegistry is the agent lookup; nil propagates the bare-engine
//     pass-through contract through swarm.ResolveTarget (see
//     internal/swarm/resolve_target.go:42).
//   - sessionManager is the session anchor for DispatchSessioned; nil
//     until Phase 2.
//
// Returns:
//   - A configured *Dispatcher.
//
// Side effects:
//   - None.
func New(
	streamer Streamer,
	dispatchEngine swarm.DispatchEngine,
	swarmRegistry *swarm.Registry,
	agentRegistry *agent.Registry,
	sessionManager SessionManager,
) *Dispatcher {
	return &Dispatcher{
		streamer:       streamer,
		dispatchEngine: dispatchEngine,
		swarmRegistry:  swarmRegistry,
		agentRegistry:  agentRegistry,
		sessionManager: sessionManager,
	}
}

// errNoTarget fires when DispatchEphemeral is called without a usable
// AgentID and ScanMentions yielded no swarm hit. Mirrors the
// orchestrator's errNoTarget shape.
var errNoTarget = errors.New("dispatch: no agent or swarm target resolved from request")

// DispatchEphemeral runs the "user input → engine stream" lifecycle for
// callers that have NO session anchor (today: /api/chat). Resolution
// follows the same logic the orchestrator's ProcessUserInput uses —
// scan @-mentions when ScanMentions is true, fall through to AgentID
// otherwise — and dispatches via swarm.DispatchSwarm when the resolver
// returns a swarm target, plain streaming.Run otherwise.
//
// Streamer lifetime is decoupled from ctx via context.WithoutCancel
// inside this method so the engine stream survives handler return.
// This preserves commit 51fb416c's pattern at the Dispatcher seam
// rather than at the handler boundary — handlers cannot accidentally
// re-couple the streamer to r.Context() by forgetting the wrap.
//
// Expected:
//   - ctx is the caller's context (typically r.Context()). The handler
//     may return / cancel ctx at any point AFTER this method returns;
//     the engine stream continues until the underlying chunks channel
//     drains because Dispatcher passes context.WithoutCancel(ctx) to
//     swarm.DispatchSwarm / streaming.Run internally.
//   - req carries the message and AgentID. SessionID is ignored here.
//   - consumer is the caller's SSE / writer / channel-pump consumer.
//     Receives every chunk this dispatch produces.
//
// Returns:
//   - EphemeralHandle.Done emits one terminal error (nil on success)
//     and then closes. The caller awaits Done before writing its SSE
//     finaliser to avoid racing the streamer's last chunk.
//   - An error from the synchronous resolve / pre-stream phase (e.g.
//     errNoTarget, swarm.NotFoundError). When non-nil, EphemeralHandle
//     is the zero value (Done is nil) and the caller must NOT await.
//
// Side effects:
//   - Spawns one goroutine carrying swarm.DispatchSwarm or
//     streaming.Run; the goroutine closes Done when the stream
//     completes.
//   - Calls dispatchEngine.SetSwarmContext / FlushSwarmLifecycle /
//     RestoreManifest when a swarm dispatch resolves AND dispatchEngine
//     is non-nil. Identical to swarm.DispatchSwarm's documented
//     side-effects (see internal/swarm/dispatch_service.go:67).
func (d *Dispatcher) DispatchEphemeral(
	ctx context.Context,
	req DispatchRequest,
	consumer streaming.StreamConsumer,
) (EphemeralHandle, error) {
	if d.streamer == nil {
		return EphemeralHandle{}, errors.New("dispatch: streamer not configured")
	}

	leadID, swarmCtx, err := d.resolve(req)
	if err != nil {
		return EphemeralHandle{}, err
	}

	// Decouple the streamer's lifetime from the caller's ctx. Pattern
	// imported from commit 51fb416c (originally applied at the
	// /messages handler boundary); centralising it inside Dispatcher
	// means /api/chat (and every future ephemeral caller) inherits the
	// fix without needing to re-apply context.WithoutCancel at the
	// handler edge.
	streamCtx := context.WithoutCancel(ctx)

	done := make(chan error, 1)
	go func() {
		defer close(done)
		var dispatchErr error
		if swarmCtx != nil {
			dispatchErr = swarm.DispatchSwarm(
				streamCtx,
				d.dispatchEngine,
				swarmCtx,
				d.streamer,
				consumer,
				leadID,
				req.Content,
			)
		} else if d.dispatchEngine != nil {
			// Plain-agent dispatch through the engine — preserves the
			// SetSwarmContext(nil) / FlushSwarmLifecycle wind-down that
			// /api/chat used to get for free via the orchestrator.
			dispatchErr = swarm.DispatchSwarm(
				streamCtx,
				d.dispatchEngine,
				nil,
				d.streamer,
				consumer,
				leadID,
				req.Content,
			)
		} else {
			// Bare-engine pass-through: no swarm, no engine. Matches
			// the legacy /api/chat fallback at server.go::handleChat
			// when swarmRegistry is unset (test surface).
			dispatchErr = streaming.Run(streamCtx, d.streamer, consumer, leadID, req.Content)
		}
		done <- dispatchErr
	}()

	return EphemeralHandle{Done: done}, nil
}

// DispatchSessioned is the Phase 2 entry point. Phase 1 ships a stub
// that returns an explicit error so callers do not accidentally route
// /messages through here before Phase 2's migration lands.
func (d *Dispatcher) DispatchSessioned(
	_ context.Context,
	_ DispatchRequest,
	_ streaming.StreamConsumer,
) (SessionedHandle, error) {
	return SessionedHandle{}, fmt.Errorf("dispatch: DispatchSessioned not yet wired — Phase 2 of Dispatcher Service Unification (May 2026)")
}

// resolve picks the target agent or swarm. ScanMentions=true scans
// req.Content for the first @-mention that resolves to a swarm; agent
// @-mentions and unknown @-mentions fall through to req.AgentID.
// Mirrors internal/orchestrator/orchestrator.go::resolve (predecessor
// commit a833fd3a defined the auto-dispatch contract that this method
// preserves through swarm.ResolveTarget).
//
// Expected:
//   - req is the dispatch input.
//
// Returns:
//   - leadID, swarmCtx as defined by swarm.ResolveTarget.
//   - errNoTarget when AgentID is empty AND no scanned mention
//     resolved to a swarm.
//
// Side effects:
//   - None.
func (d *Dispatcher) resolve(req DispatchRequest) (string, *swarm.Context, error) {
	hasAgent := d.agentLookup()

	if req.ScanMentions {
		for _, mention := range swarm.ExtractAtMentions(req.Content) {
			leadID, swarmCtx, err := swarm.ResolveTarget(hasAgent, d.swarmRegistry, mention)
			if err == nil && swarmCtx != nil {
				return leadID, swarmCtx, nil
			}
		}
	}

	if req.AgentID == "" {
		return "", nil, errNoTarget
	}
	return swarm.ResolveTarget(hasAgent, d.swarmRegistry, req.AgentID)
}

// agentLookup returns a swarm.HasAgent closure backed by the
// agentRegistry. Mirrors orchestrator.agentLookup. nil agentRegistry
// returns nil so swarm.ResolveTarget short-circuits to bare-engine
// pass-through (see internal/swarm/resolve_target.go:42).
//
// Side effects:
//   - None.
func (d *Dispatcher) agentLookup() swarm.HasAgent {
	if d.agentRegistry == nil {
		return nil
	}
	return func(name string) bool {
		if _, ok := d.agentRegistry.Get(name); ok {
			return true
		}
		_, ok := d.agentRegistry.GetByNameOrAlias(name)
		return ok
	}
}
