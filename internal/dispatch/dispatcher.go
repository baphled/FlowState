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
// Phase 1 shipped DispatchEphemeral fully wired with DispatchSessioned
// as a stub; Phase 2 migrated /messages onto DispatchSessioned and
// deleted the parallel resolve helpers; Phase 3 (this commit) folds the
// swarm lifecycle into a per-session handshake gate, closing the back-
// to-back POST race surface (S2 in the v6 codebase audit).
package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/turn"
)

// ErrTurnConflict surfaces from DispatchSessioned when a second POST
// arrives for a session whose prior Turn is still StatusRunning. The
// /messages HTTP handler maps this to 409 Conflict — Phase 2 of the
// "Turn-Based Post-Then-Poll Architecture (May 2026)" plan. Re-
// exported through the dispatch package so callers don't need to
// import internal/turn just to compare the sentinel.
var ErrTurnConflict = turn.ErrTurnConflict

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
//
// TurnID was added in Phase 1 of the "Turn-Based Post-Then-Poll
// Architecture (May 2026)" plan: every sessioned dispatch mints a
// fresh UUID turn id which the HTTP handler returns to the client
// (Phase 2 wires the response shape) so the client can poll
// `GET /turns/{turn_id}` for the eventual outcome — decoupling the
// client from the SSE wire's lifetime. The id also threads through
// the engine pipeline via ctx (turn.WithTurnID) so the accumulator
// can append engine-emitted messages onto the turn's MessagesAdded
// list. Existing callers that ignore TurnID see no behavioural
// change.
type SessionedHandle struct {
	// Snapshot is the session as it stood AFTER appending the user
	// message and BEFORE the assistant turn streams.
	Snapshot session.Session
	// TurnID is the freshly-minted UUID for this dispatch's Turn.
	// Non-empty for every successful DispatchSessioned call.
	TurnID string
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
// needs to (a) append the user message to the persistent session, (b)
// drive the streamer over its chunks channel, and (c) capture the
// post-append snapshot to hand back to the caller. The narrow interface
// is the same surface the API package's handleSessionMessage handler
// uses today; the dispatch package owns it so swapping the production
// *session.Manager for an in-memory test fake stays a one-line change.
type SessionManager interface {
	// SnapshotSession projects the persistent session into a value-typed
	// copy. Used by DispatchSessioned to capture state post-user-message-
	// append and pre-stream-completion, mirroring the e4bf9632 async-POST
	// contract.
	SnapshotSession(id string) (session.Session, error)
	// SendMessageWithAttachments appends the user message inside its
	// critical section, then returns a chunks channel driven by the
	// underlying streamer. ctx threads any per-turn overrides
	// (session.WithStreamAgentOverride for @-mention redirects). Empty
	// attachmentIDs falls through to plain SendMessage internally.
	SendMessageWithAttachments(ctx context.Context, sessionID, message string, attachmentIDs []string) (<-chan provider.StreamChunk, error)
}

// SessionBroker is the slice of internal/api.SessionBroker that
// DispatchSessioned needs to fan chunks out to live SSE / WS
// subscribers. The Dispatcher invokes Publish in its OWN goroutine so
// the caller's handler returns immediately after the snapshot is
// captured — there is no Done channel to await on, and the broker
// owns subscriber bookkeeping.
type SessionBroker interface {
	Publish(sessionID string, chunks <-chan provider.StreamChunk)
}

// Dispatcher is the single owner of the "user input → engine stream"
// lifecycle. Wiring is constructor-injected; nil-tolerance is per-method
// because tests for the ephemeral path do not need the session manager
// and vice versa.
//
// sessionLifecycleGates is the per-session handshake (Phase 3 of the v6
// plan) that closes the back-to-back POST race surface (S2). The map's
// values are buffered channels (size 1) used as one-shot baton handoffs:
//   - On `DispatchSessioned(req)` entry, the goroutine load-or-stores
//     a channel for req.SessionID and BLOCKS on receive until the
//     prior turn's flush goroutine sends the baton through.
//   - After the chunks drain AND `FlushSwarmLifecycle + RestoreManifest`
//     complete, the flush goroutine SENDS on the channel so the NEXT
//     call for this sessionID can proceed.
//   - The first call for a sessionID stores an already-loaded channel
//     (baton pre-sent) so it doesn't block.
//
// Per-session keying (anti-pattern note): a single Dispatcher-wide
// mutex OR a key on dispatchEngine instance would silently serialise
// ALL `/messages` globally (Dispatcher holds ONE dispatchEngine; per-
// engine keying degenerates to global). The plan's v6 round-5 fix text
// is explicit on this — the gate MUST be keyed by req.SessionID.
type Dispatcher struct {
	streamer              Streamer
	dispatchEngine        swarm.DispatchEngine
	swarmRegistry         *swarm.Registry
	agentRegistry         *agent.Registry
	sessionManager        SessionManager
	sessionBroker         SessionBroker
	sessionLifecycleGates sync.Map // map[sessionID string]chan struct{}
	// turnRegistry is the in-memory store of live + terminal Turns
	// (Phase 1 of the "Turn-Based Post-Then-Poll Architecture
	// (May 2026)" plan). DispatchSessioned calls Start at entry,
	// injects turn_id into the streamCtx via turn.WithTurnID, and
	// hands ownership of Complete/Fail to the wrap goroutine that
	// drains the chunks channel. Never nil — New / NewWithTurns
	// always wire a registry instance.
	turnRegistry *turn.Registry
}

// New wires a Dispatcher. All dependencies are nullable for test
// minimality; the entry methods enforce their own readiness:
//
//   - DispatchEphemeral requires streamer; swarmRegistry/agentRegistry/
//     dispatchEngine are nil-tolerant (a nil swarmRegistry short-circuits
//     to plain-agent streaming via streaming.Run, matching the API's
//     legacy fallback at internal/api/server.go::handleChat).
//   - DispatchSessioned requires sessionManager. sessionBroker is
//     optional — when nil the Dispatcher drains the chunks channel into
//     a sink so the streamer goroutine still terminates cleanly (mirrors
//     the pre-Phase-2 handler's nil-broker fallback at
//     internal/api/server.go::handleSessionMessage).
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
//   - sessionManager is the session anchor for DispatchSessioned.
//   - sessionBroker fans chunks to live SSE / WS subscribers; nil
//     drains chunks into a sink for test-surface compositions.
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
	sessionBroker SessionBroker,
) *Dispatcher {
	return NewWithTurns(streamer, dispatchEngine, swarmRegistry, agentRegistry, sessionManager, sessionBroker, nil)
}

