package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/discovery"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/streaming"
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
	streamer          Streamer
	registry          *agent.Registry
	discovery         *discovery.AgentDiscovery
	skills            []skill.Skill
	sessions          *ctxstore.FileSessionStore
	sessionManager    *session.Manager
	sessionBroker          *SessionBroker
	todoStore              todo.Store
	backgroundManager      *engine.BackgroundTaskManager
	completionOrchestrator *engine.CompletionOrchestrator
	eventBus               *eventbus.EventBus
	metricsHandler         http.Handler
	mux                    *http.ServeMux
}

// ServerOption configures an optional Server dependency.
type ServerOption func(*Server)

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
		w.Header().Set("Content-Security-Policy", "default-src 'self'")
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
	s.mux.HandleFunc("POST /api/chat", s.handleChat)
	s.mux.HandleFunc("GET /api/discover", s.handleDiscover)
	s.mux.HandleFunc("GET /api/skills", s.handleListSkills)
	s.mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	s.mux.HandleFunc("GET /", s.handleIndex)
	s.mux.HandleFunc("POST /api/v1/sessions", s.handleCreateSession)
	s.mux.HandleFunc("GET /api/v1/sessions", s.handleListV1Sessions)
	s.mux.HandleFunc("POST /api/v1/sessions/{id}/messages", s.handleSessionMessage)
	s.mux.HandleFunc("GET /api/v1/sessions/{id}/stream", s.handleSessionStream)
	s.mux.HandleFunc("GET /api/v1/sessions/{id}/ws", s.handleSessionWebSocket)
	s.mux.HandleFunc("GET /api/v1/sessions/{id}/todos", s.handleSessionTodos)
	s.mux.HandleFunc("GET /api/v1/sessions/{id}/children", s.handleSessionChildren)
	s.mux.HandleFunc("GET /api/v1/sessions/{id}/tree", s.handleSessionTree)
	s.mux.HandleFunc("GET /api/v1/sessions/{id}/parent", s.handleSessionParent)
	s.mux.HandleFunc("GET /api/v1/tasks", s.handleListTasks)
	s.mux.HandleFunc("GET /api/v1/tasks/{id}", s.handleGetTask)
	s.mux.HandleFunc("DELETE /api/v1/tasks/{id}", s.handleCancelTask)
	s.mux.HandleFunc("DELETE /api/v1/tasks", s.handleCancelAllTasks)
	s.mux.HandleFunc("GET /health", s.handleHealth)
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

// chatRequest represents a chat message request from the client.
type chatRequest struct {
	AgentID string `json:"agent_id"`
	Message string `json:"message"`
}

// sseChunk represents a single content chunk in a server-sent event stream.
type sseChunk struct {
	Content string `json:"content"`
}

// sseError represents an error message in a server-sent event stream.
type sseError struct {
	Error string `json:"error"`
}

// handleChat processes a chat request and streams the response as server-sent events.
//
// Expected:
//   - Request body contains JSON-encoded chatRequest with agent_id and message.
//   - ResponseWriter supports HTTP flushing for streaming.
//   - Optional query parameter "verbosity" accepts "minimal", "standard", or "verbose".
//
// Side effects:
//   - Writes HTTP 200 with Content-Type text/event-stream.
//   - Streams content chunks, errors, and completion marker as SSE data lines.
//   - Writes HTTP 400 if request body is invalid JSON.
//   - Writes HTTP 500 if streaming is not supported.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
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

	if err := streaming.Run(r.Context(), s.streamer, consumer, req.AgentID, req.Message); err != nil {
		log.Printf("[api] chat stream error: %v", err)
	}
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
	sess, err := s.sessionManager.CreateSession(req.AgentID)
	if err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}
	writeJSON(w, sess)
}

