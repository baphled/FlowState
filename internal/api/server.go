package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/auth"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/discovery"
	"github.com/baphled/flowstate/internal/dispatch"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/orchestrator"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/quota"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
	todo "github.com/baphled/flowstate/internal/tool/todo"
	"github.com/baphled/flowstate/internal/turn"
)

const (
	errSessionManagerNotConfigured    = `{"error":"session manager not configured"}`
	errBackgroundManagerNotConfigured = `{"error":"background manager not configured"}`
)

// Streamer abstracts the streaming producer for chat responses.
type Streamer interface {
	// Stream returns a channel of response chunks for the given agent and message.
	Stream(ctx context.Context, agentID string, message string) (<-chan provider.StreamChunk, error)
}

// Server provides HTTP endpoints for the FlowState platform.
type Server struct {
	streamer               Streamer
	registry               *agent.Registry
	swarmRegistry          *swarm.Registry
	dispatchEngine         swarm.DispatchEngine
	discovery              *discovery.AgentDiscovery
	skills                 []skill.Skill
	sessions               *ctxstore.FileSessionStore
	sessionManager         *session.Manager
	todoStore              todo.Store
	backgroundManager      *engine.BackgroundTaskManager
	completionOrchestrator *engine.CompletionOrchestrator
	eventBus               *eventbus.EventBus
	metricsHandler         http.Handler
	modelLister            ModelLister
	contextUsageProvider   ContextUsageProvider
	compactionController   CompactionController
	mux                    *http.ServeMux
	// originPatterns is the glob allowlist applied to PR3's RequireOrigin
	// HTTP middleware. Centralised in PR1/C1 per the API Auth Track plan
	// §"Session Store Interface" → "Rollout Plan" — see
	// internal/auth.OriginConfig.AllowedOrigins for the type the auth
	// package consumes. Empty defaults to ["localhost:*"]. Phase 4 /
	// Commit 2 (Turn-Based Post-Then-Poll, May 2026) retired the
	// WebSocket handshake that originally consumed this allowlist
	// alongside the auth middleware; the allowlist now exclusively
	// gates HTTP requests.
	originPatterns []string
	// auth wires the optional PR3 auth track (Origin / Session / CSRF
	// middleware composition). Installed via WithAuth(bundle); when
	// auth.Auth.Enabled is false (the PR3 ship-state default) the
	// register* helpers no-op and routes register plain on the mux.
	auth AuthBundle

	// quotaAggregator backs GET /api/v1/providers/quota +
	// POST /api/v1/providers/quota/reset (PR5 of the Provider Quota
	// and Spend Visibility plan, May 2026). Wired via
	// WithQuotaAggregator(...) from the cmd/serve boot path. Nil
	// makes both endpoints return 501 so the SPA distinguishes
	// "feature not wired" from "no providers configured".
	quotaAggregator QuotaAggregator

	// dispatcher is the unified "user-input → engine-stream" service
	// per the "Dispatcher Service Unification (May 2026)" plan
	// (FlowState vault). Phase 1 routes /api/chat through
	// dispatcher.DispatchEphemeral; Phase 2 will fold /messages;
	// Phase 4 the WS handler. Wired via WithDispatcher OR auto-
	// constructed in NewServer when streamer + swarmRegistry +
	// dispatchEngine are all present, so production wiring through
	// internal/app gets the unified path by default while
	// older test surfaces that skip those options fall back to the
	// pre-Dispatcher orchestrator path.
	dispatcher DispatcherService
}

// DispatcherService is the narrow surface the Dispatcher Service
// Unification plan exposes to the API package. Declared as an
// interface locally so production wires *dispatch.Dispatcher while
// tests can substitute a spy that records the call without spinning
// up the streamer chain.
//
// Per the v6 plan, handleChat routes through DispatchEphemeral (Phase
// 1, commit 49cf9fb7) and handleSessionMessage routes through
// DispatchSessioned (Phase 2, this commit). Phase 4 extends the WS
// handler through DispatchSessioned as well.
type DispatcherService interface {
	DispatchEphemeral(ctx context.Context, req dispatch.DispatchRequest, consumer streaming.StreamConsumer) (dispatch.EphemeralHandle, error)
	DispatchSessioned(ctx context.Context, req dispatch.DispatchRequest, consumer streaming.StreamConsumer) (dispatch.SessionedHandle, error)
	// TurnRegistry exposes the in-memory Turn store the Dispatcher writes
	// into during DispatchSessioned. Phase 2 of "Turn-Based Post-Then-Poll
	// Architecture (May 2026)" reads from this registry to serve
	// GET /api/v1/sessions/{id}/turns/{turn_id}. Always non-nil for the
	// production *dispatch.Dispatcher. May return nil for test spies that
	// do not exercise the Turn surface — handleGetTurn treats nil as
	// "feature unavailable" and returns 501.
	TurnRegistry() *turn.Registry
}

// ServerOption configures an optional Server dependency.
type ServerOption func(*Server)

// WithSwarmRegistry installs the swarm registry on the API server so the
// chat handler can route @<swarm-id> requests through the same shared
// dispatch service the CLI and TUI use. Without this option the handler
// falls back to plain streaming and any swarm id supplied as agent_id
// reaches the engine as if it were an agent — a silent no-op that
// breaks parity with the other surfaces.
//
// Per ADR - Swarm Dispatch Across Access Methods, all three surfaces
// (CLI, TUI, web) resolve through swarm.ResolveTarget and dispatch
// through swarm.DispatchSwarm; this option is what makes that true on
// the web side.
func WithSwarmRegistry(reg *swarm.Registry) ServerOption {
	return func(s *Server) { s.swarmRegistry = reg }
}

// WithDispatchEngine installs the engine used to install/flush a swarm
// context around dispatched runs. Without it the API can still resolve
// swarms (so error messages stay correct), but it cannot honour
// post-swarm gates or context propagation. Production wires
// *engine.Engine here; tests may pass a fake.
func WithDispatchEngine(eng swarm.DispatchEngine) ServerOption {
	return func(s *Server) { s.dispatchEngine = eng }
}

// WithSessionManager sets the session manager for session-scoped API routes.
//
// Expected:
//   - A valid session manager is provided.
//
// Returns:
//   - A ServerOption that installs the provided session manager.
//
// Side effects:
//   - None.
func WithSessionManager(mgr *session.Manager) ServerOption {
	return func(s *Server) { s.sessionManager = mgr }
}

// WithDispatcher installs an explicit DispatcherService on the API
// server. When unset, NewServer auto-constructs a *dispatch.Dispatcher
// from the wired streamer / dispatchEngine / swarm + agent registries
// so production callers get the unified path without an extra wiring
// step. Tests that want to record dispatch calls (e.g. wire-shape pin
// specs) pass a spy implementation via this option.
//
// Per the "Dispatcher Service Unification (May 2026)" plan §"Phase 1",
// the API handler routes through DispatchEphemeral when this surface
// is non-nil; nil falls back to the pre-Dispatcher orchestrator path
// for the legacy minimal-Server test compositions.
//
// Expected:
//   - A non-nil DispatcherService implementation.
//
// Returns:
//   - A ServerOption that installs the dispatcher.
//
// Side effects:
//   - None.
func WithDispatcher(d DispatcherService) ServerOption {
	return func(s *Server) { s.dispatcher = d }
}

// WithSessions sets the session store for session API routes.
//
// Expected:
//   - A valid session store is provided.
//
// Returns:
//   - A ServerOption that installs the provided session store.
//
// Side effects:
//   - None.
func WithSessions(store *ctxstore.FileSessionStore) ServerOption {
	return func(s *Server) { s.sessions = store }
}

// WithTodoStore sets the todo store for session todo routes.
//
// Expected:
//   - A valid todo.Store implementation is provided.
//
// Returns:
//   - A ServerOption that installs the provided todo store.
//
// Side effects:
//   - None.
func WithTodoStore(store todo.Store) ServerOption {
	return func(s *Server) { s.todoStore = store }
}

// WithBackgroundManager sets the background task manager for task endpoints.
//
// Expected:
//   - mgr is a non-nil BackgroundTaskManager.
//
// Returns:
//   - A ServerOption that sets the background manager.
//
// Side effects:
//   - None.
func WithBackgroundManager(mgr *engine.BackgroundTaskManager) ServerOption {
	return func(s *Server) { s.backgroundManager = mgr }
}

// WithEventBus configures the EventBus the api server subscribes to
// for the turn-registry projections (subscribeTurnHeartbeat,
// subscribeTurnContextCompacted, subscribeTurnGateFailed) and for the
// swarm-events SSE projection at GET /api/swarm/events.
//
// Phase-4-Commit-2 retired the per-session SSE/WebSocket bridges that
// previously consumed this bus; the surviving consumers translate bus
// events into Turn-registry state so long-poll surfaces them.
//
// Expected:
//   - bus is a non-nil EventBus from the engine.
//
// Returns:
//   - A ServerOption that sets the eventBus field.
//
// Side effects:
//   - None until NewServer's subscribeTurn* methods are called.
func WithEventBus(bus *eventbus.EventBus) ServerOption {
	return func(s *Server) { s.eventBus = bus }
}

// WithMetricsHandler sets the HTTP handler for the /metrics endpoint.
//
// Expected:
//   - h is a non-nil http.Handler serving metrics in Prometheus exposition format.
//
// Returns:
//   - A ServerOption that installs the provided metrics handler.
//
// Side effects:
//   - None.
func WithMetricsHandler(h http.Handler) ServerOption {
	return func(s *Server) { s.metricsHandler = h }
}

// ModelLister enumerates the providers and models the engine knows about.
//
// The function-adapter shape mirrors engine.CategoryResolver.WithModelLister
// so production wires api.WithModelLister(app.ListModels) without an
// api->app import edge, and tests pass closures that return canned data.
type ModelLister func() ([]provider.Model, error)

// WithModelLister installs the model enumeration source used by
// GET /api/v1/models. Without this option the endpoint returns 501 so
// callers can distinguish "no model lister wired" from "no models found".
func WithModelLister(l ModelLister) ServerOption {
	return func(s *Server) { s.modelLister = l }
}

// ContextUsageProvider returns the JSON payload (engine-side
// contextUsagePayload shape) the api server emits to keep the chat
// UI's usage chip in sync with current state outside the streamed
// pre-send / post-turn moments. Phase 3 of the May 2026 saturation
// fix wires this so the chip always reflects the current state, not
// just after the user sends — matching the TUI's StatusBar which
// reads engine.LastContextResult on every redraw.
//
// Production wires (*engine.Engine).ContextUsageJSONForSession.
//
// Returns ("", false) when the engine cannot compute a meaningful
// figure (no token counter wired, or no resolvable limit). Server
// suppresses the event in that case rather than emitting a malformed
// chunk the frontend would classify as "unknown".
type ContextUsageProvider interface {
	ContextUsageJSONForSession(providerID, modelID string, messages []provider.Message) (string, bool)
}

// WithContextUsageProvider installs the helper the api server calls on
// session-load (SSE-connect) and after agent / model PATCH so the
// chat UI's usage chip ticks up immediately rather than waiting for
// the next pre-send to land. Without this option the server still
// works — it just cannot push fresh figures outside streamed events,
// matching the pre-Phase-3 behaviour.
func WithContextUsageProvider(p ContextUsageProvider) ServerOption {
	return func(s *Server) { s.contextUsageProvider = p }
}

// CompactionController is the engine-side surface the compression
// config + manual-compact endpoints call into. Deliverable 2 + 3 of
// the May 2026 context-accuracy bundle: operators want a runtime-
// tunable soft threshold AND a /compress slash command that bypasses
// every guard and force-fires the L2 compactor on the current session.
//
// Production wires (*engine.Engine) — see engine.SetAutoCompactionThreshold,
// engine.AutoCompactionThreshold, engine.CompactNow. The api layer
// neither reads nor writes engine fields directly; everything flows
// through this interface so tests can substitute a fake and so the
// engine's locking discipline (buildStateMu around compressionConfig
// mutations) stays inside the engine package.
//
// Returns:
//   - AutoCompactionThreshold: the current configured ratio (0, 1].
//   - SetAutoCompactionThreshold: nil on success; a diagnostic error
//     when the value fails the (NaN, range) validation. Callers
//     surface the message verbatim to the operator.
//   - CompactNow: (summary, true) on a successful fire,
//     ("", false) when there is nothing to compact OR the layer is
//     disabled. The Vue UI's "nothing to compact" toast hangs off
//     the second return value.
type CompactionController interface {
	AutoCompactionThreshold() float64
	SetAutoCompactionThreshold(threshold float64) error
	CompactNow(ctx context.Context, sessionID string) (string, bool)
}