// NewWithTurns wires a Dispatcher with an externally-supplied Turn
// registry. The production wiring uses this constructor so Phase 2's
// HTTP handler (GET /turns/{id}) can share the SAME registry instance
// the Dispatcher writes into — without sharing the instance the
// handler would read from an empty store. A nil turnRegistry falls
// back to a fresh per-Dispatcher registry (matches the test surface
// and lets the legacy New() constructor stay byte-compatible).
//
// Expected:
//   - turnRegistry is the shared registry. nil constructs a fresh
//     internal registry — useful for tests and pre-Phase-2 wiring
//     where the HTTP handler doesn't read from it yet.
//
// Returns:
//   - A configured *Dispatcher with the registry wired.
//
// Side effects:
//   - None.
func NewWithTurns(
	streamer Streamer,
	dispatchEngine swarm.DispatchEngine,
	swarmRegistry *swarm.Registry,
	agentRegistry *agent.Registry,
	sessionManager SessionManager,
	sessionBroker SessionBroker,
	turnRegistry *turn.Registry,
) *Dispatcher {
	if turnRegistry == nil {
		turnRegistry = turn.NewRegistry()
	}
	return &Dispatcher{
		streamer:       streamer,
		dispatchEngine: dispatchEngine,
		swarmRegistry:  swarmRegistry,
		agentRegistry:  agentRegistry,
		sessionManager: sessionManager,
		sessionBroker:  sessionBroker,
		turnRegistry:   turnRegistry,
	}
}

// TurnRegistry returns the Dispatcher's Turn registry so Phase 2's
// HTTP handler can read Turn state by id. Always non-nil after
// construction.
func (d *Dispatcher) TurnRegistry() *turn.Registry {
	return d.turnRegistry
}

// SetSessionBroker updates the broker reference after construction.
// Required because production wiring (internal/app/app.go:444-446) creates
// the broker AFTER NewServer has already auto-constructed the Dispatcher;
// without this setter the Dispatcher captures a nil broker at construction
// time and silently drops chunks (no fan-out to SSE subscribers).
// Discovered via a live curl probe on May 18 2026 — chunks reached the
// accumulator (assistant message persisted) but never the SSE broker
// (every refresh-symptom report this session traced back here).
func (d *Dispatcher) SetSessionBroker(broker SessionBroker) {
	d.sessionBroker = broker
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
		done <- d.runEphemeralStream(streamCtx, leadID, swarmCtx, req, consumer)
	}()

	return EphemeralHandle{Done: done}, nil
}

// RunEphemeralSync is the synchronous-await variant of DispatchEphemeral.
// Same resolution + lifecycle (snapshot → SetSwarmContext → stream → flush
// → restore via swarm.DispatchSwarm), but the streamer ctx is NOT
// decoupled from the caller's ctx — cancellation propagates straight into
// the provider's Stream call. This is the contract the CLI surfaces
// (`flowstate run`, `flowstate chat --message`) and the
// orchestrator.ProcessUserInput wrapper rely on so a SIGTERM-driven ctx
// cancel mid-stream still reaches the provider and produces the
// {Error: ctx.Err(), Done: true} chunk the engine surfaces (pinned by the
// "persists the parent session on context cancellation mid-stream"
// regression at internal/cli/run_test.go:528).
//
// Phase 5 of "Dispatcher Service Unification (May 2026)" folds the
// orchestrator into a thin wrapper over the Dispatcher; this entrypoint
// gives the orchestrator a synchronous flavour that preserves CLI
// ctx-cancel semantics while still routing through Dispatcher's shared
// resolution + lifecycle. The async DispatchEphemeral path (HTTP
// handlers that may return before the stream completes) keeps its
// context.WithoutCancel wrap so HTTP callers don't accidentally re-couple
// the streamer to r.Context() by forgetting to wrap.
//
// Expected:
//   - ctx is the caller's context. Cancellation DOES propagate into the
//     streamer — that is the load-bearing semantic for CLI surfaces.
//   - req carries the message + AgentID + ScanMentions. SessionID is
//     ignored.
//   - consumer is the caller's WriterConsumer / JSONConsumer / channel-
//     pump consumer.
//
// Returns:
//   - The terminal error from swarm.DispatchSwarm / streaming.Run, or
//     nil on clean completion. The errNoTarget / swarm.NotFoundError
//     resolution errors are also surfaced here (no goroutine; no Done
//     channel; the caller sees the error directly).
//
// Side effects:
//   - Drives the streamer + consumer synchronously through
//     swarm.DispatchSwarm — see that function for the manifest snapshot
//     / SetSwarmContext / FlushSwarmLifecycle / RestoreManifest side
//     effects.
func (d *Dispatcher) RunEphemeralSync(
	ctx context.Context,
	req DispatchRequest,
	consumer streaming.StreamConsumer,
) error {
	if d.streamer == nil {
		return errors.New("dispatch: streamer not configured")
	}

	leadID, swarmCtx, err := d.resolve(req)
	if err != nil {
		return err
	}

	return d.runEphemeralStream(ctx, leadID, swarmCtx, req, consumer)
}

