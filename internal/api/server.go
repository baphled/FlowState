// Package api provides the HTTP API server with SSE streaming support.
package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/discovery"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/skill"
)

// Server provides HTTP endpoints for the FlowState platform.
type Server struct {
	engine    *engine.Engine
	registry  *agent.AgentRegistry
	discovery *discovery.AgentDiscovery
	skills    []skill.Skill
	mux       *http.ServeMux
}

// NewServer creates a new API server with the given dependencies.
func NewServer(
	eng *engine.Engine,
	registry *agent.AgentRegistry,
	disc *discovery.AgentDiscovery,
	skills []skill.Skill,
) *Server {
	s := &Server{
		engine:    eng,
		registry:  registry,
		discovery: disc,
		skills:    skills,
		mux:       http.NewServeMux(),
	}
	s.setupRoutes()
	return s
}

// Handler returns the HTTP handler for this server.
func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) setupRoutes() {
	s.mux.HandleFunc("GET /api/agents", s.handleListAgents)
	s.mux.HandleFunc("GET /api/agents/{id}", s.handleGetAgent)
	s.mux.HandleFunc("POST /api/chat", s.handleChat)
	s.mux.HandleFunc("GET /api/discover", s.handleDiscover)
	s.mux.HandleFunc("GET /api/skills", s.handleListSkills)
	s.mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	s.mux.HandleFunc("GET /", s.handleIndex)
}

func (s *Server) handleListAgents(w http.ResponseWriter, _ *http.Request) {
	manifests := s.registry.List()
	if manifests == nil {
		manifests = []*agent.AgentManifest{}
	}
	writeJSON(w, manifests)
}

func (s *Server) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	manifest, ok := s.registry.Get(id)
	if !ok {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}
	writeJSON(w, manifest)
}

type chatRequest struct {
	AgentID string `json:"agent_id"`
	Message string `json:"message"`
}

type sseChunk struct {
	Content string `json:"content"`
}

type sseError struct {
	Error string `json:"error"`
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	chunks, err := s.engine.Stream(ctx, req.AgentID, req.Message)
	if err != nil {
		writeSSEError(w, flusher, err.Error())
		writeSSEDone(w, flusher)
		return
	}

	for chunk := range chunks {
		if chunk.Error != nil {
			writeSSEError(w, flusher, chunk.Error.Error())
			continue
		}
		if chunk.Content != "" {
			writeSSEContent(w, flusher, chunk.Content)
		}
		if chunk.Done {
			break
		}
	}

	writeSSEDone(w, flusher)
}

func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	message := r.URL.Query().Get("message")
	suggestions := s.discovery.Suggest(message)
	if suggestions == nil {
		suggestions = []discovery.AgentSuggestion{}
	}
	writeJSON(w, suggestions)
}

func (s *Server) handleListSkills(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.skills)
}

func (s *Server) handleListSessions(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, []interface{}{})
}

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(embeddedHTML))
}

func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(data)
}

func writeSSEContent(w http.ResponseWriter, flusher http.Flusher, content string) {
	data := sseChunk{Content: content}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

func writeSSEError(w http.ResponseWriter, flusher http.Flusher, errMsg string) {
	data := sseError{Error: errMsg}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

func writeSSEDone(w http.ResponseWriter, flusher http.Flusher) {
	writeSSE(w, flusher, "[DONE]")
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, data string) {
	_, _ = w.Write([]byte("data: " + data + "\n\n"))
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