// WithCompactionController installs the engine-side controller for
// the compression endpoints. Without this option the server returns
// 501 on /api/v1/config/compression and /api/v1/sessions/{id}/compress
// — operators see "wired but disabled" rather than a 404 confusion.
func WithCompactionController(c CompactionController) ServerOption {
	return func(s *Server) { s.compactionController = c }
}

// WithOriginPatterns installs the glob allowlist used by the HTTP
// RequireOrigin middleware. Empty/unset is treated as ["localhost:*"]
// via originAllowlist(). Phase-4-Commit-2 retired the WebSocket
// handshake that originally consumed this allowlist alongside the
// HTTP middleware; the allowlist now gates HTTP requests exclusively.
//
// Production wires this from cfg.Auth.AllowedOrigins (TOML); tests may
// pass an explicit list or omit the option entirely.
func WithOriginPatterns(patterns []string) ServerOption {
	return func(s *Server) { s.originPatterns = patterns }
}

// QuotaAggregator is the engine-side surface the PR5 dashboard
// endpoints call into. Returns the list of (provider, account_hash,
// model) tuples the engine has observed and the Snapshot for each.
//
// Plan PR5 row 429 — the aggregator backs GET /api/v1/providers/quota
// + POST /api/v1/providers/quota/reset.
//
// Production wires (*engine.Engine) which delegates to the configured
// quota.Tracker. Tests pass a fake closure that returns canned
// Snapshots.
type QuotaAggregator interface {
	// QuotaSnapshots returns every (provider, account_hash, model)
	// Snapshot the engine has tracked. Order is unspecified; the API
	// handler sorts for deterministic JSON. Each row carries the
	// quota.Snapshot directly — the API handler then projects it
	// into the JSON wire shape (snapshotToDashboardEntry in
	// quota_dashboard.go).
	QuotaSnapshots(ctx context.Context) []QuotaAggregatorRow

	// ResetQuotaSpend zeros the spend counter for the given key. The
	// engine deletes the underlying Store entry and purges the per-
	// request cumulative cache; a subsequent UsageDelta starts the
	// counter from zero. Returns true when the key existed, false
	// when no Snapshot was found for the tuple. Errors propagate from
	// the Store impl.
	ResetQuotaSpend(ctx context.Context, providerID, accountHash, modelID string) (bool, error)
}

// QuotaAggregatorRow is one row in the dashboard's aggregated view.
// Field-for-field mirror of engine.QuotaAggregatorRow to keep the
// engine-package import edge one-way (api → engine — fine; engine
// → api — forbidden per ADR-Engine-Boundary).
type QuotaAggregatorRow struct {
	Provider    string
	AccountHash string
	Model       string
	Snapshot    quota.Snapshot
}

// WithQuotaAggregator installs the dashboard-side surface. Without it
// the dashboard endpoints return 501 so the SPA can distinguish "no
// quota tracker wired" (PR4 wiring incomplete) from "no providers
// configured" (empty array).
func WithQuotaAggregator(a QuotaAggregator) ServerOption {
	return func(s *Server) { s.quotaAggregator = a }
}

// SetBackgroundManager sets the background manager after server construction.
//
// Expected:
//   - mgr is a non-nil BackgroundTaskManager.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Updates the server's background manager reference.
func (s *Server) SetBackgroundManager(mgr *engine.BackgroundTaskManager) {
	s.backgroundManager = mgr
}

// SetCompletionOrchestrator attaches the completion orchestrator so user-
// initiated messages can reset the autonomous re-prompt budget.
//
// Expected:
//   - orch is a non-nil CompletionOrchestrator.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Updates the server's orchestrator reference.
func (s *Server) SetCompletionOrchestrator(orch *engine.CompletionOrchestrator) {
	s.completionOrchestrator = orch
}

// NewServer creates a new API server with the given dependencies.
//
// Expected:
//   - streamer is a non-nil Streamer for handling chat requests.
//   - registry is the agent registry for listing and retrieving manifests.
//   - disc is the discovery service for agent suggestions.
//   - skills is the list of available skills.
//   - sessions is the session store, or nil if sessions are disabled.
//
// Returns:
//   - A configured Server with all routes registered.
//
// Side effects:
//   - Registers HTTP route handlers on the internal mux.
func NewServer(
	streamer Streamer,
	registry *agent.Registry,
	disc *discovery.AgentDiscovery,
	skills []skill.Skill,
	opts ...ServerOption,
) *Server {
	s := &Server{
		streamer:  streamer,
		registry:  registry,
		discovery: disc,
		skills:    skills,
		mux:       http.NewServeMux(),
	}
	for _, opt := range opts {
		opt(s)
	}
	// Auto-construct the Dispatcher whenever a streamer is wired (which
	// is the case for every API server in this codebase, production or
	// test). DispatchEphemeral handles the bare-engine pass-through
	// case internally — a nil swarmRegistry and nil dispatchEngine
	// degenerate to streaming.Run, matching the pre-Dispatcher legacy
	// fallback at handleChat. Per "Dispatcher Service Unification
	// (May 2026)" §"Phase 1", routing /api/chat through the Dispatcher
	// is the load-bearing change — every code path on this handler
	// goes through DispatchEphemeral so the same context.WithoutCancel
	// + resolve+dispatch lifecycle applies uniformly.
	if s.dispatcher == nil && s.streamer != nil {
		var mgrAdapter dispatch.SessionManager
		if s.sessionManager != nil {
			mgrAdapter = s.sessionManager
		}
		s.dispatcher = dispatch.New(
			streamingAdapter{s.streamer},
			s.dispatchEngine,
			s.swarmRegistry,
			s.registry,
			mgrAdapter,
		)
	}
	// Phase-4-Commit-1 — wire the polling-side heartbeat subscriber.
	// The engine's runStreamingHeartbeat ticker publishes
	// events.EventStreamingHeartbeat on the bus during a Running turn;
	// this subscriber translates session_id → turn_id via the registry
	// and writes phase + token_count onto the Turn so GET /turns/{id}
	// can surface live progress.
	//
	// Phase-4-Commit-2 retired the SSE-side bridge at
	// internal/api/event_bridge.go — long-poll on GET /turns/{id} is
	// now the sole live channel. The three turn-registry subscribers
	// (heartbeat, context_compacted, gate_failed) feed the poll's
	// snapshot directly.
	//
	// Plan ref: ~/vaults/baphled/1. Projects/FlowState/Plans/
	//   Turn-Based Post-Then-Poll Architecture (May 2026).md §4d Commit 1/2.
	s.subscribeTurnHeartbeat()
	s.subscribeTurnContextCompacted()
	s.subscribeTurnGateFailed()
	s.setupRoutes()
	return s
}

// subscribeTurnHeartbeat wires the polling-side heartbeat subscriber
// (Phase-4-Commit-1). No-op when either the eventBus or the dispatcher's
// Turn registry is unwired — test fixtures that skip WithEventBus or
// use a spy dispatcher with TurnRegistry()==nil simply do not get the
// heartbeat projection.
//
// Production wiring: cmd/serve passes WithEventBus(bus) and
// WithDispatcher / auto-construct gives a real *dispatch.Dispatcher
// whose TurnRegistry() is always non-nil.
//
// Side effects:
//   - Subscribes a closure to events.EventStreamingHeartbeat on the bus.
//   - The closure reads turn_id from the running turn for the event's
//     session_id and writes phase + token_count onto the Turn.
func (s *Server) subscribeTurnHeartbeat() {
	if s.eventBus == nil || s.dispatcher == nil {
		return
	}
	registry := s.dispatcher.TurnRegistry()
	if registry == nil {
		return
	}
	s.eventBus.Subscribe(events.EventStreamingHeartbeat, func(msg any) {
		hb, ok := msg.(*events.StreamingHeartbeatEvent)
		if !ok {
			return
		}
		// Translate session_id → turn_id via the registry. The
		// heartbeat event payload carries only session-level data
		// (the engine pipeline does not thread turn_id onto the bus
		// payload — the registry is the single source of truth for
		// "what Turn is this session driving").
		turnID, ok := registry.FindActiveBySession(hb.Data.SessionID)
		if !ok {
			return
		}
		registry.SetHeartbeat(turnID, hb.Data.Phase, int(hb.Data.TokenCount))
	})
}

// subscribeTurnContextCompacted wires the polling-side context_compacted
// subscriber (Phase-5 §1c-γ). Mirrors subscribeTurnHeartbeat's shape: the
// closure resolves the active turn for the event's session_id via the
// registry, then appends the compaction payload onto the Turn so
// GET /turns/{id} can surface compaction history.
//
// Phase-4-Commit-2 retired the SSE-side bridge at event_bridge.go;
// this subscriber is the sole consumer of EventContextCompacted on
// the API layer.
//
// No-op when either the eventBus or the dispatcher's Turn registry is
// unwired — test fixtures that skip WithEventBus simply do not get the
// projection.
func (s *Server) subscribeTurnContextCompacted() {
	if s.eventBus == nil || s.dispatcher == nil {
		return
	}
	registry := s.dispatcher.TurnRegistry()
	if registry == nil {
		return
	}
	s.eventBus.Subscribe(events.EventContextCompacted, func(msg any) {
		ce, ok := msg.(*events.ContextCompactedEvent)
		if !ok {
			return
		}
		turnID, ok := registry.FindActiveBySession(ce.Data.SessionID)
		if !ok {
			return
		}
		registry.AppendCompactionEvent(turnID, turn.CompactionEvent{
			SessionID:      ce.Data.SessionID,
			AgentID:        ce.Data.AgentID,
			OriginalTokens: ce.Data.OriginalTokens,
			SummaryTokens:  ce.Data.SummaryTokens,
			LatencyMS:      ce.Data.LatencyMS,
			Trigger:        ce.Data.Trigger,
		})
	})
}

// subscribeTurnGateFailed wires the polling-side gate_failed subscriber
// (Phase-5 §1c-γ). Same shape as subscribeTurnContextCompacted.
//
// Halt-class failures only — the gate event bus publishes only halts
// per Plans/Gate Bus Bridge — continue-class and warn-class never
// reach this subscriber. The CoordStoreKeys slice is preserved verbatim
// onto the Turn so the FE's banner expander has data to render.
func (s *Server) subscribeTurnGateFailed() {
	if s.eventBus == nil || s.dispatcher == nil {
		return
	}
	registry := s.dispatcher.TurnRegistry()
	if registry == nil {
		return
	}
	s.eventBus.Subscribe(events.EventGateFailed, func(msg any) {
		ge, ok := msg.(*events.GateFailedEvent)
		if !ok {
			return
		}
		turnID, ok := registry.FindActiveBySession(ge.Data.SessionID)
		if !ok {
			return
		}
		// Copy CoordStoreKeys so the registry owns its memory — the
		// bus payload's slice could be mutated post-publish by the
		// engine; defensively copy at the boundary.
		var coordKeys []string
		if len(ge.Data.CoordStoreKeys) > 0 {
			coordKeys = append([]string(nil), ge.Data.CoordStoreKeys...)
		}
		registry.AppendGateFailure(turnID, turn.GateFailure{
			SwarmID:        ge.Data.SwarmID,
			Lifecycle:      ge.Data.Lifecycle,
			MemberID:       ge.Data.MemberID,
			GateName:       ge.Data.GateName,
			GateKind:       ge.Data.GateKind,
			Reason:         ge.Data.Reason,
			Cause:          ge.Data.Cause,
			CoordStoreKeys: coordKeys,
		})
	})
}

// streamingAdapter bridges the API's local Streamer interface to
// streaming.Streamer's structurally-identical shape. Both interfaces
// declare the same single method but Go's nominal interface typing
// requires an explicit adapter when assigning to a typed interface
// variable inside the dispatch package's constructor.
type streamingAdapter struct {
	inner Streamer
}

func (a streamingAdapter) Stream(ctx context.Context, agentID, message string) (<-chan provider.StreamChunk, error) {
	return a.inner.Stream(ctx, agentID, message)
}

// Handler returns the HTTP handler for this server, wrapped with security headers middleware.
//
// Returns:
//   - The http.Handler that serves all API routes, with security headers applied.
//
// Side effects:
//   - None.
func (s *Server) Handler() http.Handler {
	return securityHeaders(s.mux)
}