// runEphemeralStream is the shared "drive the streamer + consumer to
// completion" path used by both DispatchEphemeral (under a goroutine
// + WithoutCancel) and RunEphemeralSync (synchronous + ctx-propagating).
// Splitting the body lets the two entrypoints make different
// ctx-handling choices while keeping the dispatch shape identical.
//
// Side effects:
//   - See swarm.DispatchSwarm / streaming.Run.
func (d *Dispatcher) runEphemeralStream(
	ctx context.Context,
	leadID string,
	swarmCtx *swarm.Context,
	req DispatchRequest,
	consumer streaming.StreamConsumer,
) error {
	if swarmCtx != nil {
		return swarm.DispatchSwarm(
			ctx,
			d.dispatchEngine,
			swarmCtx,
			d.streamer,
			consumer,
			leadID,
			req.Content,
		)
	}
	if d.dispatchEngine != nil {
		// Plain-agent dispatch through the engine — preserves the
		// SetSwarmContext(nil) / FlushSwarmLifecycle wind-down that
		// /api/chat used to get for free via the orchestrator.
		return swarm.DispatchSwarm(
			ctx,
			d.dispatchEngine,
			nil,
			d.streamer,
			consumer,
			leadID,
			req.Content,
		)
	}
	// Bare-engine pass-through: no swarm, no engine. Matches the legacy
	// /api/chat fallback at server.go::handleChat when swarmRegistry is
	// unset (test surface).
	return streaming.Run(ctx, d.streamer, consumer, leadID, req.Content)
}