// handleListV1Sessions lists all sessions as summaries.
//
// Expected:
//   - A session manager is configured.
//
// Side effects:
//   - Writes a JSON array of session summaries.
func (s *Server) handleListV1Sessions(w http.ResponseWriter, _ *http.Request) {
	if s.sessionManager == nil {
		http.Error(w, errSessionManagerNotConfigured, http.StatusNotImplemented)
		return
	}
	summaries := s.sessionManager.ListSessions()
	if summaries == nil {
		summaries = []*session.Summary{}
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
	}
	var req reqBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Content == "" {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if s.completionOrchestrator != nil {
		s.completionOrchestrator.ResetRePromptCount(id)
	}
	chunks, err := s.sessionManager.SendMessage(r.Context(), id, req.Content)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if s.sessionBroker != nil && chunks != nil {
		go s.sessionBroker.Publish(id, chunks)
	}
	sess, err := s.sessionManager.GetSession(id)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	writeJSON(w, sess)
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

// handleSessionStream streams session events as SSE, supporting verbosity param.
//
// Expected:
//   - Request path parameter "id" contains the session identifier.
//   - If a session broker is configured, subscribes to live events and forwards them.
//   - Otherwise, replays session history and closes.
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
	verbosity := r.URL.Query().Get("verbosity")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	sess, err := s.sessionManager.GetSession(id)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	for _, msg := range sess.Messages {
		if verbosity == "full" || msg.Role == "assistant" {
			writeSSEContent(w, flusher, msg.Content)
		}
	}
	if s.sessionBroker == nil {
		writeSSEDone(w, flusher)
		return
	}
	liveCh, unsubscribe := s.sessionBroker.Subscribe(id)
	defer unsubscribe()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case chunk, ok := <-liveCh:
			if !ok {
				writeSSEDone(w, flusher)
				return
			}
			if chunk.Error != nil {
				writeSSEError(w, flusher, chunk.Error.Error())
				continue
			}
			if chunk.Done {
				writeSSEDone(w, flusher)
				return
			}
			writeSSEContent(w, flusher, chunk.Content)
		}
	}
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

// handleIndex writes the embedded HTML chat interface to the response.
//
// Expected:
//   - None.
//
// Side effects:
//   - Writes HTTP 200 with Content-Type text/html; charset=utf-8.
//   - Writes embedded HTML content to response body.
func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(embeddedHTML)); err != nil {
		return
	}
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
		http.Error(w, err.Error(), http.StatusNotFound)
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
		http.Error(w, err.Error(), http.StatusNotFound)
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
		http.Error(w, err.Error(), http.StatusNotFound)
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
		http.Error(w, err.Error(), http.StatusNotFound)
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