// InstallAuth wires the auth bundle and rebuilds the route map. Called
// from cmd/serve at boot when the FLOWSTATE_AUTH_ENABLED env var is true
// (PR4/C9 — deferred from PR3 ship-state). Because the route map is
// built during NewServer, late-installing auth requires throwing away
// the current mux and re-registering every route through the auth-aware
// register* helpers.
//
// Expected:
//   - bundle is a fully-constructed AuthBundle (Origin / Session / Auth /
//     CSRF + optional IdentitySource).
//   - InstallAuth is called BEFORE the server starts serving traffic so
//     no in-flight handler sees the half-rebuilt mux.
//
// Returns:
//   - None.
//
// Side effects:
//   - Replaces s.mux with a fresh http.ServeMux.
//   - Re-runs setupRoutes() so every route registers via the auth-aware
//     helpers using the new bundle.
//   - When bundle.IdentitySource is set, registers POST /api/auth/login
//     and POST /api/auth/logout.
func (s *Server) InstallAuth(bundle AuthBundle) {
	s.auth = bundle
	s.mux = http.NewServeMux()
	s.setupRoutes()
}

// securityHeaders returns a middleware that adds defensive HTTP security headers to every response.
//
// Expected:
//   - next is a non-nil http.Handler.
//
// Returns:
//   - An http.Handler that sets security headers then delegates to next.
//
// Side effects:
//   - Adds X-Content-Type-Options, X-Frame-Options, Content-Security-Policy,
//     and X-XSS-Protection headers to every response.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		// Plan "Chat Attachments Backend (May 2026)" §6 task-09: extend
		// `img-src` to permit `'self'` (so the same-origin
		// `/api/v1/sessions/{id}/attachments/{aid}` GETs render) and
		// `data:` (so base64 image data-URLs in assistant responses
		// render). All other directives stay constrained via
		// `default-src 'self'`. No `unsafe-inline` / `unsafe-eval`;
		// no script/style widening. Single source of truth — the
		// existing only Content-Security-Policy Set site in this repo
		// (grep `Set("Content-Security-Policy"` in /internal/).
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		next.ServeHTTP(w, r)
	})
}

// setupRoutes registers all HTTP route handlers on the server's mux.
//
// PR3/C7 — routes are classified via the registerProtected /
// registerPublic / registerLogin helpers per plan §"Endpoint Inventory".
// When AuthBundle is unwired OR AuthBundle.Auth.Enabled is false, the
// helpers no-op and routes register plain on the mux — so the pre-PR3
// behaviour is preserved exactly. PR5/C10 flipped the default-on at
// the config layer (config.DefaultAuthConfig.Enabled=true); servers
// boot-wired via cmd/serve install an active AuthBundle and the
// helpers wrap as designed.
//
// Side effects:
//   - Registers every API route on the internal mux.
//   - Wraps protected routes through the auth chain when the flag is on.
func (s *Server) setupRoutes() {
	// Public — read-only catalog / probe endpoints (no PII, no
	// session-scoped state). Plan §"Endpoint Inventory" Public list.
	s.registerPublic("GET /api/agents", s.handleListAgents)
	s.registerPublic("GET /api/agents/{id}", s.handleGetAgent)
	s.registerPublic("GET /api/swarms", s.handleListSwarms)
	s.registerPublic("GET /api/discover", s.handleDiscover)
	s.registerPublic("GET /api/skills", s.handleListSkills)
	// `GET /api/sessions` is a legacy duplicate of `GET /api/v1/sessions`
	// (plan §"Endpoint Inventory"); kept public for backwards compat
	// and flagged for deprecation in PR5. Plan §"Rollout Plan" PR5/C10.
	s.registerPublic("GET /api/sessions", s.handleListSessions)
	s.registerPublic("GET /", s.handleIndex)
	s.registerPublic("GET /api/v1/models", s.handleListModels)
	s.registerPublic("GET /health", s.handleHealth)

	// Protected — session-scoped or mutating endpoints. Plan
	// §"Endpoint Inventory" Protected list (16 endpoints). When the flag
	// is on, every entry goes through RequireOrigin → RequireSession →
	// Protect (gorilla/csrf) → RequireCSRFRecordBound.
	s.registerProtected("POST /api/chat", s.handleChat)
	s.registerProtected("POST /api/v1/sessions", s.handleCreateSession)
	s.registerProtected("GET /api/v1/sessions", s.handleListV1Sessions)
	s.registerProtected("POST /api/v1/sessions/{id}/messages", s.handleSessionMessage)
	// Phase 2 of "Turn-Based Post-Then-Poll Architecture (May 2026)".
	// The frontend polls this endpoint with the turn_id returned from
	// POST /messages to read the engine-emitted messages and the
	// running → completed | failed status transition.
	s.registerProtected("GET /api/v1/sessions/{id}/turns/{turn_id}", s.handleGetTurn)
	s.registerProtected("GET /api/v1/sessions/{id}/messages", s.handleSessionMessages)
	s.registerProtected("GET /api/v1/sessions/{id}/todos", s.handleSessionTodos)
	s.registerProtected("GET /api/v1/sessions/{id}/children", s.handleSessionChildren)
	s.registerProtected("GET /api/v1/sessions/{id}/tree", s.handleSessionTree)
	s.registerProtected("GET /api/v1/sessions/{id}/parent", s.handleSessionParent)
	s.registerProtected("DELETE /api/v1/sessions/{id}", s.handleDeleteSession)
	s.registerProtected("DELETE /api/v1/sessions/{id}/messages/from/{messageId}", s.handleTruncateMessages)
	s.registerProtected("PATCH /api/v1/sessions/{id}/agent", s.handleUpdateSessionAgent)
	s.registerProtected("PATCH /api/v1/sessions/{id}/model", s.handleUpdateSessionModel)
	s.registerProtected("GET /api/v1/tasks", s.handleListTasks)
	s.registerProtected("GET /api/v1/tasks/{id}", s.handleGetTask)
	s.registerProtected("DELETE /api/v1/tasks/{id}", s.handleCancelTask)
	s.registerProtected("DELETE /api/v1/tasks", s.handleCancelAllTasks)
	// GET /api/swarm/events is the H5 closure — currently bearer-by-
	// session_id per memory project_flowstate_api_bearer_by_session_id.
	// PR3/C7 wraps it in the auth chain so the session_id filter becomes
	// defence-in-depth rather than the sole check.
	s.registerProtected("GET /api/swarm/events", s.handleSwarmEvents)

	// Deliverable 2 / 3 of the May 2026 context-accuracy bundle —
	// runtime-tunable compression threshold + manual /compress
	// endpoint. Session-scoped mutations → protected.
	s.registerProtected("GET /api/v1/config/compression", s.handleGetCompressionConfig)
	s.registerProtected("PATCH /api/v1/config/compression", s.handleUpdateCompressionConfig)
	s.registerProtected("POST /api/v1/sessions/{id}/compress", s.handleCompactNow)
	// Chat Attachments Backend PR1 — plan "Chat Attachments Backend
	// (May 2026)" §6 task-03. Session-scoped upload + retrieval →
	// protected.
	s.registerProtected("POST /api/v1/sessions/{id}/attachments", s.handleUploadAttachments)
	s.registerProtected("GET /api/v1/sessions/{id}/attachments/{aid}", s.handleGetAttachment)

	if s.metricsHandler != nil {
		// Metrics is host-bind-gated (typically loopback-only) per plan
		// §"Endpoint Inventory" — host perimeter controls access, no
		// auth wrap needed. Wired via direct Handle to keep the
		// non-HandlerFunc shape that the metricsHandler interface
		// permits.
		s.mux.Handle("GET /metrics", s.metricsHandler)
	}

	// Login + logout routes — PR4/C9 (deferred from PR3 ship-state).
	// Only registered when the AuthBundle carries an IdentitySource AND
	// the feature flag is on. Origin + CSRF wrap via registerLogin; the
	// session middleware deliberately does NOT run on these endpoints
	// (login mints the session, logout drops it — neither prerequisites
	// an existing session). Plan §"Endpoint Inventory" line 398.
	if s.auth.active() && s.auth.IdentitySource != nil {
		s.registerLogin("POST /api/auth/login",
			auth.HandleLogin(s.auth.IdentitySource, s.auth.Session))
		s.registerLogin("POST /api/auth/logout",
			auth.HandleLogout(s.auth.Session))
	}

	// CSRF prefetch — QA showstopper fix (May 2026). The SPA's
	// first-time login flow has no _csrf cookie to read, so the POST
	// /api/auth/login was rejected with 403 before credentials were
	// evaluated. GET /api/auth/csrf is wrapped through registerLogin
	// (RequireOrigin + Protect, no Session) so the wrap's ServeHTTP
	// issues the _csrf cookie on the GET and csrf.Token(r) returns a
	// non-empty masked token. The SPA caches the response token in a
	// Pinia store and echoes it back as X-CSRF-Token on the login POST.
	//
	// Registered conditionally on the auth bundle being active — when
	// auth is disabled (flag-off), the route is unregistered so the
	// SPA's pre-login probe returns 404 (matching the "auth doesn't
	// matter here" branch). When auth.active() but no IdentitySource
	// is configured, the prefetch still ships — operators can wire
	// /api/auth/csrf ahead of users.json provisioning.
	if s.auth.active() {
		s.registerLogin("GET /api/auth/csrf", auth.HandleCSRFPrefetch())
	}

	// Whoami — PR5/C10. Returns the authenticated principal's id +
	// display name + mode. Goes through registerProtected so the
	// 401 wire shape on no-session is byte-identical to every other
	// protected endpoint (B8 fold — task brief: "MUST NOT leak mode
	// information to unauthenticated callers"). Plan §"Wire Protocol"
	// lines 503-522 (the authenticated branch ships; the unauth branch
	// is folded down to the uniform 401 to honour B8).
	s.registerProtected("GET /api/auth/whoami", handleWhoami)

	// PR5 of the Provider Quota and Spend Visibility plan (May 2026).
	// Dashboard aggregator + manual-reset endpoints. Both protected so
	// the 401 wire shape stays uniform per Auth Track B8 carry-through.
	// POST .../reset is state-changing → CSRF header required (handled
	// by the middleware chain registerProtected composes).
	s.registerProtected("GET /api/v1/providers/quota", s.handleListProviderQuotas)
	s.registerProtected("POST /api/v1/providers/quota/reset", s.handleResetProviderQuota)
}

// handleListAgents writes all registered agent manifests as JSON to the response.
//
// Expected:
//   - None.
//
// Side effects:
//   - Writes HTTP 200 response with JSON-encoded agent manifests.
func (s *Server) handleListAgents(w http.ResponseWriter, _ *http.Request) {
	manifests := s.registry.List()
	if manifests == nil {
		manifests = []*agent.Manifest{}
	}
	writeJSON(w, manifests)
}

// handleGetAgent retrieves and writes a single agent manifest by ID as JSON.
//
// Expected:
//   - Request path parameter "id" contains the agent identifier.
//
// Side effects:
//   - Writes HTTP 200 with JSON-encoded manifest if found.
//   - Writes HTTP 404 if agent not found.
func (s *Server) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	manifest, ok := s.registry.Get(id)
	if !ok {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}
	writeJSON(w, manifest)
}

// swarmListEntry is the compact projection of swarm.Manifest the web
// frontend's @-picker consumes via GET /api/swarms. Mirrors GET
// /api/agents in shape but trims the manifest to the four fields the
// picker needs — id, description, lead, members. The full manifest
// (gates, harness, retry, circuit breaker) stays server-side because
// no web surface today reasons about gate kinds or precedence.
type swarmListEntry struct {
	ID          string   `json:"id"`
	Description string   `json:"description,omitempty"`
	Lead        string   `json:"lead"`
	Members     []string `json:"members"`
}

// handleListSwarms writes registered swarm manifests as a compact JSON
// array projected through swarmListEntry. The web frontend's
// MessageInput populates its @-picker swarm slice from this endpoint
// (see web/src/stores/chatStore.ts loadSwarms). The shape is
// independent of the on-disk YAML so adding manifest fields later
// (sub-swarm composition, harness tweaks) does not push them onto the
// wire automatically — the projection is opt-in.
//
// Expected:
//   - None.
//
// Side effects:
//   - Writes HTTP 200 with Content-Type application/json.
//   - When the swarm registry is unconfigured (Server constructed
//     without WithSwarmRegistry — test surfaces, or a build that
//     omitted the loader), returns `[]` so the web client never sees
//     `null`. Matches GET /api/agents' empty-registry contract.
func (s *Server) handleListSwarms(w http.ResponseWriter, _ *http.Request) {
	if s.swarmRegistry == nil {
		writeJSON(w, []swarmListEntry{})
		return
	}
	manifests := s.swarmRegistry.List()
	out := make([]swarmListEntry, 0, len(manifests))
	for _, m := range manifests {
		members := m.Members
		if members == nil {
			members = []string{}
		}
		out = append(out, swarmListEntry{
			ID:          m.ID,
			Description: m.Description,
			Lead:        m.Lead,
			Members:     members,
		})
	}
	writeJSON(w, out)
}

