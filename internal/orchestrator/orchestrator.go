// Package orchestrator provides the canonical user-input → event-stream
// pipeline shared by every FlowState access method (CLI, API, TUI).
//
// Per ADR - Multi-Access Method Architecture (ADR-001) and ADR -
// Session Orchestrator for Surface Parity, surfaces are thin wrappers
// over Orchestrator.ProcessUserInput; they own only their I/O
// adapter (StreamConsumer implementation), never the dispatch
// lifecycle. Lives in its own package so the api/ and tui/ trees
// can import it without forcing import cycles through internal/app.
package orchestrator

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
)

// Orchestrator is the canonical user-input → event-stream
// pipeline shared by CLI, API, and TUI surfaces.
//
// Per ADR - Multi-Access Method Architecture (ADR-001) §"Five
// Principles", access methods MUST be thin wrappers over `internal/`
// services with no business logic. The Orchestrator is the
// service that owns @-mention resolution, swarm dispatch lifecycle,
// and stream event delivery for any user-input → response interaction.
//
// Per ADR - Session Orchestrator for Surface Parity (the follow-up
// ADR that extends ADR-001 to the input pipeline), every surface
// that processes a user message routes through ProcessUserInput.
// Surface-specific I/O is adapted via the streaming.StreamConsumer
// interface — surfaces own their consumer (WriterConsumer, SSEConsumer,
// channel-pump consumer for the TUI's Bubble Tea event loop) but
// never reimplement the dispatch lifecycle.
//
// Internally the orchestrator delegates to swarm.DispatchSwarm so
// CLI and TUI cannot diverge on snapshot/restore-around-stream
// semantics — the architectural drift that produced multiple
// recurring bug classes (TUI persistent identity swap, child
// session events.jsonl gaps, manifest leak across turns).
type Orchestrator struct {
	// dispatchEngine is the streaming-half engine surface — narrow on
	// purpose so test fakes that exercise only ProcessUserInput / Stream
	// can satisfy it without growing the lifecycle setters.
	dispatchEngine swarm.DispatchEngine
	// engine is the wider lifecycle-half engine surface used by
	// SwitchAgent / SwitchModel / LoadSession / NewSession. Production
	// wires the same *engine.Engine into both fields; tests that only
	// exercise the streaming path can leave engine nil and rely on the
	// per-method nil-tolerance.
	engine         Engine
	agentRegistry  *agent.Registry
	swarmRegistry  *swarm.Registry
	streamer       streaming.Streamer
	sessionStore   SessionStore
	sessionManager SessionManager
}

// UserInput is the surface-agnostic input to ProcessUserInput.
//
// Surface conventions:
//
//   - **CLI** (`flowstate run --agent <id>`, `flowstate chat
//     --message --agent <id>`): DefaultAgent = `--agent` flag,
//     ScanMentions = false. CLI callers route explicitly via the
//     flag and the message body is treated as a plain prompt.
//   - **API** (`POST /api/chat`): DefaultAgent = body's `agent_id`,
//     ScanMentions = false. API callers also route explicitly.
//   - **TUI** (chat input): DefaultAgent = the chat's persistent
//     `agentID` (typically the boot default), ScanMentions = true.
//     Chat scans the input for `@<id>` mentions; the first one that
//     resolves to a swarm wins, otherwise the message goes to
//     DefaultAgent.
//
// The orchestrator never mutates DefaultAgent — surfaces own their
// own persistent identity (e.g. the TUI's `i.agentID` is preserved
// across swarm dispatches per Phase 2 of ADR - Swarm Dispatch
// Across Access Methods).
type UserInput struct {
	// Message is the user's raw input text.
	Message string
	// DefaultAgent is the surface's baseline agent id. Used when
	// ScanMentions is false, or when ScanMentions is true but no
	// @-mention resolves to a swarm.
	DefaultAgent string
	// ScanMentions enables @-mention scanning of Message. When true,
	// the first @-mention that resolves to a swarm overrides
	// DefaultAgent for this call only. Agent @-mentions and unknown
	// @-mentions fall through to DefaultAgent.
	ScanMentions bool
}