// DispatchSessioned runs the "user input → engine stream" lifecycle for
// callers that anchor a persistent session (today: /messages; Phase 4:
// handleSessionWebSocket). The contract — load-bearing per "Dispatcher
// Service Unification (May 2026)" Phase 2:
//
//  1. Append the user message to the session SYNCHRONOUSLY before
//     returning. The Snapshot in the returned SessionedHandle carries the
//     freshly-appended user row so the caller can write it back as JSON
//     before the streamer drains. This preserves e4bf9632's async-POST
//     contract structurally: SessionedHandle has no Done channel, so a
//     /messages handler cannot block on stream completion.
//  2. Resolve swarm dispatch in two passes: scan req.Content for the
//     FIRST in-content @<swarm-id> mention that resolves to a swarm
//     (subsumes commit 48380376), then fall back to
//     swarmRegistry.AutoDispatchSwarmFor(req.AgentID) when no mention
//     hit (subsumes commit 07b0480e). The in-content mention WINS over
//     the auto-dispatch because the user explicitly typed it. Mention
//     redirect is per-turn — the session's persistent agent_id is not
//     mutated, mirroring Orchestrator.resolve's ScanMentions=true
//     semantics.
//  3. Set the resolved swarm context on the engine BEFORE driving the
//     streamer. When the mention path wins, install
//     session.WithStreamAgentOverride on the streamer ctx so the engine
//     stamps the assistant turn under the swarm's lead (not the
//     session's persistent agent).
//  4. Apply context.WithoutCancel(ctx) to the streamer ctx so the engine
//     outlives the caller's handler return. Same pattern as
//     DispatchEphemeral / commit 51fb416c, centralised at the Dispatcher
//     seam rather than at every handler edge.
//  5. Spawn the broker publish goroutine
//     (go sessionBroker.Publish(sessionID, chunks)) so chunks fan out to
//     live SSE subscribers asynchronously. A nil broker drains the
//     channel into a sink so the streamer goroutine terminates cleanly
//     (test-surface compositions).
//  6. Return SessionedHandle{Snapshot} to the caller IMMEDIATELY. The
//     caller writes the snapshot as JSON and returns 200 OK without
//     awaiting stream completion.
//
// Swarm-lifecycle flush note (Phase 3 of the v6 plan):
// the FlushSwarmLifecycle + RestoreManifest pair runs in a Dispatcher-
// owned wrap goroutine after the chunks channel drains. A PER-SESSION
// lifecycle gate keyed by req.SessionID (sync.Map[sessionID]chan
// struct{}, capacity 1, baton semantics) sequences consecutive
// DispatchSessioned calls for the same sessionID — turn N+1's
// SetSwarmContext blocks until turn N's flush + restore complete. This
// closes the S2 race surface from the v6 audit. Per-session keying is
// load-bearing: a Dispatcher-wide mutex would silently serialise ALL
// /messages because Dispatcher holds ONE dispatchEngine instance, so
// any per-engine key degenerates to global. The gate's defer ordering
// is also load-bearing — release fires AFTER flush + restore so the
// next turn never observes mid-lifecycle engine state.
//
// Expected:
//   - ctx is the caller's context (typically r.Context()). The handler
//     may return / cancel ctx at any point AFTER this method returns;
//     the engine stream continues until the underlying chunks channel
//     drains because Dispatcher passes context.WithoutCancel(ctx) to
//     SendMessageWithAttachments internally.
//   - req carries the session anchor, agent fallback, message, optional
//     attachments, and the ScanMentions flag.
//   - consumer is accepted for parity with DispatchEphemeral so future
//     test-surfaces can wire SSE-shaped consumers through the same
//     entry point; the Phase 2 production path drives the broker, not
//     the consumer (the broker is the SSE fan-out for /messages today).
//     Sessioned callers that wire a non-nil consumer get nothing — the
//     consumer is reserved for Phase 4's WS integration where the
//     handler tees chunks to its own writer in parallel with the
//     broker. Documented here so the parameter does not become a silent
//     dead arg.
//
// Returns:
//   - SessionedHandle.Snapshot = the session as it stood AFTER appending
//     the user message and BEFORE the assistant turn streams.
//   - An error from the synchronous resolve / append phase (e.g.
//     ErrSessionNotFound, session.ErrAttachmentNotFound). When non-nil,
//     SessionedHandle is the zero value and the caller must NOT consume
//     it.
//
// Side effects:
//   - Calls sessionManager.SendMessageWithAttachments (mutates session
//     state: appends user message under WLock).
//   - When swarm dispatch active: calls dispatchEngine.SetSwarmContext,
//     ManifestSnapshot once each before driving the streamer; spawns a
//     goroutine that runs FlushSwarmLifecycle + RestoreManifest when
//     the chunks channel drains.
//   - Spawns a broker.Publish goroutine that fans chunks to SSE / WS
//     subscribers for the session.
func (d *Dispatcher) DispatchSessioned(
	ctx context.Context,
	req DispatchRequest,
	consumer streaming.StreamConsumer,
) (SessionedHandle, error) {
	if d.sessionManager == nil {
		return SessionedHandle{}, errors.New("dispatch: sessionManager not configured")
	}

	// Phase 1 — Turn-Based Post-Then-Poll Architecture (May 2026).
	//
	// Mint a fresh turn_id BEFORE acquiring the lifecycle gate. The
	// conflict check (registry.Start returns ErrTurnConflict when a
	// prior turn for this sessionID is still Running) MUST fire
	// synchronously so the HTTP handler returns 409 IMMEDIATELY
	// rather than blocking on the gate until turn 1 drains. The
	// gate's job is to serialise swarm lifecycle ordering on the
	// happy path; the Turn registry's job is to enforce the v1
	// "one in-flight turn per session" contract loudly.
	//
	// Ordering: Start → gate acquire → resolve → send → wrap. If
	// Start succeeds but a downstream step errors, the error-return
	// paths below MUST call turnRegistry.Fail so the conflict gate
	// clears for the next call. The wrap goroutine takes ownership
	// of Complete on the happy path.
	turnID, turnErr := d.turnRegistry.Start(req.SessionID)
	if turnErr != nil {
		return SessionedHandle{}, turnErr
	}
	// turnOwnedByWrap is set true the moment the wrap goroutine
	// takes ownership of the terminal Complete/Fail call. Mirrors
	// the gateTransferred pattern below — every synchronous-error
	// return below must Fail() the turn so the conflict gate clears.
	turnOwnedByWrap := false
	failTurnIfOwned := func(cause error) {
		if !turnOwnedByWrap {
			_ = d.turnRegistry.Fail(turnID, cause)
		}
	}

	// Per-session lifecycle handshake — Phase 3 of the v6 plan. Closes
	// the S2 race: turn N's FlushSwarmLifecycle + RestoreManifest used
	// to race turn N+1's SetSwarmContext, leaving the engine carrying
	// stale manifest state into the next turn. The gate is a baton
	// channel keyed by sessionID:
	//   - The map value is `chan struct{}` with capacity 1, initialised
	//     "charged" (one element pre-loaded) on first call for the
	//     sessionID.
	//   - DispatchSessioned receives from the channel — blocking until
	//     a prior turn returns the baton. The first call's pre-charged
	//     channel makes the first receive non-blocking.
	//   - After the flush + restore completes (or the synchronous error
	//     path returns), the dispatch path sends on the channel,
	//     returning the baton so the next turn can proceed.
	//
	// Per-session keying is load-bearing — a global mutex (or one keyed
	// on dispatchEngine) would silently serialise ALL /messages because
	// Dispatcher holds ONE engine instance. The plan's v6 round-5
	// blocker B1 text mandates the sync.Map[sessionID] shape.
	gate := d.acquireSessionLifecycleGate(req.SessionID)
	// gateTransferred is set true the moment the wrap goroutine takes
	// over baton ownership. The function-exit safety defer only fires
	// the release when ownership has NOT been transferred — preventing
	// the outer return from releasing the gate before the wrap
	// goroutine completes flush + restore.
	gateTransferred := false
	releaseGateSync := func() {
		d.releaseSessionLifecycleGate(req.SessionID, gate)
	}
	// Safety net: every error-return path below either calls
	// releaseGateSync() synchronously OR sets gateTransferred=true to
	// hand ownership to the wrap goroutine. If the function unwinds
	// with neither happening (a code path I missed), this defer
	// releases the baton so the next call doesn't deadlock.
	defer func() {
		if !gateTransferred {
			releaseGateSync()
		}
	}()

	// Decouple the streamer's lifetime from the caller's ctx. Pattern
	// imported from commit 51fb416c (originally applied at the
	// /messages handler boundary); centralising it inside Dispatcher
	// means every sessioned caller inherits the fix without needing to
	// re-apply context.WithoutCancel at the handler edge.
	streamCtx := context.WithoutCancel(ctx)
	// Phase 1 — inject the freshly-minted turn_id so every downstream
	// consumer (engine, accumulator) can read it via
	// turn.TurnIDFromContext / session.AccumulatorTurnIDFromContext
	// without explicit plumbing.
	//
	// Two keys, one id: the turn id is written under BOTH
	// internal/turn's turnIDKey (for engine-level consumers and
	// public callers) AND internal/session's turnIDCtxKey (for the
	// accumulator inside the session package). Both keys point to
	// the same id — the dual-key avoidance is purely a Go import-
	// cycle workaround (turn imports session for Message, so a
	// reverse import would cycle).
	//
	// We also inject the TurnMessageRecorder closure that wraps
	// turnRegistry.Append — the accumulator extracts the closure
	// from ctx and calls it at each persistence site so every
	// engine-emitted message (assistant, thinking, tool_call,
	// tool_result, delegation) lands on the Turn registry's
	// per-turn MessagesAdded slice.
	streamCtx = turn.WithTurnID(streamCtx, turnID)
	streamCtx = session.WithAccumulatorTurnID(streamCtx, turnID)
	streamCtx = session.WithTurnRecorder(streamCtx, func(id string, msg session.Message) {
		_ = d.turnRegistry.Append(id, msg)
	})

	// Resolution: in-content @<swarm-id> mention wins over auto-dispatch.
	// Both passes are guarded by full registry wiring (swarm + agent +
	// engine present) — partial wiring (test surfaces) falls through to
	// the plain-agent path with no swarm context installed.
	var (
		swarmCtx         *swarm.Context
		manifestSnapshot any
		swarmActive      bool
		leadOverride     string
	)

	if d.canDispatchSwarm() {
		// Pass 1 — in-content mention.
		if req.ScanMentions && req.Content != "" {
			hasAgent := d.agentLookup()
			if hasAgent != nil {
				for _, mention := range swarm.ExtractAtMentions(req.Content) {
					_, mentionedCtx, mentionErr := swarm.ResolveTarget(hasAgent, d.swarmRegistry, mention)
					if mentionErr != nil || mentionedCtx == nil {
						continue
					}
					manifestSnapshot = d.dispatchEngine.ManifestSnapshot()
					swarmCtx = mentionedCtx
					swarmActive = true
					leadOverride = mentionedCtx.LeadAgent
					break
				}
			}
		}

		// Pass 2 — auto-dispatch fallback when no mention hit.
		if !swarmActive && req.AgentID != "" {
			if manifest, ok := d.swarmRegistry.AutoDispatchSwarmFor(req.AgentID); ok {
				ctx := swarm.NewContext(manifest.ID, manifest)
				swarmCtx = &ctx
				manifestSnapshot = d.dispatchEngine.ManifestSnapshot()
				swarmActive = true
			}
		}
	}

	if swarmActive {
		// Install BEFORE SendMessageWithAttachments so the engine sees
		// the swarm context when it starts streaming the turn. The
		// per-turn lead override (mention path only) threads through
		// the ctx so the user-message stamp stays under the session's
		// persistent agent while the streamer drives under the swarm's
		// lead. Matches the userMessageAgent vs streamAgent split in
		// session/manager.go:1227-1234.
		d.dispatchEngine.SetSwarmContext(swarmCtx)
		if leadOverride != "" {
			streamCtx = session.WithStreamAgentOverride(streamCtx, leadOverride)
		}
	}

	chunks, err := d.sessionManager.SendMessageWithAttachments(
		streamCtx, req.SessionID, req.Content, req.AttachmentIDs,
	)
	if err != nil {
		if swarmActive {
			// Stream never started — restore the manifest immediately
			// so a failed handler doesn't leave the engine re-
			// identified as the swarm lead for subsequent turns.
			d.dispatchEngine.RestoreManifest(manifestSnapshot)
		}
		// Stream never started — gateTransferred stays false, so the
		// safety-net defer releases the baton synchronously after we
		// return. Next call for this sessionID is unblocked.
		// turnOwnedByWrap stays false — Fail the turn now so the
		// per-session conflict gate clears synchronously.
		failTurnIfOwned(err)
		return SessionedHandle{}, err
	}

	// Snapshot AFTER the user message append (inside SendMessage's
	// critical section) so the returned Snapshot carries the new user
	// row. The Snapshot path is value-typed — no *Session escapes the
	// manager's lock boundary (see vault note "Session Messages Data
	// Race in SSE Fast-Path (May 2026)" § "Sibling races").
	snap, snapErr := d.sessionManager.SnapshotSession(req.SessionID)
	if snapErr != nil {
		if swarmActive {
			d.dispatchEngine.RestoreManifest(manifestSnapshot)
		}
		failTurnIfOwned(snapErr)
		return SessionedHandle{}, snapErr
	}

	if chunks != nil {
		// Hand baton ownership to the wrap goroutine — the safety-net
		// defer at function exit will skip the release. The wrap's
		// inner defers run flush → restore → release in LIFO order,
		// so the gate ONLY drops after the engine is fully restored.
		gateTransferred = true
		// Hand Turn ownership to the wrap goroutine too — Complete
		// fires after the chunks channel drains cleanly, Fail fires
		// if the terminal chunk carries an Error. The wrap goroutine
		// MUST call exactly one of {Complete, Fail} so the per-session
		// conflict gate clears.
		turnOwnedByWrap = true
		chunks = d.wrapWithTurnLifecycle(chunks, turnID)
		chunks = d.wrapWithSwarmLifecycle(
			streamCtx, chunks, manifestSnapshot, swarmActive, releaseGateSync,
		)
		// Phase 4 — Dispatcher Service Unification (May 2026) S1 closure.
		//
		// When a consumer is wired (handleSessionWebSocket, Phase 4),
		// tee chunks to BOTH the broker (live SSE subscribers, if any)
		// AND the consumer (the WS handler's frame-writer pump). Pre-
		// Phase-4 the consumer arg was discarded; the WS handler took
		// chunks directly from sessionManager.SendMessage which kept
		// r.Context() coupled to the engine. Routing through the
		// consumer here lets the WS handler keep its existing
		// out-channel + quit-signal pattern while the streamer's
		// lifetime is decoupled via context.WithoutCancel inside this
		// method (load-bearing for S1).
		d.fanOutSessionedChunks(req.SessionID, chunks, consumer)
	} else if swarmActive {
		// Nil chunks channel + swarm active — manager returned cleanly
		// without driving the streamer. Restore the manifest
		// synchronously so engine state doesn't leak.
		d.dispatchEngine.RestoreManifest(manifestSnapshot)
		// gateTransferred remains false — the safety-net defer
		// releases the baton. Complete the turn synchronously since
		// no chunks will ever drain; an empty MessagesAdded turn is
		// a valid "completed with no engine output" terminal state.
		_ = d.turnRegistry.Complete(turnID, turn.ModelInfo{})
	} else {
		// Nil chunks + no swarm — same Complete-synchronously path so
		// the conflict gate clears.
		_ = d.turnRegistry.Complete(turnID, turn.ModelInfo{})
	}

	return SessionedHandle{Snapshot: snap, TurnID: turnID}, nil
}

