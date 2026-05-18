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

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
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
type Dispatcher struct {
	streamer       Streamer
	dispatchEngine swarm.DispatchEngine
	swarmRegistry  *swarm.Registry
	agentRegistry  *agent.Registry
	sessionManager SessionManager
	sessionBroker  SessionBroker
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
	return &Dispatcher{
		streamer:       streamer,
		dispatchEngine: dispatchEngine,
		swarmRegistry:  swarmRegistry,
		agentRegistry:  agentRegistry,
		sessionManager: sessionManager,
		sessionBroker:  sessionBroker,
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
// Swarm-lifecycle flush note (Phase 2 vs Phase 3 boundary):
// the FlushSwarmLifecycle + RestoreManifest pair STAYS racing handler
// return for this commit. The wrap goroutine is owned by Dispatcher
// (moved out of the deleted wrapWithSwarmLifecycle helper), but there
// is NO per-session handshake gating turn N+1 on turn N's flush — that
// is the load-bearing Phase 3 change. The pre-Phase-2 race surface (S2
// in the v6 audit) is preserved verbatim for one commit.
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
	_ streaming.StreamConsumer,
) (SessionedHandle, error) {
	if d.sessionManager == nil {
		return SessionedHandle{}, errors.New("dispatch: sessionManager not configured")
	}

	// Decouple the streamer's lifetime from the caller's ctx. Pattern
	// imported from commit 51fb416c (originally applied at the
	// /messages handler boundary); centralising it inside Dispatcher
	// means every sessioned caller inherits the fix without needing to
	// re-apply context.WithoutCancel at the handler edge.
	streamCtx := context.WithoutCancel(ctx)

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
		return SessionedHandle{}, snapErr
	}

	if chunks != nil {
		// Wrap chunks with the swarm lifecycle goroutine when active.
		// Phase 2 preserves the existing race surface (S2): the
		// FlushSwarmLifecycle + RestoreManifest goroutine runs AFTER
		// the chunks channel drains, with NO per-session handshake
		// gating turn N+1 on turn N's flush. Phase 3 of the v6 plan
		// closes that race by keying a sync.Map[sessionID] gate on
		// req.SessionID; this commit explicitly defers that.
		if swarmActive {
			chunks = d.wrapWithSwarmLifecycle(streamCtx, chunks, manifestSnapshot)
		}
		if d.sessionBroker != nil {
			go d.sessionBroker.Publish(req.SessionID, chunks)
		} else {
			// No broker — drain into a sink so the streamer goroutine
			// terminates. Matches the pre-Phase-2 handler's nil-broker
			// fallback (server.go pre-deletion :1244-1248).
			go func() {
				for range chunks {
				}
			}()
		}
	} else if swarmActive {
		// Nil chunks channel + swarm active — manager returned cleanly
		// without driving the streamer. Restore the manifest synchronously
		// so engine state doesn't leak.
		d.dispatchEngine.RestoreManifest(manifestSnapshot)
	}

	return SessionedHandle{Snapshot: snap}, nil
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
// (FlushSwarmLifecycle + RestoreManifest) so the engine ends in the
// same shape Orchestrator.Stream leaves it: swarm context is sticky
// (the next dispatch overwrites it) but the manifest reverts to the
// pre-dispatch identity so non-swarm turns don't inherit the swarm
// lead's persona. Mirrors the deleted server.go::wrapWithSwarmLifecycle
// at server.go:1409-1431 (pre-Phase-2). Moved INTO Dispatcher per Phase
// 2 of the v6 plan; Phase 3 reworks this into a handshake-gated
// surface.
//
// Side effects:
//   - Starts one goroutine that ranges over src and runs
//     FlushSwarmLifecycle + RestoreManifest after the range exits.
func (d *Dispatcher) wrapWithSwarmLifecycle(
	ctx context.Context,
	src <-chan provider.StreamChunk,
	manifestSnapshot any,
) <-chan provider.StreamChunk {
	out := make(chan provider.StreamChunk, 64)
	go func() {
		defer close(out)
		defer func() {
			// Flush + restore even if the producer panics or src is
			// drained early. Flush errors are intentionally swallowed
			// — the post-swarm gates' own observers (event bus, logs)
			// surface failures; the SSE-side consumer cannot act on a
			// flush error anyway. RestoreManifest is no-error.
			_ = d.dispatchEngine.FlushSwarmLifecycle(ctx)
			d.dispatchEngine.RestoreManifest(manifestSnapshot)
		}()
		for chunk := range src {
			out <- chunk
		}
	}()
	return out
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