// errNoTarget fires when ProcessUserInput is called without a usable
// DefaultAgent and ScanMentions is false (or set but matched no
// mention). Surfaces SHOULD validate their input shape before calling
// the orchestrator; this error is a defensive guard.
var errNoTarget = errors.New("session orchestrator: no agent or swarm target resolved from input")

// New wires the orchestrator's dependencies. The streaming-half
// fields are required for production use; the lifecycle-half fields
// (sessionStore, sessionManager) are nullable so test fixtures and
// minimal compositions can skip them.
//
// Expected:
//   - eng is a non-nil Engine (typically *engine.Engine). May be nil
//     in test-minimal compositions; lifecycle methods guard against
//     nil per the same nil-tolerance pattern Stream uses today
//     (orchestrator.go:200-209,238).
//   - agentReg is the agent registry (may be nil; resolution falls
//     through to swarm-only matching when nil).
//   - swarmReg is the swarm registry (may be nil; orchestrator then
//     short-circuits dispatch to a plain agent stream).
//   - streamer drives the underlying provider stream (typically the
//     same *engine.Engine via the Streamer interface).
//   - sessionStore is the session-persistence surface used by
//     LoadSession / SaveTurnEnd. May be nil — those methods then
//     return errStoreNotConfigured.
//   - sessionManager is the per-session metadata manager used by
//     SwitchAgent / SwitchModel. May be nil — those methods then
//     skip the manager call and only mutate the engine.
//
// Returns:
//   - A configured *Orchestrator.
//
// Side effects:
//   - None.
func New(
	eng swarm.DispatchEngine,
	agentReg *agent.Registry,
	swarmReg *swarm.Registry,
	streamer streaming.Streamer,
	sessionStore SessionStore,
	sessionManager SessionManager,
) *Orchestrator {
	o := &Orchestrator{
		dispatchEngine: eng,
		agentRegistry:  agentReg,
		swarmRegistry:  swarmReg,
		streamer:       streamer,
		sessionStore:   sessionStore,
		sessionManager: sessionManager,
	}
	// Auto-narrow when the dispatch engine also satisfies the wider
	// lifecycle surface. *engine.Engine satisfies both in production;
	// fake DispatchEngines used by streaming-only tests do not — those
	// tests rely on per-method nil-tolerance.
	if wider, ok := eng.(Engine); ok {
		o.engine = wider
	}
	return o
}

// ProcessUserInput is the canonical entry point for "a user/client
// sent input that should produce a streamed response". CLI, API, and
// TUI all route here; behaviour is identical across surfaces because
// the orchestrator drives swarm.DispatchSwarm internally — same
// resolver, same dispatch lifecycle (snapshot → SetSwarmContext →
// stream → flush → restore), same event delivery shape via the
// supplied consumer.
//
// Expected:
//   - ctx is a valid context controlling the streamed run.
//   - req carries the message and routing intent (DefaultAgent +
//     optional ScanMentions).
//   - consumer is the surface-specific event sink. CLI uses
//     WriterConsumer/JSONConsumer; API uses SSEConsumer; TUI uses
//     a channel-pump consumer that adapts to its Bubble Tea loop.
//
// Returns:
//   - errNoTarget when neither DefaultAgent nor a scanned @-mention
//     resolves to a known agent or swarm.
//   - The wrapped error from swarm.DispatchSwarm on stream/flush
//     failure.
//   - nil on success.
//
// Side effects:
//   - Drives swarm.DispatchSwarm — see that function for the full
//     side-effect list (manifest snapshot/restore, swarm context
//     install, post-flush).
func (o *Orchestrator) ProcessUserInput(
	ctx context.Context,
	req UserInput,
	consumer streaming.StreamConsumer,
) error {
	leadID, swarmCtx, err := o.resolve(req)
	if err != nil {
		return err
	}
	return swarm.DispatchSwarm(ctx, o.dispatchEngine, swarmCtx, o.streamer, consumer, leadID, req.Message)
}