// wrapWithTurnLifecycle observes the chunks channel as it drains and
// fires the terminal turn.Registry transition exactly once when src
// closes. The terminal call is Fail when the last chunk carried a
// non-nil Error (engine surface for provider errors and ctx-cancel),
// Complete otherwise. The wrap also captures the (provider, model)
// pair from every chunk that carries it so a mid-stream failover —
// where the engine restamps ProviderID/ModelID after switching
// candidates — surfaces the FINAL pair on the Turn record, matching
// the accumulator's lastModelID / lastProviderID tracking pattern
// (internal/session/accumulator.go:296-309).
//
// Ordering note: this wrap is the INNERMOST in the chunk pipeline —
// wrapWithSwarmLifecycle wraps THIS, so the terminal Turn call fires
// BEFORE the swarm lifecycle's FlushSwarmLifecycle / RestoreManifest
// (LIFO defer order in wrapWithSwarmLifecycle's goroutine puts
// RestoreManifest LAST). This is load-bearing for the conflict gate
// — turn.Complete clears byActiveSession synchronously, so by the
// time the per-session lifecycle gate releases (which the wrap goroutine
// fires AFTER FlushSwarmLifecycle), the NEXT turn's Start has the
// active-session entry cleared and can proceed without ErrTurnConflict.
//
// Side effects:
//   - Spawns one goroutine that consumes src to completion.
//   - Calls turnRegistry.Complete OR turnRegistry.Fail exactly once.
func (d *Dispatcher) wrapWithTurnLifecycle(
	src <-chan provider.StreamChunk,
	turnID string,
) <-chan provider.StreamChunk {
	out := make(chan provider.StreamChunk, 64)
	go func() {
		defer close(out)
		var (
			lastModelID    string
			lastProviderID string
			terminalErr    error
		)
		for chunk := range src {
			if chunk.ModelID != "" {
				lastModelID = chunk.ModelID
			}
			if chunk.ProviderID != "" {
				lastProviderID = chunk.ProviderID
			}
			// Phase-5 §1c-α: surface the live (provider, model) pair onto
			// the Turn registry as soon as the engine announces it via
			// `model_active` (every successful stream) or `provider_changed`
			// (mid-stream failover). The pre-1c path only stamped the pair
			// onto Turn.Model on Complete, which left the long-poll surface
			// blank during the entire Running lifetime — the chat-UI's
			// toolbar chip couldn't observe a failover until terminal.
			//
			// The registry's own change-gate suppresses spurious broadcasts
			// when the pair is unchanged, so calling SetProviderModel on
			// every announcement chunk is safe; gating by EventType here
			// keeps the dispatcher tap deliberate (each event type's
			// semantics is the documented contract — see internal/engine).
			//
			// Plan ref: ~/vaults/baphled/1. Projects/FlowState/Plans/
			//   Phase-5 Turn-Endpoint Event-Type Parity (May 2026).md §1c-α.
			if chunk.EventType == "model_active" || chunk.EventType == "provider_changed" {
				if lastProviderID != "" || lastModelID != "" {
					d.turnRegistry.SetProviderModel(turnID, lastProviderID, lastModelID)
				}
			}
			// Phase-5 §1c-β: surface the live context_usage figure onto the
			// Turn registry so the long-poll wire exposes it without an SSE
			// side-channel. The engine emits a chunk{EventType:"context_usage",
			// Content:<json>} as the first artefact of every Stream that has
			// enough information to compute it (engine.go:3262
			// buildContextUsageChunk + engine.go:2643 Stream). The wire shape
			// mirrors sseContextUsage at internal/api/sse_writers.go:142-150.
			//
			// Parse failure is silently absorbed — a malformed chunk is
			// already dropped by the existing SSE writer (writeSSEContextUsage
			// silently drops malformed payloads to keep the chip stable);
			// the long-poll surface inherits the same forward-compat policy.
			if chunk.EventType == "context_usage" && chunk.Content != "" {
				var cu turn.ContextUsage
				if err := json.Unmarshal([]byte(chunk.Content), &cu); err == nil {
					d.turnRegistry.SetContextUsage(turnID, &cu)
				}
			}
			// Phase-5 §1c-β: surface provider_quota snapshots onto the Turn
			// registry. The engine emits a chunk{EventType:"provider_quota",
			// Content:<json>} inline before reply and post-turn (see
			// engine.go:2650 Stream + buildProviderQuotaChunk). The wire
			// shape mirrors sseProviderQuota at internal/api/sse_writers.go:
			// 176-189. UpsertProviderQuota's partition-key semantics dedup
			// per-partition so multiple snapshots accumulate (anthropic +
			// zai after failover, anthropic + openai across @-mention
			// swarm hops); each partition's most-recent payload wins.
			//
			// Parse failure: same silent-absorb policy as context_usage.
			if chunk.EventType == "provider_quota" && chunk.Content != "" {
				var snap turn.ProviderQuotaSnapshot
				if err := json.Unmarshal([]byte(chunk.Content), &snap); err == nil {
					d.turnRegistry.UpsertProviderQuota(turnID, snap)
				}
			}
			if chunk.Error != nil {
				// Capture the FIRST non-nil error — subsequent chunks
				// may carry the same error redundantly (engine's
				// Done-on-error path emits {Error, Done} together).
				if terminalErr == nil {
					terminalErr = chunk.Error
				}
			}
			out <- chunk
		}
		// Terminal transition — exactly one of {Fail, Complete} fires.
		// Errors from the registry call are swallowed: an ErrTurnTerminal
		// would only happen if a producer-side bug already transitioned
		// the turn (e.g. a future test injecting a Complete) and is not
		// actionable from this seam.
		if terminalErr != nil {
			_ = d.turnRegistry.Fail(turnID, terminalErr)
			return
		}
		_ = d.turnRegistry.Complete(turnID, turn.ModelInfo{
			Provider: lastProviderID,
			Model:    lastModelID,
		})
	}()
	return out
}