// chatRequest represents a chat message request from the client.
type chatRequest struct {
	AgentID string `json:"agent_id"`
	Message string `json:"message"`
}

// Error sanitization: all HTTP and SSE error paths in this file route through
// writeJSONError or writeSSEClientError (see errors.go). Raw err.Error() text
// is never forwarded to clients; a canonical category message and a random
// correlation ID are sent instead. The full error is logged server-side under
// the same correlation ID so operators can locate the matching entry.

// handleChat processes a chat request and streams the response as server-sent events.
//
// Expected:
//   - Request body contains JSON-encoded chatRequest with agent_id and message.
//   - ResponseWriter supports HTTP flushing for streaming.
//   - Optional query parameter "verbosity" accepts "minimal", "standard", or "verbose".
//   - When agent_id resolves to a registered swarm via the swarm registry
//     (WithSwarmRegistry option), the request is dispatched through
//     swarm.DispatchSwarm so post-swarm gates fire and the swarm context
//     is installed on the engine — matching the CLI and TUI shapes.
//   - When the swarm registry is not configured or agent_id resolves to a
//     plain agent, falls back to plain streaming.Run for backward
//     compatibility with API consumers built against the agent-only
//     contract.
//
// Side effects:
//   - Writes HTTP 200 with Content-Type text/event-stream.
//   - Streams content chunks, errors, and completion marker as SSE data lines.
//   - Writes HTTP 400 if request body is invalid JSON or agent_id is unknown.
//   - Writes HTTP 500 if streaming is not supported.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Pre-resolve to surface unknown agent_id as HTTP 400 BEFORE we
	// commit to SSE. The Dispatcher would also resolve internally;
	// this duplicate-but-cheap pre-flight preserves the historical
	// 400-on-unknown-id contract because once SSE headers are sent
	// the response is committed and cannot return a non-200 status.
	if _, _, err := s.resolveDispatchTarget(req.AgentID); err != nil {
		writeJSONError(w, err, "swarm_error", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	sseConsumer, ok := NewSSEConsumer(w)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	verbosityParam := r.URL.Query().Get("verbosity")
	consumer := streaming.NewVerbosityFilter(sseConsumer, parseVerbosityLevel(verbosityParam))

	// Phase 1 of "Dispatcher Service Unification (May 2026)" — route
	// through the unified Dispatcher. NewServer auto-constructs a
	// *dispatch.Dispatcher whenever a streamer is wired (including
	// the bare-engine test compositions) so this branch is always
	// taken. The Dispatcher applies context.WithoutCancel internally
	// (preserving 51fb416c's pattern at the seam rather than the
	// handler edge) and shares its resolve+dispatch logic with the
	// future /messages migration in Phase 2 + WS handler in Phase 4.
	//
	// Pre-flight resolution above keeps the historical 400-on-unknown-
	// id contract symmetric with the CLI's *swarm.NotFoundError; the
	// Dispatcher repeats the resolve internally but by then we have
	// committed to SSE, so the duplicate-but-cheap pre-flight stays.
	handle, dispatchErr := s.dispatcher.DispatchEphemeral(r.Context(), dispatch.DispatchRequest{
		AgentID:      req.AgentID,
		Content:      req.Message,
		ScanMentions: true,
	}, consumer)
	if dispatchErr != nil {
		log.Printf("[api] chat dispatch error: %v", dispatchErr)
		return
	}
	// Block until the Dispatcher's streamer goroutine completes so the
	// SSE response finalises after the last chunk drains. The goroutine
	// inside DispatchEphemeral honours context.WithoutCancel internally
	// — handler return cannot kill it, and Done emits the terminal
	// stream error (or nil) before closing.
	if streamErr := <-handle.Done; streamErr != nil {
		log.Printf("[api] chat stream error: %v", streamErr)
	}
}

// resolveDispatchTarget classifies req.AgentID into a (lead-or-agent,
// swarmCtx) pair using the same shared swarm.ResolveTarget the CLI's
// resolveAgentOrSwarm uses (the TUI chat intent reaches the same
// resolver via orchestrator.Stream). When
// the swarm registry is not installed (test surface, or a build that
// omitted the loader), passes the input id through verbatim with a nil
// swarmCtx — matching the CLI's bare-engine pass-through contract.
//
// Expected:
//   - id is the user-supplied agent_id from the chat request body.
//
// Returns:
//   - leadID: the agent id to drive the streamer with. For agent
//     targets and pass-through this is id verbatim; for swarm targets
//     it is the swarm's lead.
//   - swarmCtx: the *swarm.Context to install on the engine; nil for
//     agent targets and pass-through.
//   - err: non-nil only when both registries are configured and id
//     matches neither (the api surface treats this as a 400 to keep
//     the contract symmetric with the CLI's *swarm.NotFoundError).
//
// Side effects:
//   - None.
func (s *Server) resolveDispatchTarget(id string) (string, *swarm.Context, error) {
	if s.swarmRegistry == nil {
		return id, nil, nil
	}
	hasAgent := func(name string) bool {
		if s.registry == nil {
			return false
		}
		if _, ok := s.registry.Get(name); ok {
			return true
		}
		_, ok := s.registry.GetByNameOrAlias(name)
		return ok
	}
	return swarm.ResolveTarget(hasAgent, s.swarmRegistry, id)
}

// handleDiscover retrieves agent suggestions based on a message query parameter.
//
// Expected:
//   - Query parameter "message" contains the user's input for discovery.
//
// Side effects:
//   - Writes HTTP 200 with JSON-encoded agent suggestions.
func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	message := r.URL.Query().Get("message")
	suggestions := s.discovery.Suggest(message)
	if suggestions == nil {
		suggestions = []discovery.AgentSuggestion{}
	}
	writeJSON(w, suggestions)
}

// handleListSkills writes all available skills as JSON to the response.
//
// Expected:
//   - None.
//
// Side effects:
//   - Writes HTTP 200 response with JSON-encoded skills list.
func (s *Server) handleListSkills(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.skills)
}