// Stream is the async cousin of ProcessUserInput. Returns a channel
// of provider.StreamChunk values (the same shape the TUI's existing
// readNextChunkFrom expects) plus an error if resolution or the
// initial Stream call fails. Lifecycle (manifest snapshot →
// SetSwarmContext → stream → flush → RestoreManifest) is wrapped
// around the chunk-channel consumption inside an internal goroutine
// so callers see only "request → channel of chunks" and the engine
// state restores symmetrically when the channel closes.
//
// Use this from event-loop surfaces (the TUI's Bubble Tea Cmd) where
// blocking on a synchronous Consumer is incompatible with the
// surface's async model. CLI/API call ProcessUserInput instead —
// they're synchronous and the Consumer pattern fits naturally.
//
// Expected:
//   - ctx is a valid context controlling the streamed run.
//   - req carries the message and routing intent.
//
// Returns:
//   - A channel that emits provider.StreamChunk values until the
//     underlying stream completes, then closes. Never nil on a
//     successful return.
//   - An error from resolve() (errNoTarget / NotFoundError) or from
//     the initial streamer.Stream call. When non-nil, the chan
//     return is nil and lifecycle side-effects have been rolled back.
//
// Side effects:
//   - When a target resolves, calls eng.SetSwarmContext(swarmCtx)
//     immediately and schedules eng.FlushSwarmLifecycle +
//     eng.RestoreManifest to run when the chunk channel drains.
//   - Drives streamer.Stream(ctx, leadID, message) once.
func (o *Orchestrator) Stream(ctx context.Context, req UserInput) (<-chan provider.StreamChunk, error) {
	leadID, swarmCtx, err := o.resolve(req)
	if err != nil {
		return nil, err
	}

	var snapshot any
	var prevSkipFiles bool
	if o.dispatchEngine != nil {
		snapshot = o.dispatchEngine.ManifestSnapshot()
		prevSkipFiles = o.dispatchEngine.SkipAgentFiles()
		o.dispatchEngine.SetSwarmContext(swarmCtx)
		o.dispatchEngine.SetSkipAgentFiles(true)
	}

	src, err := o.streamer.Stream(ctx, leadID, req.Message)
	if err != nil {
		if o.dispatchEngine != nil {
			o.dispatchEngine.RestoreManifest(snapshot)
			o.dispatchEngine.SetSkipAgentFiles(prevSkipFiles)
		}
		return nil, err
	}

	out := make(chan provider.StreamChunk, 1)
	go func() {
		defer close(out)
		// Forward every chunk verbatim — surfaces decode tool
		// calls, delegation info, etc. from the StreamChunk just
		// like they did when calling streamer.Stream directly.
		for chunk := range src {
			select {
			case out <- chunk:
			case <-ctx.Done():
				// Drain the source so the producer goroutine
				// doesn't park on a full chan we'll never read,
				// then break to the cleanup phase.
				go func() {
					for range src {
					}
				}()
				goto cleanup
			}
		}
	cleanup:
		if o.dispatchEngine != nil {
			_ = o.dispatchEngine.FlushSwarmLifecycle(ctx)
			o.dispatchEngine.RestoreManifest(snapshot)
			o.dispatchEngine.SetSkipAgentFiles(prevSkipFiles)
		}
	}()
	return out, nil
}

// resolve picks the target agent or swarm based on the supplied
// UserInput. ScanMentions=true causes a left-to-right scan of
// req.Message for @-mentions; the first one that resolves to a swarm
// wins. Agent @-mentions and unknown @-mentions are skipped and the
// resolver falls through to DefaultAgent.
//
// Expected:
//   - req is the orchestrator's input.
//
// Returns:
//   - leadID, swarmCtx as defined by swarm.ResolveTarget.
//   - errNoTarget when DefaultAgent is empty AND no scanned mention
//     resolved to a swarm.
//
// Side effects:
//   - None.
func (o *Orchestrator) resolve(req UserInput) (string, *swarm.Context, error) {
	hasAgent := o.agentLookup()

	if req.ScanMentions {
		for _, mention := range swarm.ExtractAtMentions(req.Message) {
			leadID, swarmCtx, err := swarm.ResolveTarget(hasAgent, o.swarmRegistry, mention)
			if err == nil && swarmCtx != nil {
				return leadID, swarmCtx, nil
			}
		}
	}

	if req.DefaultAgent == "" {
		return "", nil, errNoTarget
	}
	return swarm.ResolveTarget(hasAgent, o.swarmRegistry, req.DefaultAgent)
}

