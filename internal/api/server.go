package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/discovery"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/orchestrator"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
	todo "github.com/baphled/flowstate/internal/tool/todo"
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
	sessionBroker          *SessionBroker
	todoStore              todo.Store
	backgroundManager      *engine.BackgroundTaskManager
	completionOrchestrator *engine.CompletionOrchestrator
	eventBus               *eventbus.EventBus
	metricsHandler         http.Handler
	modelLister            ModelLister
	contextUsageProvider   ContextUsageProvider
	compactionController   CompactionController
	mux                    *http.ServeMux
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

// WithSessionBroker sets the session broker for live event streaming.
//
// Expected:
//   - A valid session broker is provided.
//
// Returns:
//   - A ServerOption that installs the provided session broker.
//
// Side effects:
//   - None.
func WithSessionBroker(broker *SessionBroker) ServerOption {
	return func(s *Server) { s.sessionBroker = broker }
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

// WithEventBus configures the EventBus for forwarding operational events to WebSocket clients.
//
// Expected:
//   - bus is a non-nil EventBus from the engine.
//
// Returns:
//   - A ServerOption that sets the eventBus field.
//
// Side effects:
//   - None until subscribeSessionBus is called.
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

// SetSessionBroker sets the session broker for live event streaming.
//
// Expected:
//   - broker is a non-nil SessionBroker.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Updates the server's session broker reference.
func (s *Server) SetSessionBroker(broker *SessionBroker) {
	s.sessionBroker = broker
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

// SubscribeSessionBus exposes subscribeSessionBus for external use.
// It subscribes to EventBus events for the given session, forwarding sanitised
// summaries to the provided channel. The returned function stops forwarding when called.
//
// Expected:
//   - sessionID is a valid session identifier.
//   - out is a buffered channel for receiving WSChunkMsg values.
//
// Returns:
//   - An unsubscribe function that stops event forwarding.
//
// Side effects:
//   - Subscribes to EventBus events for the given session when the EventBus is configured.
func (s *Server) SubscribeSessionBus(sessionID string, out chan<- WSChunkMsg) func() {
	return s.subscribeSessionBus(sessionID, out)
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
	s.setupRoutes()
	return s
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
// Side effects:
//   - Registers GET /api/agents, GET /api/agents/{id}, POST /api/chat,
//     GET /api/discover, GET /api/skills, GET /api/sessions, and GET / routes.
//   - Wraps the mux with security headers middleware.
func (s *Server) setupRoutes() {
	s.mux.HandleFunc("GET /api/agents", s.handleListAgents)
	s.mux.HandleFunc("GET /api/agents/{id}", s.handleGetAgent)
	s.mux.HandleFunc("GET /api/swarms", s.handleListSwarms)
	s.mux.HandleFunc("POST /api/chat", s.handleChat)
	s.mux.HandleFunc("GET /api/discover", s.handleDiscover)
	s.mux.HandleFunc("GET /api/skills", s.handleListSkills)
	s.mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	s.mux.HandleFunc("GET /", s.handleIndex)
	s.mux.HandleFunc("POST /api/v1/sessions", s.handleCreateSession)
	s.mux.HandleFunc("GET /api/v1/sessions", s.handleListV1Sessions)
	s.mux.HandleFunc("POST /api/v1/sessions/{id}/messages", s.handleSessionMessage)
	s.mux.HandleFunc("GET /api/v1/sessions/{id}/stream", s.handleSessionStream)
	s.mux.HandleFunc("DELETE /api/v1/sessions/{id}/stream", s.handleCancelStream)
	s.mux.HandleFunc("GET /api/v1/sessions/{id}/messages", s.handleSessionMessages)
	s.mux.HandleFunc("GET /api/v1/sessions/{id}/ws", s.handleSessionWebSocket)
	s.mux.HandleFunc("GET /api/v1/sessions/{id}/todos", s.handleSessionTodos)
	s.mux.HandleFunc("GET /api/v1/sessions/{id}/children", s.handleSessionChildren)
	s.mux.HandleFunc("GET /api/v1/sessions/{id}/tree", s.handleSessionTree)
	s.mux.HandleFunc("GET /api/v1/sessions/{id}/parent", s.handleSessionParent)
	s.mux.HandleFunc("DELETE /api/v1/sessions/{id}", s.handleDeleteSession)
	s.mux.HandleFunc("DELETE /api/v1/sessions/{id}/messages/from/{messageId}", s.handleTruncateMessages)
	s.mux.HandleFunc("PATCH /api/v1/sessions/{id}/agent", s.handleUpdateSessionAgent)
	s.mux.HandleFunc("PATCH /api/v1/sessions/{id}/model", s.handleUpdateSessionModel)
	s.mux.HandleFunc("GET /api/v1/models", s.handleListModels)
	s.mux.HandleFunc("GET /api/v1/tasks", s.handleListTasks)
	s.mux.HandleFunc("GET /api/v1/tasks/{id}", s.handleGetTask)
	s.mux.HandleFunc("DELETE /api/v1/tasks/{id}", s.handleCancelTask)
	s.mux.HandleFunc("DELETE /api/v1/tasks", s.handleCancelAllTasks)
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /api/swarm/events", s.handleSwarmEvents)
	// Deliverable 2 / 3 of the May 2026 context-accuracy bundle —
	// runtime-tunable compression threshold + manual /compress
	// endpoint. Both flow through the CompactionController option;
	// when not installed the handlers return 501 so callers see
	// "wired but disabled" rather than a 404 confusion.
	s.mux.HandleFunc("GET /api/v1/config/compression", s.handleGetCompressionConfig)
	s.mux.HandleFunc("PATCH /api/v1/config/compression", s.handleUpdateCompressionConfig)
	s.mux.HandleFunc("POST /api/v1/sessions/{id}/compress", s.handleCompactNow)
	// Chat Attachments Backend PR1 — plan "Chat Attachments Backend
	// (May 2026)" §6 task-03. Upload endpoint rides the same path-param
	// session-scope gate as handleSessionMessage. Image-only PR1
	// (jpeg/png/gif/webp). Caps: 5 MB/file, 10/request, 50 MB/session.
	s.mux.HandleFunc("POST /api/v1/sessions/{id}/attachments", s.handleUploadAttachments)
	// PR2 task-07: binary retrieval endpoint for the inbound `<img>`
	// render surface that task-08 closes (N9). Same path-param
	// session-scope gate; cross-session probes return 404 with no
	// media-type leak (plan R9).
	s.mux.HandleFunc("GET /api/v1/sessions/{id}/attachments/{aid}", s.handleGetAttachment)
	if s.metricsHandler != nil {
		s.mux.Handle("GET /metrics", s.metricsHandler)
	}
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
	// commit to SSE. The orchestrator (or legacy DispatchSwarm) would
	// also resolve internally; this duplicate-but-cheap pre-flight
	// preserves the historical 400-on-unknown-id contract.
	leadID, swarmCtx, err := s.resolveDispatchTarget(req.AgentID)
	if err != nil {
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

	// Pre-flight resolution result is consumed inside the orchestrator
	// path's own resolve; we keep the variables in scope for the
	// legacy fallback below.
	_ = leadID
	_ = swarmCtx

	// Per ADR-001 §"Wrappers not duplicates" + ADR - Session
	// Orchestrator for Surface Parity, /api/chat routes through the
	// shared orchestrator when the engine + registries are wired
	// (production path — every NewServer call from app.go has
	// these). Tests that construct a Server without the swarm
	// registry option fall back to legacy plain streaming via the
	// dispatch helper, preserving the agent-only contract used by
	// older API consumers.
	if s.swarmRegistry != nil {
		orch := orchestrator.New(s.dispatchEngine, s.registry, s.swarmRegistry, s.streamer, nil, s.sessionManager)
		err := orch.ProcessUserInput(r.Context(), orchestrator.UserInput{
			Message:      req.Message,
			DefaultAgent: req.AgentID,
			// ScanMentions matches TUI parity (Web Swarm Mention Parity,
			// May 2026): the web chat composer routes typed @-mentions
			// through the same orchestrator path the TUI's chat intent
			// uses, so a typed `@<swarm-id>` dispatches the swarm
			// regardless of which agent the toolbar AgentPicker has
			// selected. Agent @-mentions and unknown @-mentions still
			// fall through to req.AgentID — the orchestrator's resolver
			// only redirects on a swarm hit (see internal/orchestrator/
			// orchestrator.go::resolve).
			ScanMentions: true,
		}, consumer)
		if err != nil {
			log.Printf("[api] chat stream error: %v", err)
		}
		return
	}

	// Legacy path retained for tests that construct Server without
	// the swarm registry. Pre-flight resolution above produced
	// (id-verbatim, nil) for this case, so we just stream as agent.
	if err := swarm.DispatchSwarm(r.Context(), s.dispatchEngine, swarmCtx, s.streamer, consumer, leadID, req.Message); err != nil {
		log.Printf("[api] chat stream error: %v", err)
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
//   - Sets IsStreaming on each summary when a broker is configured and
//     reports an active publish for that session.
func (s *Server) handleListV1Sessions(w http.ResponseWriter, _ *http.Request) {
	if s.sessionManager == nil {
		http.Error(w, errSessionManagerNotConfigured, http.StatusNotImplemented)
		return
	}
	summaries := s.sessionManager.ListSessions()
	if summaries == nil {
		summaries = []*session.Summary{}
	}
	if s.sessionBroker != nil {
		for _, sum := range summaries {
			sum.IsStreaming = s.sessionBroker.IsPublishing(sum.ID)
		}
	}
	writeJSON(w, summaries)
}

// handleSessionMessage appends a message to a session and returns the updated session.
//
// Expected:
//   - Request path parameter "id" contains the session identifier.
//   - Request body contains non-empty content.
//
// Side effects:
//   - Appends a message to the session.
//   - Publishes stream chunks to the session broker if configured.
//   - Writes the updated session as JSON.
func (s *Server) handleSessionMessage(w http.ResponseWriter, r *http.Request) {
	if s.sessionManager == nil {
		http.Error(w, errSessionManagerNotConfigured, http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	id := r.PathValue("id")
	type reqBody struct {
		Content string `json:"content"`
		// AttachmentIDs is the optional list of attachment ids to thread
		// onto this turn. Plan "Chat Attachments Backend (May 2026)"
		// §6 task-04 / task-05. The ids must already exist in the
		// session's attachment store (via POST .../attachments). Unknown
		// ids surface as session.ErrAttachmentNotFound → 400.
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
	chunks, err := s.sessionManager.SendMessageWithAttachments(
		r.Context(), id, req.Content, req.AttachmentIDs,
	)
	if err != nil {
		if errors.Is(err, session.ErrAttachmentNotFound) {
			http.Error(w, "attachment id not found in session", http.StatusBadRequest)
			return
		}
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if chunks != nil {
		if s.sessionBroker != nil {
			s.sessionBroker.Publish(id, chunks)
		} else {
			for range chunks {
			}
		}
	}
	// SnapshotSession (not GetSession) so the *Session pointer never
	// escapes the manager's lock boundary. NewSessionResponse reads
	// ~10 mutable fields (Messages, Status, CurrentAgentID, ...);
	// any one of them races with SendMessage / UpdateSession* under
	// WLock if the caller derefs after RLock drops. See vault note
	// "Session Messages Data Race in SSE Fast-Path (May 2026)" §
	// "Sibling races".
	snap, err := s.sessionManager.SnapshotSession(id)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	writeJSON(w, NewSessionResponse(&snap))
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

// handleSessionStream streams *live* session events as SSE.
//
// The SSE stream is for events emitted strictly after the subscriber
// connects. Historical content lives on GET /api/v1/sessions/{id}/messages
// (the canonical history endpoint) — replaying it here would duplicate the
// previous turn's content into the next turn's streaming placeholder.
//
// Expected:
//   - Request path parameter "id" contains the session identifier.
//   - A session broker is configured (production wiring guarantees this
//     when a session manager is configured; see app.go).
//
// Side effects:
//   - Writes server-sent events to the response.
//   - Blocks until the broker closes the subscription or the client disconnects.
func (s *Server) handleSessionStream(w http.ResponseWriter, r *http.Request) {
	if s.sessionManager == nil {
		http.Error(w, errSessionManagerNotConfigured, http.StatusNotImplemented)
		return
	}
	id := r.PathValue("id")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	if _, err := s.sessionManager.GetSession(id); err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// Phase 3 — TUI-cadence parity. Emit a context_usage SSE event
	// on connect so the chat UI's usage chip hydrates with the
	// session's current state the moment the SSE connection lands,
	// matching the TUI's StatusBar which reflects current state on
	// every redraw (internal/tui/intents/chat/intent.go syncStatusBar).
	// Pre-fix the chip stayed hidden until the next pre-send event,
	// so a user reopening a session saw a blank chip until they
	// started typing.
	s.emitSessionLoadContextUsage(w, flusher, id)

	if s.sessionBroker == nil {
		// No live broker — the SSE stream contract is "live events only";
		// historical content lives on GET /api/v1/sessions/{id}/messages.
		// Send [DONE] immediately so the client knows the stream is closed.
		writeSSEDone(w, flusher)
		return
	}
	// Live session — stream new events only.
	// Historical content is loaded by the client via GET /messages.
	//
	// Branch on the session's last-message role to decide which broker
	// accessor to use:
	//
	//   - Last message non-user (sealed turn) → SubscribeIfPublishing.
	//     The transactional accessor returns (nil, no-op, false) when
	//     no Publish is in-flight, in which case we emit [DONE]
	//     immediately. When (ok == true) a fresh Publish run is
	//     mid-flight despite the sealed last message (e.g. a recovery
	//     turn or future multi-publish wiring) — consume from ch.
	//
	//   - Last message is user / no messages (between turns) →
	//     unconditional Subscribe. Publish hasn't started for the new
	//     turn yet; we must register a subscriber and wait for chunks
	//     to land. (Subscribing before Publish increments the active
	//     refcount is exactly the case Subscribe is for.)
	//
	// This ordering closes the IsPublishing TOCTOU previously surfaced
	// by the broker concurrency audit: separate Subscribe + IsPublishing
	// calls observed two different states, and a Publish that started
	// in the gap fanned chunks into a channel the handler had already
	// abandoned. The new transactional accessor decides "is there a
	// publisher AND register me" in one critical section under the
	// broker mutex.
	//
	// LastMessageRole is used instead of GetSession + freshSess.Messages
	// because GetSession returns the *Session pointer and releases the
	// manager's RLock on return. Reading sess.Messages outside the lock
	// races with SendMessage's append under the write lock — confirmed
	// by `go test -race` triggering on the slice header at server.go:756
	// vs manager.go:647. LastMessageRole projects the role under RLock
	// and returns by value, closing the race window.
	role, hasMsgs, lastErr := s.sessionManager.LastMessageRole(id)
	sealed := lastErr == nil && hasMsgs && role != "user"

	var liveCh <-chan provider.StreamChunk
	var unsubscribe func()
	if sealed {
		ch, unsub, ok := s.sessionBroker.SubscribeIfPublishing(id)
		if !ok {
			writeSSEDone(w, flusher)
			return
		}
		liveCh = ch
		unsubscribe = unsub
	} else {
		liveCh, unsubscribe = s.sessionBroker.Subscribe(id)
	}
	defer unsubscribe()

	// Slice 6a — bridge bus events for this session into the SSE
	// stream. The auto-compactor publishes EventContextCompacted on
	// the engine bus when an L2 compaction succeeds; the bridge
	// handler in event_bridge.go fans matching events into busCh as
	// WSChunkMsg values, and the select below multiplexes them with
	// the broker's stream chunks. The buffer is sized at 16 — bus
	// events arrive at most once per turn so the handler should
	// never see backpressure on this channel; the buffer is purely
	// to absorb a publish that races the SSE write.
	busCh := make(chan WSChunkMsg, 16)
	stopBus := s.subscribeSessionBus(id, busCh)
	defer stopBus()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-busCh:
			if !ok {
				continue
			}
			s.dispatchSessionBusEventSSE(w, flusher, ev)
			continue
		case chunk, ok := <-liveCh:
			if !ok {
				// M1 — SSE [DONE] race (May 2026 bug-hunt). The
				// select-loop multiplexes liveCh with busCh; when
				// both are ready in the same iteration Go picks one
				// branch non-deterministically. Pre-fix the terminal
				// branches (liveCh close, chunk.Done, critical
				// chunk.Error) wrote [DONE] and returned immediately,
				// silently dropping any already-enqueued bus event
				// (context_compacted, gate_failed, streaming.heartbeat)
				// that lost the race. Draining busCh of all
				// already-enqueued events onto the wire BEFORE
				// emitting [DONE] removes the timing dependency:
				// [DONE] is the terminal frame, so anything written
				// after it would be unreachable.
				s.drainPendingBusEventsSSE(w, flusher, busCh)
				writeSSEDone(w, flusher)
				return
			}
			if chunk.Error != nil {
				// Gate on severity so fatal provider errors (revoked
				// OAuth, 401, model-not-found, billing/quota lockout)
				// stop the SSE fan-out instead of being treated as
				// self-healing blips. Pre-fix the consumer always
				// `continue`d on chunk.Error, so the loop kept
				// reading subsequent chunks from the live channel
				// and emitting whatever landed — even after the
				// provider was definitively dead. The chunk-error
				// helper IsCriticalStreamError has been wired here
				// so the fix is one branch and one early return,
				// not a refactor of the surrounding dispatcher.
				//
				// The proactive context-window overflow gate
				// (engine.checkContextWindowOverflow) shares this
				// path: it wraps a *provider.Error with
				// ErrorTypeContextWindowExceeded which classifies as
				// SeverityCritical, so the gate fires here. The
				// distinct safeMsg category routes a user-actionable
				// recovery copy through the same banner instead of
				// the generic "critical stream error" wording fatal
				// errors share.
				if provider.IsCriticalStreamError(chunk.Error) {
					category := "stream_critical"
					var pErr *provider.Error
					if errors.As(chunk.Error, &pErr) && pErr != nil &&
						pErr.ErrorType == provider.ErrorTypeContextWindowExceeded {
						category = "stream_critical_context_exceeded"
					}
					writeSSEClientError(w, flusher, chunk.Error, category)
					// M1 — drain queued bus events before terminating
					// the stream so a heartbeat / context_compacted
					// published moments before the critical error is
					// never lost to the [DONE] race.
					s.drainPendingBusEventsSSE(w, flusher, busCh)
					writeSSEDone(w, flusher)
					return
				}
				writeSSEClientError(w, flusher, chunk.Error, "stream_error")
				continue
			}
			if chunk.Done {
				// M1 — drain queued bus events before terminating
				// the stream so a heartbeat / context_compacted /
				// gate_failed published moments before chunk.Done
				// is never silently dropped to the select race.
				s.drainPendingBusEventsSSE(w, flusher, busCh)
				writeSSEDone(w, flusher)
				return
			}
			if chunk.DelegationInfo != nil {
				writeSSEDelegationInfo(w, flusher, chunk.DelegationInfo)
			}
			if chunk.ToolCall != nil {
				name := chunk.ToolCall.Name
				if strings.HasPrefix(name, "skill:") {
					writeSSESkillLoad(w, flusher, strings.TrimPrefix(name, "skill:"))
				} else {
					argsJSON := ""
					if b, err := json.Marshal(chunk.ToolCall.Arguments); err == nil {
						argsJSON = string(b)
					}
					writeSSEToolCall(w, flusher, name, argsJSON)
				}
			}
			// Drop #2 — Thinking SSE dispatch. Anthropic's streaming.go
			// already emits StreamChunk{Thinking: <text>} for thinking_delta
			// blocks, and the openaicompat reasoning_content fix (Drop #1)
			// emits the same shape for glm-4.6 / DeepSeek-R1 reasoning. Pre
			// this branch the wire dropped both — the chat UI saw a 52-second
			// silent gap during the model's reasoning phase.
			if chunk.Thinking != "" {
				writeSSEThinking(w, flusher, chunk.Thinking)
			}
			// Typed event chunks MUST route to their typed SSE writer rather
			// than fall through to writeSSEContent. Pre-this-fix the
			// dispatcher emitted a plain {"content":"..."} chunk for
			// harness_* events, leaking raw JSON like
			// {"valid":...,"score":...,"attemptCount":2,...} into the assistant
			// bubble (visible to non-technical users in chat).
			//
			// The contract: when EventType is set, the chunk is observability
			// metadata, not assistant content. Content is dispatched as plain
			// text only when EventType is empty.
			switch chunk.EventType {
			case "tool_result":
				if chunk.ToolResult != nil {
					// Gap 2 (tool_error SSE wire, May 2026). The engine
					// stamps ToolResult.IsError=true on tool_result chunks
					// when the executor returns a non-nil err (see
					// internal/engine/engine.go:storeToolResult dispatch
					// at the appendToolResultsBatch site). Route those to
					// the additive `tool_error` typed event so the Vue
					// chatStore can flip the matching tool message to
					// status='error' in-stream — without this branch the
					// frontend's handleToolResultEvent hard-set
					// status='completed' and the failure stayed hidden
					// until the post-stream history reconcile.
					if chunk.ToolResult.IsError {
						writeSSEToolError(w, flusher, chunk.ToolResult.Content)
					} else {
						writeSSEToolResult(w, flusher, chunk.ToolResult.Content)
					}
				}
			case "harness_retry":
				writeSSEHarnessRetry(w, flusher, chunk.Content)
			case "harness_attempt_start":
				writeSSEAttemptStart(w, flusher, chunk.Content)
			case "harness_complete":
				writeSSEHarnessComplete(w, flusher, chunk.Content)
			case "harness_critic_feedback":
				writeSSECriticFeedback(w, flusher, chunk.Content)
			case "provider_changed":
				// Track B — failover transition affordance. The failover
				// StreamHook prepends a chunk{EventType:"provider_changed",
				// Content:<json>} when a fallback candidate succeeds after
				// a previous candidate failed. Content is the marshalled
				// providerChangedPayload (from / to / reason). The wire
				// passes the JSON through verbatim — the frontend
				// discriminated union's "provider_changed" branch parses
				// from/to/reason and renders the toast.
				writeSSEProviderChanged(w, flusher, chunk.Content)
			case "model_active":
				// Always-on actual-model affordance (May 2026 chip-shows-
				// selection-not-actual fix). The failover StreamHook prepends
				// a chunk{EventType:"model_active", Content:<json>} on EVERY
				// successful stream — not just on failover. Content is the
				// marshalled modelActivePayload (provider / model). The
				// frontend's discriminated union "model_active" branch
				// updates currentProviderId / currentModelId so the toolbar
				// chip pivots from the user's selection to the actual model
				// the moment streaming starts.
				writeSSEModelActive(w, flusher, chunk.Content)
			case "context_usage":
				// Always-on context-window usage affordance (May 2026
				// output-reserve fix). The engine emits a chunk{EventType:
				// "context_usage", Content:<json>} as the first artefact of
				// every Stream that has enough information to compute it.
				// Content is the marshalled contextUsagePayload (input /
				// reserve / limit / percentage / provider / model). The
				// frontend's discriminated union "context_usage" branch
				// updates the toolbar usage chip so the user sees how
				// close the request is to saturating the model's window.
				writeSSEContextUsage(w, flusher, chunk.Content)
			case "":
				if chunk.Content != "" {
					writeSSEContent(w, flusher, chunk.Content)
				}
			default:
				// Forward-compatible: an unknown EventType chunk falls
				// through to a plain content emission only when the
				// frontend can render it as text. Absent that signal the
				// chunk is dropped on the wire — the client's discriminated
				// union 'unknown' bucket will record any genuinely-new
				// typed events (see web/src/lib/sseEvent.ts) without
				// affecting state.
				if chunk.Content != "" {
					writeSSEContent(w, flusher, chunk.Content)
				}
			}
		}
	}
}

// handleListSessions writes all available sessions as JSON to the response.
//
// handleCancelStream cancels an in-flight streaming turn for a session.
//
// Expected:
//   - Request path parameter "id" identifies an existing session.
//
// Returns:
//   - 204 No Content when a cancel was fired for an in-flight turn.
//   - 404 Not Found when no in-flight turn exists for the session.
//
// Side effects:
//   - Fires the context cancellation for the session's streaming turn,
//     propagating cancellation through the entire turn pipeline.
func (s *Server) handleCancelStream(w http.ResponseWriter, r *http.Request) {
	if s.sessionManager == nil {
		http.Error(w, errSessionManagerNotConfigured, http.StatusNotImplemented)
		return
	}
	id := r.PathValue("id")
	if s.sessionManager.CancelInflight(id) {
		w.WriteHeader(http.StatusNoContent)
	} else {
		http.Error(w, "no in-flight turn", http.StatusNotFound)
	}
}

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
	w.Header().Set("Access-Control-Allow-Origin", "*")

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

// emitSessionLoadContextUsage writes a typed context_usage SSE event
// to the response if the api server has a ContextUsageProvider wired
// AND the provider returns a payload for the session's current state.
// No-op otherwise — the chat UI's chip falls back to its empty-state
// affordance until the next streamed context_usage event lands.
//
// The payload reflects the session's CurrentProviderID +
// CurrentModelID and the snapshot's persisted message history, so the
// chip's input-token estimate accounts for prior turns (the figure
// the user wants to see on session-load: "if I sent another turn now
// this is the cost").
//
// session.Message → provider.Message projection mirrors
// session.Manager.SendMessage's projection: Role, Content,
// ThinkingBlocks, StopReason are propagated; ToolCalls/ToolResults
// the manager rebuilds inline are not relevant for the input-token
// estimate.
//
// Expected:
//   - sessionID identifies a session known to the manager.
//
// Side effects:
//   - On a successful payload: writes one SSE data line carrying the
//     `{"type":"context_usage", ...}` JSON and flushes.
//   - On absence of a provider OR a degraded payload: no write.
func (s *Server) emitSessionLoadContextUsage(w http.ResponseWriter, flusher http.Flusher, sessionID string) {
	if s == nil || s.contextUsageProvider == nil || s.sessionManager == nil {
		return
	}
	snap, err := s.sessionManager.SnapshotSession(sessionID)
	if err != nil {
		return
	}
	payload := s.contextUsageForSnapshot(&snap)
	if len(payload) == 0 {
		return
	}
	writeSSEContextUsage(w, flusher, string(payload))
}

// drainPendingBusEventsSSE non-blockingly drains every event already
// enqueued on the bus channel and dispatches each onto the SSE wire.
//
// M1 — SSE [DONE] race (May 2026 bug-hunt). The SSE main loop's
// select multiplexes liveCh (provider chunks) and busCh (session-
// scoped bus events). When both are ready in the same iteration Go's
// select picks one non-deterministically. Pre-fix, the terminal
// branches (liveCh close, chunk.Done, critical chunk.Error) wrote
// [DONE] and returned immediately — silently discarding any pending
// bus event that lost the race. Bus events enqueued AFTER [DONE] are
// unreachable because [DONE] is the terminal frame, so the only safe
// place to surface a "queued at termination" event is BEFORE [DONE].
//
// The drain is non-blocking by design: it reads only what is already
// in the buffer at the moment of the terminal branch firing. It does
// NOT wait for in-flight publishers; the goal is "do not lose what
// the bus already delivered" not "wait for late publishers to land".
// Late publishers (publishing after the terminal branch fires) hit
// the buffered channel which is then orphaned when the handler
// returns — the same drop-on-close-window the WebSocket path
// accepts (see C2 fix documentation).
//
// Expected:
//   - busCh has been allocated by the SSE handler; the handler is
//     the sole receiver so no other goroutine competes for receives.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - For each event already buffered on busCh, calls
//     dispatchSessionBusEventSSE which writes one SSE frame and
//     flushes. Stops at the first non-ready receive (default branch).
func (s *Server) drainPendingBusEventsSSE(w http.ResponseWriter, flusher http.Flusher, busCh <-chan WSChunkMsg) {
	for {
		select {
		case ev, ok := <-busCh:
			if !ok {
				return
			}
			s.dispatchSessionBusEventSSE(w, flusher, ev)
		default:
			return
		}
	}
}

// dispatchSessionBusEventSSE routes a session-scoped bus event from
// subscribeSessionBus onto the SSE wire. Slice 6a wires
// EventContextCompacted; future bus events that should mirror onto
// SSE add a case here. Bus events that aren't intended for SSE (the
// existing tool.execute.* / provider.rate_limited family, which
// only WS clients consume today) fall through to the default branch
// and are silently dropped — adding new SSE surfaces is opt-in here.
//
// Expected:
//   - ev is a WSChunkMsg produced by one of the handlers in
//     event_bridge.go.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes one SSE data line per event whose EventType has a case
//     below; flushes after every successful write.
//   - Drops events without an SSE counterpart silently — they remain
//     visible to WebSocket clients via the same bridge.
func (s *Server) dispatchSessionBusEventSSE(w http.ResponseWriter, flusher http.Flusher, ev WSChunkMsg) {
	switch ev.EventType {
	case events.EventContextCompacted:
		// Slice 6a — bridge the engine's L2 auto-compactor telemetry
		// onto the SSE wire. The bridge handler produces a
		// map[string]any payload with the canonical fields; the
		// writer adds the `"type":"context_compacted"` discriminant.
		data, ok := ev.EventData.(map[string]any)
		if !ok {
			return
		}
		writeSSEContextCompacted(w, flusher, data)
	case events.EventGateFailed:
		// Plans/Gate Bus Bridge — Engine to SSE and TUI (May 2026):
		// project the engine's gate.failed bus event onto the SSE
		// wire as `"type":"gate_failed"` so the Vue chat surface
		// renders the persistent gate-failed banner. Halt-class only;
		// continue/warn-class failures never reach this dispatch.
		data, ok := ev.EventData.(map[string]any)
		if !ok {
			return
		}
		writeSSEGateFailed(w, flusher, data)
	case events.EventStreamingHeartbeat:
		// Streaming Coherence Slice F follow-up (Bug Fix #62, May
		// 2026): project the engine's streaming.heartbeat bus event
		// onto the SSE wire as `"type":"streaming.heartbeat"` so the
		// Vue chat surface's adaptive stall watchdog re-arms on
		// every tick. Before this case the engine published the
		// typed event with zero subscribers — the catalog claim that
		// api.subscribeSessionBus bridged it onto the wire was a
		// documentation lie (corrected at fe071507); this case wires
		// the bridge for real.
		data, ok := ev.EventData.(map[string]any)
		if !ok {
			return
		}
		writeSSEStreamingHeartbeat(w, flusher, data)
	default:
		// Bus event without an SSE binding — safely no-op. WebSocket
		// clients still receive it via the same bridge.
	}
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