// handleCreateSession creates a new session and returns its summary.
//
// Expected:
//   - Request body may include agent_id.
//
// Side effects:
//   - Creates a session through the session manager.
//   - Writes a JSON session summary response.
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	if s.sessionManager == nil {
		http.Error(w, errSessionManagerNotConfigured, http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	type reqBody struct {
		AgentID string `json:"agent_id"`
	}
	var req reqBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AgentID == "" {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	// Pre-populate the new session's CurrentProviderID / CurrentModelID
	// from the agent manifest's first PreferredModels entry when one
	// exists. Without this the brand-new session has empty model+provider
	// fields, the activity-indicator chip renders nothing on the very
	// first turn, and the user has no way to confirm which model is
	// producing the answer they're watching arrive — the regression
	// reported during Track B hands-on UI verification (May 2026).
	//
	// The agent manifest is the canonical source: each manifest declares
	// PreferredModels in priority order, and the first entry is the
	// pairing the agent is intended to run on. The model picker uses the
	// same first entry as its default highlight.
	//
	// Empty / missing manifest, or an empty PreferredModels list, both
	// degrade silently to the legacy behaviour (no defaults; the chip
	// stays hidden until the user picks a model or the engine streams an
	// assistant turn that stamps the pair onto the session).
	defaultProvider, defaultModel := defaultModelPairForAgent(s.registry, req.AgentID)
	sess, err := s.sessionManager.CreateSessionWithDefaults(req.AgentID, defaultProvider, defaultModel)
	if err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}
	// CreateSession returns the *Session pointer that has just been
	// installed in the manager's map. Although unlikely in practice,
	// another goroutine that already knows the ID (e.g. an aggressive
	// frontend kicking off SendMessage immediately after POST returns
	// before the response is written) could race the dereference here.
	// Snapshot via the new ID for symmetry with the other handlers and
	// to keep the "no *Session past lock boundary" invariant uniform.
	snap, err := s.sessionManager.SnapshotSession(sess.ID)
	if err != nil {
		http.Error(w, "failed to read created session", http.StatusInternalServerError)
		return
	}
	writeJSON(w, NewSessionResponse(&snap))
}

// defaultModelPairForAgent returns the (provider, model) pair the agent's
// manifest declares as preferred, used to seed a newly-created session so
// the chat activity-indicator chip renders immediately rather than waiting
// for an explicit selection or a streamed assistant turn.
//
// Expected:
//   - registry may be nil (early start-up, tests without a registry); the
//     function tolerates this and returns empty strings.
//   - agentID identifies the agent that owns the new session.
//
// Returns:
//   - The Provider and Model strings from the first entry in the agent
//     manifest's PreferredModels list, or empty strings when:
//     (a) the registry is nil
//     (b) the agentID is unknown
//     (c) the manifest declares no preferred models
//
// Side effects:
//   - None.
func defaultModelPairForAgent(registry *agent.Registry, agentID string) (provider, model string) {
	if registry == nil || agentID == "" {
		return "", ""
	}
	manifest, ok := registry.Get(agentID)
	if !ok || manifest == nil || len(manifest.PreferredModels) == 0 {
		return "", ""
	}
	first := manifest.PreferredModels[0]
	return first.Provider, first.Model
}

// handleListV1Sessions lists all sessions as summaries.
//
// Expected:
//   - A session manager is configured.
//
// Side effects:
//   - Writes a JSON array of session summaries.
//   - Sets ActiveTurnID on each summary when the dispatcher's Turn
//     registry has a Running turn for the session (Phase-4-Commit-1).
//   - Sets IsStreaming on each summary as a backward-compatibility
//     mirror of `ActiveTurnID != ""`. Phase-4-Commit-2 retired the
//     SSE broker so IsStreaming is no longer broker-driven; existing
//     FE consumers (chatStore.ts wasStreaming→nowStreaming transition
//     detector) continue to work because the wire field still flips
//     across the same boundary — when the Turn ends and the registry
//     no longer reports it as active.
func (s *Server) handleListV1Sessions(w http.ResponseWriter, _ *http.Request) {
	if s.sessionManager == nil {
		http.Error(w, errSessionManagerNotConfigured, http.StatusNotImplemented)
		return
	}
	summaries := s.sessionManager.ListSessions()
	if summaries == nil {
		summaries = []*session.Summary{}
	}
	// Cache the turn registry once outside the loop — dispatcher.TurnRegistry()
	// is a getter but the indirection is wasted work on every iteration.
	var turnRegistry *turn.Registry
	if s.dispatcher != nil {
		turnRegistry = s.dispatcher.TurnRegistry()
	}
	for _, sum := range summaries {
		if turnRegistry != nil {
			if id, ok := turnRegistry.FindActiveBySession(sum.ID); ok {
				sum.ActiveTurnID = id
				sum.IsStreaming = true
			}
		}
	}
	writeJSON(w, summaries)
}

// handleSessionMessage appends a message to a session and returns the
// updated session. Phase 2 of "Dispatcher Service Unification (May
// 2026)" delegates ALL resolve + dispatch + lifecycle logic to
// dispatch.Dispatcher.DispatchSessioned. The handler is now a thin
// adapter: decode body → build DispatchRequest → call
// DispatchSessioned → write Snapshot as JSON → 200 OK.
//
// SessionedHandle has no Done channel, so the handler structurally
// cannot block on stream completion. This preserves the async-POST
// contract from commit e4bf9632 by construction rather than by
// convention: the broker.Publish goroutine fans chunks to live SSE
// subscribers asynchronously inside Dispatcher.
//
// The three local helpers (resolveAutoDispatchSwarm,
// resolveInContentMention, wrapWithSwarmLifecycle) that grew on this
// handler in commits 07b0480e + 48380376 are DELETED as of this
// commit — their logic moved verbatim into Dispatcher.
//
// Expected:
//   - Request path parameter "id" contains the session identifier.
//   - Request body contains non-empty content.
//
// Side effects:
//   - Appends a message to the session (via Dispatcher →
//     sessionManager.SendMessageWithAttachments).
//   - Spawns the broker.Publish goroutine inside Dispatcher when the
//     broker is configured.
//   - Writes the updated session as JSON.
func (s *Server) handleSessionMessage(w http.ResponseWriter, r *http.Request) {
	if s.sessionManager == nil {
		http.Error(w, errSessionManagerNotConfigured, http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	id := r.PathValue("id")
	type reqBody struct {
		Content       string   `json:"content"`
		AttachmentIDs []string `json:"attachmentIds,omitempty"`
	}
	var req reqBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Content == "" {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if s.completionOrchestrator != nil {
		s.completionOrchestrator.ResetRePromptCount(id)
	}
	// Resolve the agent fallback the same way SendMessage does:
	// CurrentAgentID overrides AgentID per the agent-stamping-asymmetry
	// fix at session/manager.go:1217-1220. Dispatcher uses this as the
	// fallback when no in-content @-mention resolves to a swarm.
	snap, snapErr := s.sessionManager.SnapshotSession(id)
	if snapErr != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	agentID := snap.AgentID
	if snap.CurrentAgentID != "" {
		agentID = snap.CurrentAgentID
	}

	handle, err := s.dispatcher.DispatchSessioned(r.Context(), dispatch.DispatchRequest{
		SessionID:     id,
		AgentID:       agentID,
		Content:       req.Content,
		AttachmentIDs: req.AttachmentIDs,
		ScanMentions:  true,
	}, nil)
	if err != nil {
		if errors.Is(err, session.ErrAttachmentNotFound) {
			http.Error(w, "attachment id not found in session", http.StatusBadRequest)
			return
		}
		// Phase 2 — Turn-Based Post-Then-Poll Architecture (May 2026).
		// dispatch.ErrTurnConflict surfaces when a second POST hits a
		// session whose prior Turn is still StatusRunning. Per the
		// plan's "User-chosen design" section, v1 supports ONE in-flight
		// turn per session; the wire contract is HTTP 409 Conflict.
		if errors.Is(err, dispatch.ErrTurnConflict) {
			http.Error(w, "a turn is already running for this session", http.StatusConflict)
			return
		}
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	// Phase 2 response shape: legacy SessionResponse fields stay at the
	// top level (so pre-Phase-3 clients keep working) PLUS two new
	// additive fields:
	//   - `turn_id` — the freshly-minted Turn UUID; the Phase 3 frontend
	//     drives `GET /api/v1/sessions/{id}/turns/{turn_id}` off this.
	//   - `snapshot` — a nested copy of the same SessionResponse, so
	//     the Phase 3 frontend can read `snapshot.messages` without
	//     coupling to the legacy flat shape. Future deprecation of
	//     the top-level fields lands in Phase 4 once the frontend is
	//     fully migrated.
	//
	// Both shapes ship intentionally — the plan's wire contract calls
	// for `{turn_id, snapshot}` while the regression-preservation
	// constraint forbids removing the existing top-level fields. The
	// additive approach satisfies both without breaking the Vue UI.
	snapshot := NewSessionResponse(&handle.Snapshot)
	writeJSON(w, sessionMessageResponse{
		SessionResponse: snapshot,
		TurnID:          handle.TurnID,
		Snapshot:        snapshot,
	})
}

// sessionMessageResponse is the Phase-2 wire shape for POST
// /api/v1/sessions/{id}/messages. It pairs the freshly-minted turn_id
// (so the client can drive GET /turns/{turn_id}) with the synchronously-
// returned session snapshot (so the user message renders without a
// poll round-trip).
//
// Backwards compatibility: the embedded *SessionResponse flattens the
// legacy top-level keys (`messages`, `agentId`, `messageCount`, ...)
// so pre-Phase-3 callers see the same response shape they always
// have. The new `turn_id` and `snapshot` keys are additive — unknown
// to the legacy clients, load-bearing for the Phase 3 frontend.
type sessionMessageResponse struct {
	*SessionResponse
	TurnID   string           `json:"turn_id"`
	Snapshot *SessionResponse `json:"snapshot"`
}

// turnResponse is the wire shape for GET /api/v1/sessions/{id}/turns/{turn_id}.
// Mirrors internal/turn/Turn but with explicit JSON tags so the wire
// contract is owned by this package and decoupled from the internal
// type's evolution.
//
// Fields:
//   - turn_id — the Turn UUID minted by DispatchSessioned.
//   - session_id — the session this turn belongs to.
//   - status — running | completed | failed.
//   - started_at — ISO-8601 timestamp of Turn.Start.
//   - completed_at — ISO-8601 timestamp of Turn.Complete / Turn.Fail;
//     null when status is running.
//   - model — {provider, model} pair the turn ran under; populated on
//     Complete, empty during Running.
//   - error — non-empty when status is failed; empty otherwise.
//   - messages — engine-emitted rows persisted during the turn
//     (assistant, thinking, tool_call, tool_result, delegation).
//     Excludes the user message that triggered the turn — that lives
//     in the POST response's snapshot.
type turnResponse struct {
	TurnID      string            `json:"turn_id"`
	SessionID   string            `json:"session_id"`
	Status      string            `json:"status"`
	StartedAt   time.Time         `json:"started_at"`
	CompletedAt *time.Time        `json:"completed_at"`
	Model       turn.ModelInfo    `json:"model"`
	Error       string            `json:"error,omitempty"`
	Messages    []session.Message `json:"messages"`
	// Phase + TokenCount surface the engine's most-recent streaming
	// heartbeat onto the polling wire (Phase-4-Commit-1). Populated by
	// the engine bus subscriber via registry.SetHeartbeat. Empty Phase
	// and zero TokenCount are the pre-first-heartbeat state. Replaces
	// the SSE side-channel's `streaming.heartbeat` frames once Commit 2
	// retires the existing event_bridge.go subscriber — Commit 1 ships
	// both surfaces so the frontend can migrate without a half-state.
	//
	// Plan ref: ~/vaults/baphled/1. Projects/FlowState/Plans/
	//   Turn-Based Post-Then-Poll Architecture (May 2026).md §4d Commit 1.
	Phase      string `json:"phase"`
	TokenCount int    `json:"token_count"`
	// CurrentProvider + CurrentModel surface the engine's live (provider,
	// model) pair onto the polling wire (Phase-5 §1c-α). Populated by the
	// dispatcher's wrapWithTurnLifecycle chunk-tap on `model_active` and
	// `provider_changed` events via registry.SetProviderModel. Empty
	// during the brief window between Start and the first model_active
	// chunk; frozen at the last live value once the Turn reaches a
	// terminal state. Distinct from Model — Model is the post-Complete
	// snapshot; CurrentProvider/CurrentModel surface the value DURING
	// Running so the chat-UI's toolbar chip pivots without an SSE
	// side-channel.
	//
	// Plan ref: ~/vaults/baphled/1. Projects/FlowState/Plans/
	//   Phase-5 Turn-Endpoint Event-Type Parity (May 2026).md §1c-α.
	CurrentProvider string `json:"current_provider,omitempty"`
	CurrentModel    string `json:"current_model,omitempty"`
	// ContextUsage + ProviderQuotas surface the engine's `context_usage`
	// and `provider_quota` chunk payloads onto the polling wire
	// (Phase-5 §1c-β). Populated by the dispatcher's wrapWithTurnLifecycle
	// chunk-tap on the matching event types via registry.SetContextUsage
	// and registry.UpsertProviderQuota. Both fields are emitted with
	// `omitempty` — pre-1c-β servers and pre-first-chunk Turn states
	// omit them from the JSON entirely so the FE parser sees `undefined`
	// rather than a misleading zero-valued payload.
	//
	// ContextUsage is a pointer because the absent state must be
	// unambiguously distinguishable from a zero-figure payload. The
	// FE's chip-gate reads "no payload" vs "limit=0 payload" differently.
	//
	// ProviderQuotas is a slice — multiple partitions can accumulate
	// across a single Turn (failover or @-mention swarm hop), and the
	// FE's quotaStore subscribes to per-partition updates.
	//
	// Plan ref: ~/vaults/baphled/1. Projects/FlowState/Plans/
	//   Phase-5 Turn-Endpoint Event-Type Parity (May 2026).md §1c-β.
	ContextUsage   *turn.ContextUsage            `json:"context_usage,omitempty"`
	ProviderQuotas []turn.ProviderQuotaSnapshot `json:"provider_quotas,omitempty"`
	// CompactionEvents + GateFailures + CriticalError surface the
	// remaining SSE-only event projections onto the polling wire
	// (Phase-5 §1c-γ). The first two come from bus subscribers wired in
	// subscribeTurnContextCompacted / subscribeTurnGateFailed mirroring
	// the existing subscribeTurnHeartbeat pattern; CriticalError comes
	// from the dispatcher's chunk-tap on chunk.Error when classified as
	// SeverityCritical.
	//
	// Each is `omitempty` — pre-1c-γ servers and pre-first-event Turn
	// states omit the field entirely so the FE poll-diff treats absent
	// as "no transition yet" rather than "real empty signal".
	//
	// Plan ref: ~/vaults/baphled/1. Projects/FlowState/Plans/
	//   Phase-5 Turn-Endpoint Event-Type Parity (May 2026).md §1c-γ.
	CompactionEvents []turn.CompactionEvent  `json:"compaction_events,omitempty"`
	GateFailures     []turn.GateFailure      `json:"gate_failures,omitempty"`
	CriticalError    *turn.TurnCriticalError `json:"critical_error,omitempty"`
}

// handleGetTurn returns the current state of a Turn by its UUID.
//
// Phase 2 of "Turn-Based Post-Then-Poll Architecture (May 2026)".
// The frontend polls this endpoint between POST /messages and the
// terminal status transition; each poll returns the Turn's current
// MessagesAdded slice so the client appends the delta to local state.
//
// Expected:
//   - Request path parameter "id" is the session id (kept for route
//     symmetry with sibling session endpoints — the registry is keyed
//     by turn_id alone so "id" is not used for the lookup).
//   - Request path parameter "turn_id" is the Turn UUID returned by
//     POST /messages.
//
// Returns:
//   - 200 OK with turnResponse JSON when the turn is found.
//   - 404 Not Found when the turn_id is unknown (predates server
//     restart OR never existed).
//   - 501 Not Implemented when the dispatcher's TurnRegistry is nil
//     (test wiring without a real Dispatcher).
//
// Side effects:
//   - None — read-only against the in-memory Turn registry.
// longPollTimeout is the maximum time handleGetTurn holds a wait=true
// request before returning the current snapshot. Sized at 25s so the
// handler returns well within the 30s nginx / proxy default keep-alive
// budget, and matches the canonical long-poll cadence used by
// Anthropic SDK / OpenAI Assistants polling consumers. The FE's
// long-poll loop re-issues immediately on every return so a 25s tick
// without a server-side mutation costs one round-trip per 25s.
//
// Plan ref: ~/vaults/baphled/1. Projects/FlowState/Plans/
//   Turn-Based Post-Then-Poll Architecture (May 2026).md §4d Commit 1b.
const longPollTimeout = 25 * time.Second

func (s *Server) handleGetTurn(w http.ResponseWriter, r *http.Request) {
	if s.dispatcher == nil {
		http.Error(w, "dispatcher not configured", http.StatusNotImplemented)
		return
	}
	registry := s.dispatcher.TurnRegistry()
	if registry == nil {
		http.Error(w, "turn registry not configured", http.StatusNotImplemented)
		return
	}
	turnID := r.PathValue("turn_id")
	if turnID == "" {
		http.Error(w, "turn_id required", http.StatusBadRequest)
		return
	}

	// Long-poll path (Phase-4-Commit-1b). When ?wait=true is set, the
	// handler holds the request until ANY of the watched fields move
	// past the caller's baseline, OR the Turn reaches a terminal state,
	// OR 25s elapses, OR the client disconnects. The baseline comes from
	// the ?since query param (last-seen MessagesAdded count); Phase +
	// TokenCount baselines are read off the current snapshot under lock
	// so the wait observes any change against the moment the wait starts.
	//
	// The default (?wait absent OR != "true") preserves the pre-long-poll
	// behaviour: snapshot-read, immediate return. The FE's legacy 250ms
	// loop continues to work against the non-wait path.
	if r.URL.Query().Get("wait") == "true" {
		// Headers MUST be set BEFORE writeJSON so proxies see them on
		// the response that actually carries the body. nginx's default
		// behaviour buffers responses; without `X-Accel-Buffering: no`
		// a long-poll response that ships its body quickly after a 25s
		// hold could still be delayed at the proxy.
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

		// Capture baselines under the registry's lock by reading a fresh
		// snapshot synchronously. WaitForChange then watches against
		// these baselines.
		sinceCount := 0
		if raw := r.URL.Query().Get("since"); raw != "" {
			if v, err := strconv.Atoi(raw); err == nil && v >= 0 {
				sinceCount = v
			}
		}
		// We need the current Phase + TokenCount as baselines too.
		// Read the snapshot through Get (which also surfaces the 404
		// not-found path); this is one extra lock acquisition but the
		// registry's contention is negligible at v1 scale (a few calls
		// per second per session).
		baseline, err := registry.Get(turnID)
		if err != nil {
			if errors.Is(err, turn.ErrTurnNotFound) {
				http.Error(w, "turn not found", http.StatusNotFound)
				return
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		t, _ := registry.WaitForChange(
			r.Context(),
			turnID,
			sinceCount,
			baseline.Phase,
			baseline.TokenCount,
			baseline.CurrentProvider,
			baseline.CurrentModel,
			baseline.ContextUsage,
			baseline.ProviderQuotas,
			len(baseline.CompactionEvents),
			len(baseline.GateFailures),
			baseline.CriticalError,
			longPollTimeout,
		)
		// ctx-cancel path returns the zero snapshot — t.ID == "" iff
		// the wait aborted via client disconnect. Drop the response
		// entirely; the client is gone and writing a body would be a
		// best-effort wasted operation.
		if t.ID == "" {
			return
		}
		msgs := t.MessagesAdded
		if msgs == nil {
			msgs = []session.Message{}
		}
		writeJSON(w, turnResponse{
			TurnID:           t.ID,
			SessionID:        t.SessionID,
			Status:           string(t.Status),
			StartedAt:        t.StartedAt,
			CompletedAt:      t.CompletedAt,
			Model:            t.Model,
			Error:            t.Error,
			Messages:         msgs,
			Phase:            t.Phase,
			TokenCount:       t.TokenCount,
			CurrentProvider:  t.CurrentProvider,
			CurrentModel:     t.CurrentModel,
			ContextUsage:     t.ContextUsage,
			ProviderQuotas:   t.ProviderQuotas,
			CompactionEvents: t.CompactionEvents,
			GateFailures:     t.GateFailures,
			CriticalError:    t.CriticalError,
		})
		return
	}

	t, err := registry.Get(turnID)
	if err != nil {
		if errors.Is(err, turn.ErrTurnNotFound) {
			http.Error(w, "turn not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	msgs := t.MessagesAdded
	if msgs == nil {
		msgs = []session.Message{}
	}
	writeJSON(w, turnResponse{
		TurnID:           t.ID,
		SessionID:        t.SessionID,
		Status:           string(t.Status),
		StartedAt:        t.StartedAt,
		CompletedAt:      t.CompletedAt,
		Model:            t.Model,
		Error:            t.Error,
		Messages:         msgs,
		Phase:            t.Phase,
		TokenCount:       t.TokenCount,
		CurrentProvider:  t.CurrentProvider,
		CurrentModel:     t.CurrentModel,
		ContextUsage:     t.ContextUsage,
		ProviderQuotas:   t.ProviderQuotas,
		CompactionEvents: t.CompactionEvents,
		GateFailures:     t.GateFailures,
		CriticalError:    t.CriticalError,
	})
}

// handleSessionTodos returns the todo list for the given session as JSON.
//
// Expected:
//   - Request path parameter "id" contains the session identifier.
//
// Returns:
//   - HTTP 200 with a JSON array of todo.Item values.
//   - An empty array when no store is configured or the session has no todos.
//
// Side effects:
//   - None.
func (s *Server) handleSessionTodos(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.todoStore == nil {
		writeJSON(w, []todo.Item{})
		return
	}
	writeJSON(w, s.todoStore.Get(id))
}

// handleListSessions writes all available sessions as JSON to the response.
//
// Expected:
//   - None.
//
// Side effects:
//   - Writes HTTP 200 response with JSON-encoded sessions list.
//   - Returns empty list if session store is disabled.
func (s *Server) handleListSessions(w http.ResponseWriter, _ *http.Request) {
	if s.sessions == nil {
		writeJSON(w, []ctxstore.SessionInfo{})
		return
	}
	sessions := s.sessions.List()
	if sessions == nil {
		sessions = []ctxstore.SessionInfo{}
	}
	writeJSON(w, sessions)
}

// handleIndex redirects requests to the Vue SPA /chat route.
//
// Expected:
//   - None.
//
// Returns:
//   - 302 redirect to /chat for SPA routing.
//
// Side effects:
//   - Writes redirect response.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/chat", http.StatusFound)
}

// handleSessionChildren returns the direct child sessions of the given session.
//
// Expected:
//   - r.PathValue("id") is a valid session ID.
//
// Returns:
//   - 200 with JSON array of child sessions.
//   - 404 if the session has no children or does not exist.
//   - 501 if no session manager is configured.
//
// Side effects:
//   - None.
func (s *Server) handleSessionChildren(w http.ResponseWriter, r *http.Request) {
	if s.sessionManager == nil {
		http.Error(w, errSessionManagerNotConfigured, http.StatusNotImplemented)
		return
	}
	id := r.PathValue("id")
	children, err := s.sessionManager.ChildSessions(id)
	if err != nil {
		writeJSONError(w, err, "session_not_found", http.StatusNotFound)
		return
	}
	writeJSON(w, children)
}

// handleSessionTree returns the full session hierarchy rooted at the given session.
//
// Expected:
//   - r.PathValue("id") is a valid session ID.
//
// Returns:
//   - 200 with JSON array of sessions in depth-first order.
//   - 404 if the session does not exist.
//   - 501 if no session manager is configured.
//
// Side effects:
//   - None.
func (s *Server) handleSessionTree(w http.ResponseWriter, r *http.Request) {
	if s.sessionManager == nil {
		http.Error(w, errSessionManagerNotConfigured, http.StatusNotImplemented)
		return
	}
	id := r.PathValue("id")
	tree, err := s.sessionManager.SessionTree(id)
	if err != nil {
		writeJSONError(w, err, "session_not_found", http.StatusNotFound)
		return
	}
	writeJSON(w, tree)
}

// handleSessionParent returns the parent session of the given session.
//
// Expected:
//   - r.PathValue("id") is a valid session ID.
//
// Returns:
//   - 200 with JSON parent session.
//   - 404 if the session is a root session or does not exist.
//   - 501 if no session manager is configured.
//
// Side effects:
//   - None.
func (s *Server) handleSessionParent(w http.ResponseWriter, r *http.Request) {
	if s.sessionManager == nil {
		http.Error(w, errSessionManagerNotConfigured, http.StatusNotImplemented)
		return
	}
	id := r.PathValue("id")
	root, err := s.sessionManager.GetRootSession(id)
	if err != nil {
		writeJSONError(w, err, "session_not_found", http.StatusNotFound)
		return
	}
	writeJSON(w, root)
}

// handleListTasks returns all background tasks.
//
// Expected:
//   - None.
//
// Returns:
//   - 200 with JSON array of tasks.
//   - 501 if no background manager is configured.
//
// Side effects:
//   - None.
func (s *Server) handleListTasks(w http.ResponseWriter, _ *http.Request) {
	if s.backgroundManager == nil {
		http.Error(w, errBackgroundManagerNotConfigured, http.StatusNotImplemented)
		return
	}
	writeJSON(w, s.backgroundManager.List())
}

// handleGetTask returns a single background task by ID.
//
// Expected:
//   - r.PathValue("id") is a valid task ID.
//
// Returns:
//   - 200 with JSON task.
//   - 404 if the task does not exist.
//   - 501 if no background manager is configured.
//
// Side effects:
//   - None.
func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	if s.backgroundManager == nil {
		http.Error(w, errBackgroundManagerNotConfigured, http.StatusNotImplemented)
		return
	}
	id := r.PathValue("id")
	task, ok := s.backgroundManager.Get(id)
	if !ok {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	writeJSON(w, task)
}

// handleCancelTask cancels a specific background task.
//
// Expected:
//   - r.PathValue("id") is a valid task ID.
//
// Returns:
//   - 204 on success.
//   - 404 if the task does not exist.
//   - 501 if no background manager is configured.
//
// Side effects:
//   - Cancels the specified task.
func (s *Server) handleCancelTask(w http.ResponseWriter, r *http.Request) {
	if s.backgroundManager == nil {
		http.Error(w, errBackgroundManagerNotConfigured, http.StatusNotImplemented)
		return
	}
	id := r.PathValue("id")
	if err := s.backgroundManager.Cancel(id); err != nil {
		writeJSONError(w, err, "cancel_error", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleCancelAllTasks cancels all background tasks when ?all=true is set.
//
// Expected:
//   - r.URL.Query().Get("all") == "true" to cancel all tasks.
//
// Returns:
//   - 204 on success.
//   - 400 if ?all=true is not set.
//   - 501 if no background manager is configured.
//
// Side effects:
//   - Cancels all running tasks when ?all=true.
func (s *Server) handleCancelAllTasks(w http.ResponseWriter, r *http.Request) {
	if s.backgroundManager == nil {
		http.Error(w, errBackgroundManagerNotConfigured, http.StatusNotImplemented)
		return
	}
	if r.URL.Query().Get("all") != "true" {
		http.Error(w, `{"error":"pass ?all=true to cancel all tasks"}`, http.StatusBadRequest)
		return
	}
	s.backgroundManager.CancelAll()
	w.WriteHeader(http.StatusNoContent)
}

// healthResponse represents the JSON payload returned by the health endpoint.
type healthResponse struct {
	Status string `json:"status"`
}

// handleHealth returns a simple health check response indicating the server is running.
//
// Expected:
//   - w is a valid http.ResponseWriter.
//
// Returns:
//   - 200 with JSON {"status":"ok"}.
//
// Side effects:
//   - None.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, healthResponse{Status: "ok"})
}

// handleSwarmEvents streams swarm activity events as SSE using the same projection
// logic as the TUI chat intent.
//
// This handler subscribes to the EventBus and projects tool execute,
// delegation, and background task events into the SwarmEvent format that the
// frontend expects.
//
// QA bughunt 2026-05-08, H5: this handler must be scoped to a single session.
// Pre-fix it subscribed globally with no auth and no filter, leaking SessionID
// + tool name + result body + delegation chains across tenants to any
// anonymous client. Callers must pass ?session_id=<id>; events are filtered
// such that only events whose underlying SessionID (tool/background) or
// Parent/Child SessionID (delegation, where the chain spans two surfaces)
// matches the requested session reach the wire.
func (s *Server) handleSwarmEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		// H5: anonymous global subscription is the leak. Until auth lands
		// (see vault Bug Hunt Findings (May 2026) §"H5"), require an explicit
		// session scope on every request. A future debug flag can re-enable
		// global view for ops tooling.
		http.Error(w, "session_id query parameter is required", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusNotImplemented)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// PR3/C7 round-6 fold: replace the pre-PR3
	// `Access-Control-Allow-Origin: *` with the first configured
	// allowlist origin + Allow-Credentials: true, so the SPA can send
	// the session cookie on cross-origin SSE. With no allowlist
	// configured we emit nothing — relying on same-origin (the most
	// common deployment shape — server + SPA same host) rather than
	// falling back to "*" which would re-open the gap PR1 flagged.
	if len(s.originPatterns) > 0 {
		w.Header().Set("Access-Control-Allow-Origin", s.originPatterns[0])
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}

	if s.eventBus == nil {
		writeSSE(w, flusher, `{"error":"event bus not configured"}`)
		flusher.Flush()
		return
	}

	eventCh := make(chan interface{}, 64)
	stopCh := make(chan struct{})

	// Non-blocking forwarder: the bus.Publish caller is the engine, the tool
	// executor, or any other publisher goroutine. A naive `eventCh <- msg`
	// blocks Publish indefinitely when the SSE consumer is slow or has
	// disconnected mid-publish (deferred Unsubscribe still in-flight). One
	// stalled swarm-events client would then DoS every tool execution across
	// every session (QA bughunt 2026-05-08, C3) and leak goroutines under
	// disconnect churn (H6, same root cause).
	//
	// The session-scope filter (H5) runs BEFORE the channel send so that
	// other tenants' events do not even consume eventCh capacity, and the
	// select+default pattern matches the prevailing approach in
	// event_bridge.go's WebSocket handlers: drop on full / on disconnect
	// rather than block the bus dispatcher.
	forward := func(msg any) {
		if !eventBelongsToSession(msg, sessionID) {
			return
		}
		select {
		case eventCh <- msg:
		case <-stopCh:
		default:
			// eventCh full — drop the event rather than wedge Publish.
		}
	}
	handlers := map[string]eventbus.EventHandler{
		"tool.execute.before":       forward,
		"tool.execute.result":       forward,
		"tool.execute.error":        forward,
		"background.task.started":   forward,
		"background.task.completed": forward,
		"background.task.failed":    forward,
		"delegation.started":        forward,
		"delegation.completed":      forward,
		"delegation.failed":         forward,
	}

	for topic, handler := range handlers {
		s.eventBus.Subscribe(topic, handler)
	}

	defer func() {
		close(stopCh)
		for topic, handler := range handlers {
			s.eventBus.Unsubscribe(topic, handler)
		}
	}()

	writeSSE(w, flusher, `{"type":"connected"}`)
	flusher.Flush()

	for {
		select {
		case <-stopCh:
			writeSSE(w, flusher, `{"type":"done"}`)
			flusher.Flush()
			return
		case ev := <-eventCh:
			swarmEv := projectSwarmEvent(ev)
			if swarmEv.ID == "" {
				continue
			}
			jsonData, err := json.Marshal(swarmEv)
			if err != nil {
				continue
			}
			writeSSE(w, flusher, string(jsonData))
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// eventBelongsToSession reports whether the given bus event is in scope for
// the supplied session id, used by handleSwarmEvents to enforce H5
// (cross-tenant leak).
//
// Tool execute, background task, and tool start/end events all carry a single
// SessionID field — match it directly. Delegation events span two sessions
// (parent and child); both surfaces have a legitimate need to observe the
// chain (the parent surface renders progress; the child surface renders the
// upstream agent + chain id), so an event matches when EITHER endpoint is the
// requested session.
//
// Unknown event types fall through to "no" rather than "yes" — the safer
// default for a leak-fix predicate.
func eventBelongsToSession(msg any, sessionID string) bool {
	switch e := msg.(type) {
	case *events.ToolExecuteResultEvent:
		return e.Data.SessionID == sessionID
	case *events.ToolExecuteErrorEvent:
		return e.Data.SessionID == sessionID
	case *events.ToolEvent:
		return e.Data.SessionID == sessionID
	case *events.BackgroundTaskStartedEvent:
		return e.Data.SessionID == sessionID
	case *events.BackgroundTaskCompletedEvent:
		return e.Data.SessionID == sessionID
	case *events.BackgroundTaskFailedEvent:
		return e.Data.SessionID == sessionID
	case *events.DelegationStartedEvent:
		return e.Data.ParentSessionID == sessionID || e.Data.ChildSessionID == sessionID
	case *events.DelegationCompletedEvent:
		return e.Data.ParentSessionID == sessionID || e.Data.ChildSessionID == sessionID
	case *events.DelegationFailedEvent:
		return e.Data.ParentSessionID == sessionID || e.Data.ChildSessionID == sessionID
	default:
		return false
	}
}

// projectSwarmEvent converts an EventBus event into a SwarmEvent for the frontend.
//
// Plans/Tool Execute Bus Bridge — Engine to SSE (May 2026) §"Web SSE consumer".
// The tool.execute.* variants now key on InternalToolCallID (the FlowState
// session-scoped, failover-stable correlation id) and expose the upstream
// provider tool-use id alongside the result body in metadata. The legacy
// <sessionID>:<toolName> id-fabrication remains as a defensive fallback for
// any code path that does not yet route through executeToolCall.
//
// Every projected event is stamped with streaming.CurrentSchemaVersion — the
// pre-bridge projector forgot to set this and shipped SchemaVersion: 0 events
// to the wire; corrected here in passing.
func projectSwarmEvent(ev interface{}) streaming.SwarmEvent {
	switch e := ev.(type) {
	case *events.ToolExecuteResultEvent:
		id := e.Data.InternalToolCallID
		if id == "" {
			id = e.Data.SessionID + ":" + e.Data.ToolName
		}
		metadata := map[string]interface{}{
			"tool_name": e.Data.ToolName,
			"ok":        true,
			"content":   e.Data.Result,
		}
		if e.Data.ToolCallID != "" {
			metadata["provider_tool_use_id"] = e.Data.ToolCallID
		}
		return streaming.SwarmEvent{
			ID:            id,
			Type:          streaming.EventToolResult,
			Status:        "completed",
			AgentID:       e.Data.SessionID,
			Timestamp:     time.Now(),
			SchemaVersion: streaming.CurrentSchemaVersion,
			Metadata:      metadata,
		}
	case *events.ToolExecuteErrorEvent:
		id := e.Data.InternalToolCallID
		if id == "" {
			id = e.Data.SessionID + ":" + e.Data.ToolName
		}
		errMsg := ""
		if e.Data.Error != nil {
			errMsg = e.Data.Error.Error()
		}
		metadata := map[string]interface{}{
			"tool_name": e.Data.ToolName,
			"ok":        false,
			"error":     errMsg,
		}
		if e.Data.ToolCallID != "" {
			metadata["provider_tool_use_id"] = e.Data.ToolCallID
		}
		return streaming.SwarmEvent{
			ID:            id,
			Type:          streaming.EventToolResult,
			Status:        "error",
			AgentID:       e.Data.SessionID,
			Timestamp:     time.Now(),
			SchemaVersion: streaming.CurrentSchemaVersion,
			Metadata:      metadata,
		}
	case *events.ToolEvent:
		id := e.Data.InternalToolCallID
		if id == "" {
			id = e.Data.SessionID + ":" + e.Data.ToolName
		}
		metadata := map[string]interface{}{
			"tool_name": e.Data.ToolName,
		}
		if e.Data.ToolCallID != "" {
			metadata["provider_tool_use_id"] = e.Data.ToolCallID
		}
		return streaming.SwarmEvent{
			ID:            id,
			Type:          streaming.EventToolCall,
			Status:        "started",
			AgentID:       e.Data.SessionID,
			Timestamp:     time.Now(),
			SchemaVersion: streaming.CurrentSchemaVersion,
			Metadata:      metadata,
		}
	case *events.BackgroundTaskStartedEvent:
		return streaming.SwarmEvent{
			ID:        e.Data.TaskID,
			Type:      streaming.EventPlan,
			Status:    "started",
			AgentID:   e.Data.SessionID,
			Timestamp: time.Now(),
			Metadata: map[string]interface{}{
				"name": e.Data.Name,
			},
		}
	case *events.BackgroundTaskCompletedEvent:
		return streaming.SwarmEvent{
			ID:        e.Data.TaskID,
			Type:      streaming.EventPlan,
			Status:    "completed",
			AgentID:   e.Data.SessionID,
			Timestamp: time.Now(),
			Metadata: map[string]interface{}{
				"name": e.Data.Name,
			},
		}
	case *events.BackgroundTaskFailedEvent:
		return streaming.SwarmEvent{
			ID:        e.Data.TaskID,
			Type:      streaming.EventPlan,
			Status:    "failed",
			AgentID:   e.Data.SessionID,
			Timestamp: time.Now(),
			Metadata: map[string]interface{}{
				"name":  e.Data.Name,
				"error": e.Data.Error,
			},
		}
	case *events.DelegationStartedEvent:
		return projectDelegationEvent(e.Data, "started", e.Timestamp())
	case *events.DelegationCompletedEvent:
		return projectDelegationEvent(e.Data, "completed", e.Timestamp())
	case *events.DelegationFailedEvent:
		return projectDelegationEvent(e.Data, "failed", e.Timestamp())
	}
	return streaming.SwarmEvent{}
}

// projectDelegationEvent converts a DelegationEventData payload into the
// on-the-wire `streaming.SwarmEvent` shape the Vue surface consumes. The
// `metadata.child_session_id` field is the load-bearing slot the Vue
// `DelegationPanel.vue` clicks through on; the other metadata keys are
// forward-compatible decoration the frontend ignores today.
//
// Expected:
//   - data carries the in-process bus payload populated by the engine.
//   - status is one of "started", "completed", "failed".
//   - ts is the bus event's timestamp; preserved on the wire so client-side
//     ordering matches engine-side firing order.
//
// Returns:
//   - A populated SwarmEvent ready for SSE emission.
//
// Side effects:
//   - None.
func projectDelegationEvent(data events.DelegationEventData, status string, ts time.Time) streaming.SwarmEvent {
	metadata := map[string]interface{}{
		"child_session_id":  data.ChildSessionID,
		"parent_session_id": data.ParentSessionID,
		"source_agent":      data.SourceAgent,
		"description":       data.Description,
	}
	if data.ModelName != "" {
		metadata["model_name"] = data.ModelName
	}
	if data.ProviderName != "" {
		metadata["provider_name"] = data.ProviderName
	}
	if data.ToolCalls > 0 {
		metadata["tool_calls"] = data.ToolCalls
	}
	if data.LastTool != "" {
		metadata["last_tool"] = data.LastTool
	}
	if data.Error != "" {
		metadata["error"] = data.Error
	}
	// Gap 1 (load_skills propagation, May 2026): the Vue
	// DelegationPanel renders a delegation-skills-row chip block for
	// every skill name in metadata.load_skills. Surface the bus event's
	// LoadSkills onto the wire as a JSON array; omit the key entirely
	// when the slice is empty so the chip-render gate (length>0) keeps
	// the row hidden for delegations that did not pass load_skills.
	if len(data.LoadSkills) > 0 {
		metadata["load_skills"] = data.LoadSkills
	}
	return streaming.SwarmEvent{
		ID:            data.ChainID,
		Type:          streaming.EventDelegation,
		Status:        status,
		AgentID:       data.TargetAgent,
		Timestamp:     ts,
		SchemaVersion: streaming.CurrentSchemaVersion,
		Metadata:      metadata,
	}
}

// writeJSON encodes data as JSON and writes it to the response with HTTP 200.
//
// Expected:
//   - data is a value that can be marshalled to JSON.
//
// Side effects:
//   - Sets Content-Type header to application/json.
//   - Writes HTTP 200 status code.
//   - Writes JSON-encoded data to response body.
func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		return
	}
}

// handleSessionMessages returns the messages for the given session as JSON.
//
// Expected:
//   - Request path parameter "id" contains the session identifier.
//
// Side effects:
//   - Writes HTTP 200 with JSON-encoded messages.
func (s *Server) handleSessionMessages(w http.ResponseWriter, r *http.Request) {
	if s.sessionManager == nil {
		http.Error(w, errSessionManagerNotConfigured, http.StatusNotImplemented)
		return
	}
	id := r.PathValue("id")
	// SnapshotSession deep-copies the Messages slice under RLock so
	// the read here never races with SendMessage's append under
	// WLock. Pre-fix this site read sess.Messages on a *Session that
	// had escaped the lock boundary — same anti-pattern as the SSE
	// fast-path race fixed in commit aaa6f1f.
	snap, err := s.sessionManager.SnapshotSession(id)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	messages := snap.Messages
	if messages == nil {
		messages = []session.Message{}
	}
	writeJSON(w, messages)
}

// handleDeleteSession removes a session entirely (in-memory + on-disk).
//
// Expected:
//   - Request path parameter "id" identifies a session.
//
// Returns:
//   - 204 No Content on success.
//   - 404 Not Found when the session does not exist.
//   - 501 Not Implemented when no session manager is configured.
//
// Side effects:
//   - Removes the session from the manager's in-memory map.
//   - Removes the session's .meta.json sidecar and .events.jsonl WAL from disk
//     when sessionsDir is configured.
//
// Backs the Vue UI's per-row trash button in SessionBrowser /
// SessionSwitcher. Closes Quick-wins QW-11.
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	if s.sessionManager == nil {
		http.Error(w, errSessionManagerNotConfigured, http.StatusNotImplemented)
		return
	}
	id := r.PathValue("id")
	if err := s.sessionManager.DeleteSession(id); err != nil {
		writeJSONError(w, err, "session_not_found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTruncateMessages truncates a session's message history at (and including)
// the specified message ID, enabling the frontend to repopulate the composer with
// the reverted message content for editing and re-sending.
//
// Expected:
//   - Request path parameter "id" contains the session identifier.
//   - Request path parameter "messageId" contains the ID of the message to truncate from.
//
// Returns:
//   - 204 No Content on success.
//   - 404 when the session or message is not found.
//   - 501 when no session manager is configured.
//
// Side effects:
//   - Removes the message and all subsequent messages from the session.
//   - Persists the truncated session to disk.
func (s *Server) handleTruncateMessages(w http.ResponseWriter, r *http.Request) {
	if s.sessionManager == nil {
		http.Error(w, errSessionManagerNotConfigured, http.StatusNotImplemented)
		return
	}
	id := r.PathValue("id")
	messageID := r.PathValue("messageId")
	if err := s.sessionManager.TruncateMessages(id, messageID); err != nil {
		writeJSONError(w, err, "session_not_found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleUpdateSessionAgent switches the active agent for an existing session.
//
// Expected:
//   - Request path parameter "id" identifies an existing session.
//   - Request body is JSON of the form {"agentId":"<id>"} with a non-empty agentId.
//
// Side effects:
//   - Sets the session's CurrentAgentID so subsequent SendMessage calls stream
//     through the new agent rather than the agent the session was created with.
//   - Writes the updated session as JSON.
func (s *Server) handleUpdateSessionAgent(w http.ResponseWriter, r *http.Request) {
	if s.sessionManager == nil {
		http.Error(w, errSessionManagerNotConfigured, http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	id := r.PathValue("id")
	var req struct {
		AgentID string `json:"agentId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.AgentID == "" {
		http.Error(w, "agentId is required", http.StatusBadRequest)
		return
	}
	// Per ADR - Session Orchestrator for Surface Parity §"SwitchAgent",
	// the API agent-switch fans out via the orchestrator so the engine
	// half (SetManifest) lands alongside the session-manager metadata
	// update. Pre-lift only the manager half ran on this endpoint —
	// the engine kept the stale manifest until the next stream call,
	// which made multi-turn web chat lose context whenever the user
	// switched agents (the session reported the new agent but the
	// engine streamed under the old one). Closes Audit Finding 3 (web
	// half).
	orch := orchestrator.New(s.dispatchEngine, s.registry, s.swarmRegistry, s.streamer, nil, s.sessionManager)
	if _, err := orch.SwitchAgent(r.Context(), id, req.AgentID); err != nil {
		// SessionManager.UpdateSessionAgent reports session-not-found
		// via the wrapped error — preserve the existing 404 contract
		// for the most common failure mode. Orchestrator.SwitchAgent
		// also returns ErrAgentNotFound for an unknown agent id; that
		// surfaces as a 404 too, matching the historical "session not
		// found" rendering of any UpdateSessionAgent failure on this
		// route.
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	// SnapshotSession instead of GetSession — see handleSessionMessage
	// for the rationale; identical pointer-leak race shape.
	snap, err := s.sessionManager.SnapshotSession(id)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	// Phase 3 — annotate the response with the post-switch
	// context_usage shape so the chat UI's chip ticks up to reflect
	// the new agent's preferred model / context limit without
	// waiting for the next pre-send streamed event.
	usage := s.contextUsageForSnapshot(&snap)
	writeJSON(w, NewSessionResponse(&snap, WithContextUsage(usage)))
}

// handleUpdateSessionModel switches the active provider+model pairing for a session.
//
// Expected:
//   - Request path parameter "id" identifies an existing session.
//   - Request body is JSON of the form {"modelId":"<id>","providerId":"<id>"}.
//
// Side effects:
//   - Sets the session's CurrentProviderID and CurrentModelID so subsequent
//     SendMessage calls stream through the selected provider/model.
//   - Writes the updated session as JSON via NewSessionResponse.
func (s *Server) handleUpdateSessionModel(w http.ResponseWriter, r *http.Request) {
	if s.sessionManager == nil {
		http.Error(w, errSessionManagerNotConfigured, http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	id := r.PathValue("id")
	var req struct {
		ModelID    string `json:"modelId"`
		ProviderID string `json:"providerId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.ModelID == "" {
		http.Error(w, "modelId is required", http.StatusBadRequest)
		return
	}
	if req.ProviderID == "" {
		http.Error(w, "providerId is required", http.StatusBadRequest)
		return
	}
	// Per ADR - Session Orchestrator for Surface Parity §"SwitchModel",
	// the API model-switch fans out via the orchestrator so the engine
	// half (SetModelPreference) lands alongside the session-manager
	// metadata update. Same parity gap as the agent route — closes
	// Audit Finding 3 (web half).
	orch := orchestrator.New(s.dispatchEngine, s.registry, s.swarmRegistry, s.streamer, nil, s.sessionManager)
	if err := orch.SwitchModel(r.Context(), id, req.ProviderID, req.ModelID); err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	// SnapshotSession instead of GetSession — see handleSessionMessage
	// for the rationale; identical pointer-leak race shape.
	snap, err := s.sessionManager.SnapshotSession(id)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	// Phase 3 — annotate the response with the post-switch
	// context_usage shape so the chat UI's chip pivots to the new
	// limit immediately rather than waiting for the next pre-send.
	usage := s.contextUsageForSnapshot(&snap)
	writeJSON(w, NewSessionResponse(&snap, WithContextUsage(usage)))
}

// compressionConfigResponse is the wire shape for the GET / PATCH
// /api/v1/config/compression endpoints. Mirrors the SettingsView
// slider's data binding so the round-trip is symmetric: GET returns
// the current threshold, PATCH writes a new threshold and echoes the
// stored value back. Only the soft trigger's ratio is exposed for
// runtime mutation — the other AutoCompaction knobs (Enabled flag,
// MicroCompaction layer, SessionMemory layer) remain config-file-
// only because flipping them at runtime has session-state
// implications outside the scope of Deliverable 2.
type compressionConfigResponse struct {
	Threshold float64 `json:"threshold"`
}

// handleGetCompressionConfig surfaces the engine's current auto-
// compaction soft-trigger threshold so the SettingsView slider can
// hydrate to the correct value on page load rather than guessing the
// default.
//
// Expected:
//   - A CompactionController has been installed via
//     WithCompactionController.
//
// Side effects:
//   - Writes the current threshold as JSON.
//   - Returns 501 when no controller is wired so callers can
//     distinguish "feature not built" from "feature built but
//     erroring".
func (s *Server) handleGetCompressionConfig(w http.ResponseWriter, _ *http.Request) {
	if s.compactionController == nil {
		http.Error(w, `{"error":"compaction controller not configured"}`, http.StatusNotImplemented)
		return
	}
	writeJSON(w, compressionConfigResponse{
		Threshold: s.compactionController.AutoCompactionThreshold(),
	})
}

// handleUpdateCompressionConfig updates the engine's auto-compaction
// threshold at runtime. The validation rules mirror
// CompressionConfig.Validate so the runtime knob cannot land the
// engine in a state the startup loader would have rejected.
//
// Expected:
//   - A CompactionController has been installed.
//   - Request body is JSON of the form {"threshold": 0.5}.
//
// Side effects:
//   - Calls SetAutoCompactionThreshold; on success the next soft-
//     trigger evaluation reads the new value.
//   - Writes the (now-current) threshold as JSON on success, or a
//     400 + diagnostic message on validation failure.
func (s *Server) handleUpdateCompressionConfig(w http.ResponseWriter, r *http.Request) {
	if s.compactionController == nil {
		http.Error(w, `{"error":"compaction controller not configured"}`, http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<14)
	var req struct {
		Threshold float64 `json:"threshold"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := s.compactionController.SetAutoCompactionThreshold(req.Threshold); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, compressionConfigResponse{
		Threshold: s.compactionController.AutoCompactionThreshold(),
	})
}

// compactNowResponse is the wire shape returned by POST
// /api/v1/sessions/{id}/compress. Carries the fired discriminant
// (true iff the compactor produced a non-empty summary) and the
// summary text on a fire. The Vue UI's `/compress` slash command
// branches on fired to choose between the "compacted X→Y" confirmation
// toast and the "nothing to compact" empty-state toast.
type compactNowResponse struct {
	Fired   bool   `json:"fired"`
	Summary string `json:"summary,omitempty"`
}

// handleCompactNow is the api seam for the /compress slash command
// and the SettingsView's "compact now" button. Routes through the
// CompactionController so the engine's locking discipline and the
// ContextCompactedEvent bus emission stay on the engine side.
//
// Expected:
//   - A CompactionController has been installed.
//   - The URL path id identifies the session to force-compact.
//
// Side effects:
//   - Invokes CompactNow on the controller, which may issue one
//     summariser LLM call and one ContextCompactedEvent bus emission.
//   - Writes the fired discriminant + optional summary as JSON.
func (s *Server) handleCompactNow(w http.ResponseWriter, r *http.Request) {
	if s.compactionController == nil {
		http.Error(w, `{"error":"compaction controller not configured"}`, http.StatusNotImplemented)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "session id is required", http.StatusBadRequest)
		return
	}
	summary, fired := s.compactionController.CompactNow(r.Context(), id)
	writeJSON(w, compactNowResponse{Fired: fired, Summary: summary})
}

// modelDescriptor is the wire shape for a single model entry in the
// GET /api/v1/models response.
type modelDescriptor struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// providerDescriptor groups models under their provider id.
type providerDescriptor struct {
	ID     string            `json:"id"`
	Models []modelDescriptor `json:"models"`
}

// modelsResponse is the wire shape returned by GET /api/v1/models.
type modelsResponse struct {
	Providers []providerDescriptor `json:"providers"`
}

// handleListModels returns the providers and models the engine knows about,
// reusing the same enumeration the `flowstate models` cobra command uses.
//
// Expected:
//   - A ModelLister has been installed via WithModelLister.
//
// Side effects:
//   - Writes the providers list as JSON.
//   - Returns 501 when no ModelLister is configured so callers can
//     distinguish "not wired" from "wired but empty".
func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	if s.modelLister == nil {
		http.Error(w, `{"error":"model lister not configured"}`, http.StatusNotImplemented)
		return
	}
	models, err := s.modelLister()
	if err != nil {
		http.Error(w, "failed to list models", http.StatusInternalServerError)
		return
	}
	grouped := make(map[string][]modelDescriptor)
	for _, m := range models {
		grouped[m.Provider] = append(grouped[m.Provider], modelDescriptor{ID: m.ID, Name: m.ID})
	}
	providerIDs := make([]string, 0, len(grouped))
	for id := range grouped {
		providerIDs = append(providerIDs, id)
	}
	sort.Strings(providerIDs)
	resp := modelsResponse{Providers: make([]providerDescriptor, 0, len(providerIDs))}
	for _, id := range providerIDs {
		resp.Providers = append(resp.Providers, providerDescriptor{ID: id, Models: grouped[id]})
	}
	writeJSON(w, resp)
}

// contextUsageForSnapshot returns the raw JSON payload of the engine's
// current context_usage figure for the given session snapshot, or an
// empty slice when the api server has no ContextUsageProvider wired or
// the engine cannot compute a meaningful figure. Centralised so the
// SSE-on-load and PATCH-response sites agree on the same projection
// semantics (session.Message → provider.Message + which fields carry).
//
// Expected:
//   - snap is the session snapshot to compute usage for. Read-only.
//
// Returns:
//   - The raw JSON payload as a byte slice ready to drop into a
//     SessionResponse.ContextUsage via WithContextUsage.
//   - An empty slice when the provider is unwired or returns
//     hasUsage=false.
//
// Side effects:
//   - None.
func (s *Server) contextUsageForSnapshot(snap *session.Session) []byte {
	if s == nil || s.contextUsageProvider == nil || snap == nil {
		return nil
	}
	providerMsgs := make([]provider.Message, 0, len(snap.Messages))
	for _, m := range snap.Messages {
		providerMsgs = append(providerMsgs, provider.Message{
			Role:           m.Role,
			Content:        m.Content,
			ThinkingBlocks: m.ThinkingBlocks,
			StopReason:     m.StopReason,
		})
	}
	payload, ok := s.contextUsageProvider.ContextUsageJSONForSession(
		snap.CurrentProviderID, snap.CurrentModelID, providerMsgs,
	)
	if !ok || payload == "" {
		return nil
	}
	return []byte(payload)
}

// parseVerbosityLevel converts a verbosity query parameter string to a streaming.VerbosityLevel.
// Unrecognised values default to Standard.
//
// Expected:
//   - s is the raw query parameter value from the request.
//
// Returns:
//   - The corresponding VerbosityLevel, or Standard for unknown values.
//
// Side effects:
//   - None.
func parseVerbosityLevel(s string) streaming.VerbosityLevel {
	switch s {
	case "minimal":
		return streaming.Minimal
	case "verbose":
		return streaming.Verbose
	default:
		return streaming.Standard
	}
}