// IsSwarmMention reports whether message contains an @-mention that
// resolves to a registered swarm (not just any agent). Useful for
// surfaces that need to discriminate "swarm dispatch turn" vs "normal
// agent turn" before deciding which streaming path to use — the TUI
// in particular needs this so it can keep its session-manager path
// for normal chat while routing swarm dispatches through Stream.
//
// Returns false when the orchestrator's swarmRegistry is nil
// (orchestrator never resolves a swarm in that state).
//
// Expected:
//   - message is the raw user input.
//
// Returns:
//   - true when at least one @-mention in message resolves to a
//     swarm via swarm.Resolve.
//   - false otherwise — including when the message contains
//     @-mentions that resolve to plain agents (those go through
//     the surface's normal agent-chat path).
//
// Side effects:
//   - None.
func (o *Orchestrator) IsSwarmMention(message string) bool {
	if o.swarmRegistry == nil {
		return false
	}
	hasAgent := o.agentLookup()
	for _, mention := range swarm.ExtractAtMentions(message) {
		kind, _ := swarm.Resolve(mention, hasAgent, o.swarmRegistry)
		if kind == swarm.KindSwarm {
			return true
		}
	}
	return false
}

// agentLookup returns a swarm.HasAgent closure backed by the
// orchestrator's agentRegistry, or nil when the registry is unset.
// nil propagates through swarm.ResolveTarget which treats it as the
// historical "bare engine" pass-through case (returns id verbatim
// with nil swarmCtx).
//
// Side effects:
//   - None.
func (o *Orchestrator) agentLookup() swarm.HasAgent {
	if o.agentRegistry == nil {
		return nil
	}
	return func(name string) bool {
		if _, ok := o.agentRegistry.Get(name); ok {
			return true
		}
		_, ok := o.agentRegistry.GetByNameOrAlias(name)
		return ok
	}
}

// SwitchAgent installs a new agent manifest on the engine and updates
// the session's CurrentAgentID via the session manager. Returns the
// resolved manifest so callers can sync surface state (status bar,
// SSE response payload) without re-resolving.
//
// Per ADR - Session Orchestrator for Surface Parity, this method is
// the single owner of agent-switch lifecycle across CLI, TUI, and
// API. Pre-lift the TUI's applyAgentSwitch and the API's
// handleUpdateSessionAgent each composed engine + session-manager
// calls themselves; post-lift they both route through here so the
// engine half cannot drift from the session-metadata half.
//
// Expected:
//   - sessionID identifies an existing session, or "" for a
//     session-less switch (skips the session-manager call).
//   - agentID is a registry id or alias; resolved via
//     agent.Registry.GetByNameOrAlias.
//
// Returns:
//   - The resolved *agent.Manifest on success.
//   - errAgentNotFound when the registry yields nothing.
//
// Side effects:
//   - Calls engine.SetManifest with the resolved manifest.
//   - Calls sessionManager.UpdateSessionAgent when sessionID is
//     non-empty AND a session manager is wired.
func (o *Orchestrator) SwitchAgent(_ context.Context, sessionID, agentID string) (*agent.Manifest, error) {
	if o.agentRegistry == nil {
		return nil, errAgentNotFound
	}
	manifest, ok := o.agentRegistry.GetByNameOrAlias(agentID)
	if !ok {
		manifest, ok = o.agentRegistry.Get(agentID)
	}
	if !ok || manifest == nil {
		return nil, errAgentNotFound
	}
	if o.engine != nil {
		o.engine.SetManifest(*manifest)
	}
	if o.sessionManager != nil && sessionID != "" {
		if err := o.sessionManager.UpdateSessionAgent(sessionID, manifest.ID); err != nil {
			return manifest, err
		}
	}
	return manifest, nil
}