// acquireSessionLifecycleGate returns the per-session baton channel for
// req.SessionID after blocking on receive — proving the prior turn's
// flush + restore have completed (or this is the first call for this
// sessionID, in which case the receive returns immediately because the
// channel was created pre-charged).
//
// Channel semantics:
//   - capacity 1, pre-charged on first creation
//   - acquire = receive (blocks until baton available)
//   - release = send (returns the baton to the channel)
//
// Side effects:
//   - LoadOrStore in sessionLifecycleGates; consumes one buffered slot
//     by receiving the baton from the channel.
func (d *Dispatcher) acquireSessionLifecycleGate(sessionID string) chan struct{} {
	// Build the pre-charged channel template; LoadOrStore returns the
	// existing channel if another goroutine raced us. The "new" channel
	// gets the baton pre-loaded so the first call for a sessionID
	// doesn't block.
	fresh := make(chan struct{}, 1)
	fresh <- struct{}{}
	actual, loaded := d.sessionLifecycleGates.LoadOrStore(sessionID, fresh)
	gate := actual.(chan struct{})
	if loaded {
		// The pre-charged channel we built was thrown away; the existing
		// channel may have its baton in flight if another turn for this
		// sessionID is still running. Block until the baton arrives.
		<-gate
	} else {
		// We won the race; consume our own pre-charge so we hold the
		// baton symmetric to the loaded path.
		<-gate
	}
	return gate
}

