// Package api provides the HTTP API server with SSE streaming support.
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
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/streaming"
)

// Streamer abstracts the streaming producer for chat responses.
type Streamer interface {
	// Stream returns a channel of response chunks for the given agent and message.
	Stream(ctx context.Context, agentID string, message string) (<-chan provider.StreamChunk, error)
}

// Server provides HTTP endpoints for the FlowState platform.
type Server struct {
	streamer  Streamer
	registry  *agent.Registry
	discovery *discovery.AgentDiscovery
	skills    []skill.Skill
	sessions  *ctxstore.FileSessionStore
	mux       *http.ServeMux
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
	sessions *ctxstore.FileSessionStore,
) *Server {
	s := &Server{
		streamer:  streamer,
		registry:  registry,
		discovery: disc,
		skills:    skills,
		sessions:  sessions,
		mux:       http.NewServeMux(),
	}
	s.setupRoutes()
	return s
}

// Handler returns the HTTP handler for this server.
//
// Returns:
//   - The http.Handler that serves all API routes.
//
// Side effects:
//   - None.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// setupRoutes registers all HTTP route handlers on the server's mux.
//
// Side effects:
//   - Registers GET /api/agents, GET /api/agents/{id}, POST /api/chat,
//     GET /api/discover, GET /api/skills, GET /api/sessions, and GET / routes.
func (s *Server) setupRoutes() {
	s.mux.HandleFunc("GET /api/agents", s.handleListAgents)
	s.mux.HandleFunc("GET /api/agents/{id}", s.handleGetAgent)
	s.mux.HandleFunc("POST /api/chat", s.handleChat)
	s.mux.HandleFunc("GET /api/discover", s.handleDiscover)
	s.mux.HandleFunc("GET /api/skills", s.handleListSkills)
	s.mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	s.mux.HandleFunc("GET /", s.handleIndex)
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
//
// Side effects:
//   - Writes HTTP 200 with Content-Type text/event-stream.
//   - Streams content chunks, errors, and completion marker as SSE data lines.
//   - Writes HTTP 400 if request body is invalid JSON.
//   - Writes HTTP 500 if streaming is not supported.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	consumer, ok := NewSSEConsumer(w)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

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