// SwitchModel installs a new provider/model preference on the engine
// and updates the session's CurrentProviderID/CurrentModelID via the
// session manager. Mirror of SwitchAgent for the model preference.
//
// Phase-5 Slice α — model-switch compaction trigger: BEFORE the
// preference swings, the orchestrator asks the engine whether the
// persisted history would saturate the new model's window
// (MaybeCompactForModel). If so, the trigger force-fires the auto-
// compactor on the still-active engine state. Without this, switching
// from a 200K-window model to a 32K-window model in mid-conversation
// caused the next Stream call to refuse at the proactive overflow
// gate with no auto-recovery — operators saw the saturation but had
// no remediation path other than starting a fresh session.
//
// Order matters: compaction MUST run before SetModelPreference. The
// trigger inspects the persisted history against the destination
// model's window via ResolveContextLength; if SetModelPreference ran
// first, the engine would resolve limits against the new model
// regardless of which orchestrator call is in flight, masking the
// "would the next request refuse?" check that motivates firing.
//
// Expected:
//   - sessionID identifies an existing session, or "" for a
//     session-less switch (the compaction trigger is a no-op then —
//     it is session-scoped).
//   - providerName and modelName identify the target preference;
//     no validation against a registry is performed here — the
//     engine accepts the preference verbatim and resolves on next
//     stream call.
//
// Returns:
//   - nil on success.
//   - The session-manager error when UpdateSessionModel fails.
//
// Side effects:
//   - Calls engine.MaybeCompactForModel BEFORE SetModelPreference
//     when sessionID is non-empty AND an engine is wired. May
//     produce one summariser LLM call and a ContextCompactedEvent
//     bus emission.
//   - Calls engine.SetModelPreference with the supplied pair.
//   - Calls sessionManager.UpdateSessionModel when sessionID is
//     non-empty AND a session manager is wired.
func (o *Orchestrator) SwitchModel(ctx context.Context, sessionID, providerName, modelName string) error {
	if o.engine != nil {
		// Phase-5 Slice α — fire the model-switch compaction trigger
		// BEFORE the preference swings. Session-less switches skip
		// the trigger (it has nothing session-scoped to drive).
		if sessionID != "" {
			o.engine.MaybeCompactForModel(ctx, sessionID, providerName, modelName)
		}
		o.engine.SetModelPreference(providerName, modelName)
	}
	if o.sessionManager != nil && sessionID != "" {
		return o.sessionManager.UpdateSessionModel(sessionID, providerName, modelName)
	}
	return nil
}

// LoadSession rehydrates a session: loads the context store from the
// session store, restores swarm events from the persisted WAL when
// the store satisfies SwarmEventPersister, installs the store on the
// engine, and returns a fully-populated LoadedSession bundle for the
// surface to render.
//
// Per ADR - Session Orchestrator for Surface Parity §LoadSession,
// the orchestrator owns the load + engine install; surfaces own the
// per-store apply for the swarm-event slice (TUI's i.swarmStore is
// intent-scoped, web's will be SSE-connection-scoped).
//
// Expected:
//   - sessionID is the persisted session id.
//
// Returns:
//   - *LoadedSession with Store, Metadata (zero-value today; populated
//     once the SessionStore exposes a Load shape that returns
//     metadata), and SwarmEvents populated.
//   - errStoreNotConfigured when no SessionStore is wired.
//   - errSessionNotFound when Load yields a nil store.
//   - The wrapped error from sessionStore.Load on read failure.
//
// Side effects:
//   - Calls engine.SetContextStore(store, sessionID) on success.
//   - Reads the persisted WAL via SwarmEventPersister.LoadEvents
//     when supported. WAL read errors are swallowed (matches the
//     TUI's existing fallback at intent.go:resetAndRestoreSwarmEvents).
func (o *Orchestrator) LoadSession(_ context.Context, sessionID string) (*LoadedSession, error) {
	if o.sessionStore == nil {
		return nil, errStoreNotConfigured
	}
	store, err := o.sessionStore.Load(sessionID)
	if err != nil {
		return nil, err
	}
	if store == nil {
		return nil, errSessionNotFound
	}
	if o.engine != nil {
		o.engine.SetContextStore(store, sessionID)
	}
	loaded := &LoadedSession{
		Store: store,
	}
	if ep, ok := o.sessionStore.(SwarmEventPersister); ok {
		// Mirror intent.go:resetAndRestoreSwarmEvents — a WAL read
		// error is non-fatal; the surface continues with no events.
		if events, evErr := ep.LoadEvents(sessionID); evErr == nil {
			loaded.SwarmEvents = events
		}
	}
	return loaded, nil
}