// releaseSessionLifecycleGate returns the baton to the per-session
// channel so the NEXT call for this sessionID can proceed. Idempotency
// is enforced by the caller's `gateReleased` flag in DispatchSessioned —
// this method assumes it holds the baton.
//
// Side effects:
//   - Non-blocking send (capacity 1, no contending holder).
func (d *Dispatcher) releaseSessionLifecycleGate(_ string, gate chan struct{}) {
	select {
	case gate <- struct{}{}:
	default:
		// Capacity-1 channel already has the baton — should only
		// happen on a programming error (double-release). Swallowed
		// silently; the next acquire still works.
	}
}

// canDispatchSwarm reports whether the swarm registry + dispatch engine
// + session manager are all wired so swarm dispatch is observable on
// the engine. Test surfaces that omit any of the three fall through to
// plain-agent streaming.
func (d *Dispatcher) canDispatchSwarm() bool {
	return d.swarmRegistry != nil && d.dispatchEngine != nil && d.sessionManager != nil
}

// wrapWithSwarmLifecycle pumps src into a new channel and, after src
// closes, runs the post-stream swarm dispatch lifecycle
// (FlushSwarmLifecycle + RestoreManifest when swarmActive) so the engine
// ends in the same shape Orchestrator.Stream leaves it: swarm context
// is sticky (the next dispatch overwrites it) but the manifest reverts
// to the pre-dispatch identity so non-swarm turns don't inherit the
// swarm lead's persona. Mirrors the deleted server.go::
// wrapWithSwarmLifecycle at server.go:1409-1431 (pre-Phase-2). Moved
// INTO Dispatcher per Phase 2; Phase 3 folds the per-session gate
// release in here so the gate ONLY drops after the lifecycle completes.
//
// Defer ordering (load-bearing): the inner defer fires in LIFO order —
//
//  1. close(out)         (innermost — emit done to broker)
//  2. releaseGate()      (drops the per-session baton)
//  3. RestoreManifest()  (engine state reverted to baseline)
//  4. FlushSwarmLifecycle() (gates record cleanup)
//
// The gate MUST release AFTER FlushSwarmLifecycle + RestoreManifest so
// turn N+1's SetSwarmContext observes a fully-restored engine. If the
// release ran first, turn N+1 could SetSwarmContext while the prior
// turn's RestoreManifest is still in flight, re-introducing the S2
// race. The spec at "Swarm lifecycle handshake across consecutive
// POSTs" pins this exact ordering via the event log.
//
// Expected:
//   - swarmActive controls whether flush + restore fire. Non-swarm
//     turns still take the wrap (so the gate release waits on broker
//     drain) but skip the lifecycle methods.
//   - releaseGate is the closure from acquireSessionLifecycleGate's
//     caller; the wrap goroutine ALWAYS calls it exactly once, whether
//     swarmActive is true or false.
//
// Side effects:
//   - Starts one goroutine that ranges over src and runs flush + restore
//     + gate-release after the range exits.
func (d *Dispatcher) wrapWithSwarmLifecycle(
	ctx context.Context,
	src <-chan provider.StreamChunk,
	manifestSnapshot any,
	swarmActive bool,
	releaseGate func(),
) <-chan provider.StreamChunk {
	out := make(chan provider.StreamChunk, 64)
	go func() {
		// LIFO defer order — innermost runs FIRST. Reading top-to-bottom:
		// the gate is released AFTER flush + restore, so turn N+1's
		// SetSwarmContext can never land mid-lifecycle.
		defer close(out)
		defer releaseGate()
		if swarmActive {
			defer d.dispatchEngine.RestoreManifest(manifestSnapshot)
			defer func() {
				// Flush errors are intentionally swallowed — the post-
				// swarm gates' own observers (event bus, logs) surface
				// failures; the SSE-side consumer cannot act on a flush
				// error anyway. RestoreManifest is no-error.
				_ = d.dispatchEngine.FlushSwarmLifecycle(ctx)
			}()
		}
		for chunk := range src {
			out <- chunk
		}
	}()
	return out
}