// writeSSEContent marshals content as a JSON chunk and writes it as a server-sent event.
//
// Expected:
//   - content is the text to send in the SSE chunk.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded chunk to response.
//   - Flushes response buffer.
func writeSSEContent(w http.ResponseWriter, flusher http.Flusher, content string) {
	data := sseChunk{Content: content}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSEError marshals an error message as a JSON error and writes it as a server-sent event.
//
// Expected:
//   - errMsg is the error message text to send.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded error to response.
//   - Flushes response buffer.
func writeSSEError(w http.ResponseWriter, flusher http.Flusher, errMsg string) {
	data := sseError{Error: errMsg}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSEDone writes the completion marker as a server-sent event.
//
// Expected:
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with "[DONE]" marker to response.
//   - Flushes response buffer.
func writeSSEDone(w http.ResponseWriter, flusher http.Flusher) {
	writeSSE(w, flusher, "[DONE]")
}

// sseToolCall represents a tool call event in a server-sent event stream.
type sseToolCall struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// writeSSEToolCall marshals a tool call as a JSON event and writes it as a server-sent event.
//
// Expected:
//   - name is the tool name being invoked.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded tool call to response.
//   - Flushes response buffer.
func writeSSEToolCall(w http.ResponseWriter, flusher http.Flusher, name string) {
	data := sseToolCall{Type: "tool_call", Name: name, Status: "running"}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// sseSkillLoad represents a skill load event in a server-sent event stream.
type sseSkillLoad struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// writeSSESkillLoad marshals a skill load as a JSON event and writes it as a server-sent event.
//
// Expected:
//   - name is the skill name being loaded.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded skill load to response.
//   - Flushes response buffer.
func writeSSESkillLoad(w http.ResponseWriter, flusher http.Flusher, name string) {
	data := sseSkillLoad{Type: "skill_load", Name: name}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// sseToolResult represents a tool result event in a server-sent event stream.
type sseToolResult struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// writeSSEToolResult marshals a tool result as a JSON event and writes it as a server-sent event.
//
// Expected:
//   - content is the tool result content to send.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded tool result to response.
//   - Flushes response buffer.
func writeSSEToolResult(w http.ResponseWriter, flusher http.Flusher, content string) {
	data := sseToolResult{Type: "tool_result", Content: content}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// sseHarnessRetry represents a harness retry event in a server-sent event stream.
type sseHarnessRetry struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// writeSSEHarnessRetry marshals a harness retry as a JSON event and writes it as a server-sent event.
//
// Expected:
//   - content describes the validation failure and retry reason.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded harness retry event to response.
//   - Flushes response buffer.
func writeSSEHarnessRetry(w http.ResponseWriter, flusher http.Flusher, content string) {
	data := sseHarnessRetry{Type: "harness_retry", Content: content}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// sseAttemptStart represents a harness attempt start event in a server-sent event stream.
type sseAttemptStart struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// writeSSEAttemptStart marshals a harness attempt start as a JSON event and writes it as a server-sent event.
//
// Expected:
//   - content describes the attempt being started.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded attempt start event to response.
//   - Flushes response buffer.
func writeSSEAttemptStart(w http.ResponseWriter, flusher http.Flusher, content string) {
	data := sseAttemptStart{Type: "harness_attempt_start", Content: content}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// sseHarnessComplete represents a harness completion event in a server-sent event stream.
type sseHarnessComplete struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// writeSSEHarnessComplete marshals a harness completion as a JSON event and writes it as a server-sent event.
//
// Expected:
//   - content describes the evaluation outcome.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded harness complete event to response.
//   - Flushes response buffer.
func writeSSEHarnessComplete(w http.ResponseWriter, flusher http.Flusher, content string) {
	data := sseHarnessComplete{Type: "harness_complete", Content: content}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// sseCriticFeedback represents a harness critic feedback event in a server-sent event stream.
type sseCriticFeedback struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// writeSSECriticFeedback marshals harness critic feedback as a JSON event and writes it as a server-sent event.
//
// Expected:
//   - content describes the critic's feedback on the plan.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded critic feedback event to response.
//   - Flushes response buffer.
func writeSSECriticFeedback(w http.ResponseWriter, flusher http.Flusher, content string) {
	data := sseCriticFeedback{Type: "harness_critic_feedback", Content: content}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
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

// writeSSEDelegation marshals a delegation event as JSON and writes it as a server-sent event.
//
// Expected:
//   - event contains delegation metadata.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded delegation event to response.
//   - Flushes response buffer.
func writeSSEDelegation(w http.ResponseWriter, flusher http.Flusher, event streaming.DelegationEvent) {
	jsonData, err := json.Marshal(event)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSE writes a server-sent event data line and flushes the response buffer.
//
// Expected:
//   - data is the content to send in the SSE data line.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes "data: " prefix, data, and two newlines to response.
//   - Flushes response buffer to send data immediately.
func writeSSE(w http.ResponseWriter, flusher http.Flusher, data string) {
	if _, err := w.Write([]byte("data: " + data + "\n\n")); err != nil {
		return
	}
	flusher.Flush()
}

var embeddedHTML = strings.Join([]string{
	`<!DOCTYPE html>`,
	`<html lang="en">`,
	`<head>`,
	`    <meta charset="UTF-8">`,
	`    <meta name="viewport" content="width=device-width, initial-scale=1.0">`,
	`    <title>FlowState Chat</title>`,
	`    <style>`,
	`        * { box-sizing: border-box; margin: 0; padding: 0; }`,
	`        body {`,
	`            font-family: system-ui, -apple-system, sans-serif;`,
	`            background: #1a1a2e; color: #eee;`,
	`            height: 100vh; display: flex; flex-direction: column;`,
	`        }`,
	`        .header { padding: 1rem; background: #16213e; border-bottom: 1px solid #0f3460; }`,
	`        .header h1 { font-size: 1.5rem; color: #e94560; }`,
	`        .messages { flex: 1; overflow-y: auto; padding: 1rem; }`,
	`        .message {`,
	`            margin-bottom: 1rem; padding: 0.75rem 1rem;`,
	`            border-radius: 8px; max-width: 80%;`,
	`        }`,
	`        .message.user { background: #0f3460; margin-left: auto; }`,
	`        .message.assistant { background: #16213e; border: 1px solid #0f3460; }`,
	`        .input-area {`,
	`            padding: 1rem; background: #16213e;`,
	`            border-top: 1px solid #0f3460; display: flex; gap: 0.5rem;`,
	`        }`,
	`        textarea {`,
	`            flex: 1; background: #1a1a2e; border: 1px solid #0f3460;`,
	`            color: #eee; padding: 0.75rem; border-radius: 8px;`,
	`            resize: none; font-family: inherit; font-size: 1rem;`,
	`        }`,
	`        textarea:focus { outline: none; border-color: #e94560; }`,
	`        button {`,
	`            background: #e94560; color: white; border: none;`,
	`            padding: 0.75rem 1.5rem; border-radius: 8px; cursor: pointer; font-size: 1rem;`,
	`        }`,
	`        button:hover { background: #ff6b6b; }`,
	`        button:disabled { background: #444; cursor: not-allowed; }`,
	`    </style>`,
	`</head>`,
	`<body>`,
	`    <div class="header"><h1>FlowState Chat</h1></div>`,
	`    <div class="messages" id="messages"></div>`,
	`    <div class="input-area">`,
	`        <textarea id="input" rows="2" placeholder="Type your message..."></textarea>`,
	`        <button id="send">Send</button>`,
	`    </div>`,
	`    <script>`,
	`        const messagesDiv = document.getElementById('messages');`,
	`        const input = document.getElementById('input');`,
	`        const sendBtn = document.getElementById('send');`,
	``,
	`        function addMessage(content, role) {`,
	`            const div = document.createElement('div');`,
	`            div.className = 'message ' + role;`,
	`            div.textContent = content;`,
	`            messagesDiv.appendChild(div);`,
	`            messagesDiv.scrollTop = messagesDiv.scrollHeight;`,
	`            return div;`,
	`        }`,
	``,
	`        async function sendMessage() {`,
	`            const message = input.value.trim();`,
	`            if (!message) return;`,
	``,
	`            input.value = '';`,
	`            sendBtn.disabled = true;`,
	`            addMessage(message, 'user');`,
	``,
	`            const assistantDiv = addMessage('', 'assistant');`,
	``,
	`            try {`,
	`                const response = await fetch('/api/chat', {`,
	`                    method: 'POST',`,
	`                    headers: { 'Content-Type': 'application/json' },`,
	`                    body: JSON.stringify({ agent_id: 'default', message: message })`,
	`                });`,
	``,
	`                const reader = response.body.getReader();`,
	`                const decoder = new TextDecoder();`,
	`                let buffer = '';`,
	``,
	`                while (true) {`,
	`                    const { done, value } = await reader.read();`,
	`                    if (done) break;`,
	``,
	`                    buffer += decoder.decode(value, { stream: true });`,
	`                    const lines = buffer.split('\\n');`,
	`                    buffer = lines.pop() || '';`,
	``,
	`                    for (const line of lines) {`,
	`                        if (line.startsWith('data: ')) {`,
	`                            const data = line.slice(6);`,
	`                            if (data === '[DONE]') continue;`,
	`                            try {`,
	`                                const parsed = JSON.parse(data);`,
	`                                if (parsed.content) {`,
	`                                    assistantDiv.textContent += parsed.content;`,
	`                                }`,
	`                            } catch (e) {}`,
	`                        }`,
	`                    }`,
	`                }`,
	`            } catch (error) {`,
	`                assistantDiv.textContent = 'Error: ' + error.message;`,
	`            }`,
	``,
	`            sendBtn.disabled = false;`,
	`        }`,
	``,
	`        sendBtn.addEventListener('click', sendMessage);`,
	`        input.addEventListener('keydown', (e) => {`,
	`            if (e.key === 'Enter' && !e.shiftKey) {`,
	`                e.preventDefault();`,
	`                sendMessage();`,
	`            }`,
	`        });`,
	`    </script>`,
	`</body>`,
	`</html>`,
}, "\n")