// NewSession generates a fresh session id (UUID v4) and installs an
// empty context store on the engine. Mirrors the TUI's
// createNewSession (intent.go:createNewSession) so the engine state
// flips together with the new id.
//
// Returns:
//   - The generated session id.
//   - nil error today; reserved for future failure modes.
//
// Side effects:
//   - Calls engine.SetContextStore with a new empty store and the
//     generated id.
func (o *Orchestrator) NewSession(_ context.Context) (string, error) {
	sessionID := uuid.New().String()
	if o.engine != nil {
		o.engine.SetContextStore(recall.NewEmptyContextStore(""), sessionID)
	}
	return sessionID, nil
}

// SaveTurnEnd persists the session and its swarm events at end of
// turn. The snapshot is built by the caller — surfaces read engine
// state on their own goroutine (the TUI's Bubble Tea loop, the CLI's
// main goroutine) where ownership of concurrent reads is unambiguous;
// the orchestrator accepts the pre-built TurnSnapshot and performs
// only the persistence side-effects.
//
// Per ADR - Session Orchestrator for Surface Parity §SaveTurnEnd,
// SaveEvents errors are non-fatal — event loss is tolerable, but a
// failed Save is not. This mirrors the TUI's existing asymmetry at
// intent.go:saveSession.
//
// Expected:
//   - sessionID is non-empty.
//   - snapshot.Store is non-nil; otherwise the call is a no-op.
//
// Returns:
//   - nil on success.
//   - errStoreNotConfigured when no SessionStore is wired.
//   - The wrapped error from sessionStore.Save on persistence
//     failure (this IS fatal for the caller).
//
// Side effects:
//   - Calls sessionStore.Save with the snapshot's store + metadata.
//   - When the store satisfies SwarmEventPersister AND the snapshot
//     carries non-empty SwarmEvents, calls SaveEvents (best-effort;
//     errors logged silently as in the TUI's existing path).
func (o *Orchestrator) SaveTurnEnd(_ context.Context, sessionID string, snapshot TurnSnapshot) error {
	if o.sessionStore == nil {
		return errStoreNotConfigured
	}
	if snapshot.Store == nil {
		return nil
	}
	meta := contextMetadataFromSnapshot(snapshot)
	if err := o.sessionStore.Save(sessionID, snapshot.Store, meta); err != nil {
		return err
	}
	if ep, ok := o.sessionStore.(SwarmEventPersister); ok && len(snapshot.SwarmEvents) > 0 {
		// Match intent.go:saveSession's intentional swallow — event
		// persistence is best-effort; a SaveEvents failure must not
		// fail the caller's turn-end signal.
		_ = ep.SaveEvents(sessionID, snapshot.SwarmEvents)
	}
	return nil
}

// errors exported for tests and callers that need to discriminate
// orchestrator-side failures from underlying store / manager errors.
//
// ErrAgentNotFound mirrors errAgentNotFound. ErrSessionNotFound mirrors
// errSessionNotFound. ErrStoreNotConfigured mirrors errStoreNotConfigured.
var (
	ErrAgentNotFound      = errAgentNotFound
	ErrSessionNotFound    = errSessionNotFound
	ErrStoreNotConfigured = errStoreNotConfigured
)

// errorsAlias lets the package satisfy errors.Is checks without exposing
// the underlying sentinels via package-level value redeclaration —
// keeps the Go vet "tag mismatch" checker quiet on the explicit aliases.
var _ = errors.Is