// fanOutSessionedChunks routes the post-lifecycle chunks channel to all
// live subscribers of the sessioned dispatch: the broker (existing SSE
// path) and the consumer (Phase 4 WS direct tee).
//
// Combinations:
//   - broker non-nil, consumer nil   — broker.Publish in a goroutine
//     (legacy Phase 2 behaviour).
//   - broker nil, consumer non-nil   — single goroutine ranges over
//     chunks and routes each chunk through the consumer interface
//     (WriteChunk / WriteError / Done). Bare-engine test surfaces and
//     WS-only deployments take this path.
//   - broker non-nil, consumer non-nil — single goroutine ranges over
//     chunks; each chunk fans BOTH to a buffered re-channel feeding
//     broker.Publish AND to the consumer. The broker re-channel is
//     buffered (capacity 64) so a slow subscriber cannot back-pressure
//     the consumer (and vice versa). The goroutine closes the broker
//     re-channel after src drains so broker.Publish's terminal close
//     fires under its existing invariants.
//   - broker nil, consumer nil       — chunks drain to a sink so the
//     streamer goroutine terminates. Matches the pre-Phase-2 server.go
//     nil-broker fallback.
//
// Why route content chunks through StreamConsumer.WriteChunk for the
// consumer-only path: WSStreamConsumer's WriteChunk implementation
// (internal/api/websocket.go) maps the content to a WSChunkMsg with the
// content field set and delivers it through the out channel, preserving
// the existing forwardWSChunks shape. Done chunks call consumer.Done()
// so the WS client receives a final {done: true} frame, mirroring the
// pre-fix BuildWSChunkMsg behaviour for chunk.Done. Error chunks call
// consumer.WriteError so the WS surface still distinguishes critical-
// vs-transient via the existing BuildWSChunkMsg severity gate at the
// consumer seam.
//
// Side effects:
//   - Spawns one goroutine that consumes src to completion.
func (d *Dispatcher) fanOutSessionedChunks(
	sessionID string,
	src <-chan provider.StreamChunk,
	consumer streaming.StreamConsumer,
) {
	switch {
	case d.sessionBroker != nil && consumer == nil:
		go d.sessionBroker.Publish(sessionID, src)
	case d.sessionBroker == nil && consumer != nil:
		go d.driveConsumer(src, consumer)
	case d.sessionBroker != nil && consumer != nil:
		// Tee: re-channel feeds broker; same goroutine drives consumer.
		brokerCh := make(chan provider.StreamChunk, 64)
		go d.sessionBroker.Publish(sessionID, brokerCh)
		go func() {
			defer close(brokerCh)
			for chunk := range src {
				// Broker first — the broker delivers with a bounded
				// grace period; the consumer is the WS handler's local
				// out channel and is essentially non-blocking with a
				// 128-slot buffer (websocket.go:125).
				brokerCh <- chunk
				d.deliverChunkToConsumer(chunk, consumer)
			}
		}()
	default:
		// No broker, no consumer — drain to sink so the streamer
		// goroutine terminates. Matches the pre-Phase-2 server.go
		// nil-broker fallback (server.go pre-deletion :1244-1248).
		go func() {
			for range src {
			}
		}()
	}
}

// driveConsumer ranges over src and delivers each chunk through the
// streaming.StreamConsumer interface. Pulled out for symmetry with the
// broker+consumer tee path and to keep fanOutSessionedChunks readable.
//
// Side effects:
//   - Reads src to completion.
//   - Calls consumer.WriteChunk / WriteError / Done for each chunk.
func (d *Dispatcher) driveConsumer(src <-chan provider.StreamChunk, consumer streaming.StreamConsumer) {
	for chunk := range src {
		d.deliverChunkToConsumer(chunk, consumer)
	}
}

// deliverChunkToConsumer routes a single chunk through the
// StreamConsumer surface AND the optional consumer interfaces
// (DelegationConsumer, ToolCallConsumer, ToolResultConsumer,
// HarnessEventConsumer, EventConsumer). The dispatch order mirrors
// streaming.Run (internal/streaming/runner.go:36-55) so the WS surface
// observes the same chunk → frame mapping as the live engine driver
// does. Pre-Phase-4 the WS handler's BuildWSChunkMsg surfaced these
// signals from the chunk itself; centralising the dispatch here lets
// the WS handler keep its existing out-channel pump without re-
// implementing every chunk-type case.
//
// Side effects:
//   - One or more Write* calls on consumer depending on chunk shape.
func (d *Dispatcher) deliverChunkToConsumer(chunk provider.StreamChunk, consumer streaming.StreamConsumer) {
	if chunk.Error != nil {
		consumer.WriteError(chunk.Error)
		// Errors do NOT short-circuit the rest of the dispatch — a
		// chunk can legally carry Error + Done (engine.go:3796 builds
		// exactly that shape on ctx cancel). Falling through preserves
		// the Done signal so the WS client receives a terminal frame.
	}
	if streaming.DispatchHarnessEvent(consumer, chunk) {
		// Harness / typed event chunks deliberately do NOT emit a
		// content frame even when chunk.Content is non-empty — see
		// streaming.IsControlEvent for the rationale (structured
		// metadata vs natural-language assistant text).
		if chunk.Done {
			consumer.Done()
		}
		return
	}
	if streaming.DeliverDelegationEvent(consumer, chunk.DelegationInfo) {
		if chunk.Done {
			consumer.Done()
		}
		return
	}
	streaming.DeliverToolCall(consumer, chunk.ToolCall)
	streaming.DeliverToolResult(consumer, chunk.ToolResult)
	if chunk.Content != "" {
		_ = consumer.WriteChunk(chunk.Content)
	}
	if chunk.Done {
		consumer.Done()
	}
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
