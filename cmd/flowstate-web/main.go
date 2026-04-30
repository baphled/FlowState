// Package main is the entry point for the FlowState web API server.
//
// It exposes a minimal HTTP API consumed by the Vue.js frontend, including:
//   - Chat proxying to the configured AI provider
//   - Swarm event streaming (mock data for PoC)
//   - Model listing
//   - Health check
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// message represents a single conversation turn.
type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatRequest is the payload for POST /api/chat.
type chatRequest struct {
	Messages []message `json:"messages"`
	Model    string    `json:"model"`
}

// chatResponse is the response body for POST /api/chat.
type chatResponse struct {
	Content string `json:"content"`
}

// swarmEvent represents a single swarm activity event.
type swarmEvent struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
	AgentName string                 `json:"agentName"`
	Payload   map[string]interface{} `json:"payload"`
	Timestamp string                 `json:"timestamp"`
}

// errorResponse is the standard error envelope.
type errorResponse struct {
	Error string `json:"error"`
}

// healthResponse is returned by GET /api/health.
type healthResponse struct {
	Status string `json:"status"`
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", handleHealth)
	mux.HandleFunc("/api/models", handleModels)
	mux.HandleFunc("/api/chat", handleChat)
	mux.HandleFunc("/api/swarm/events", handleSwarmEvents)

	addr := ":8080"
	log.Printf("FlowState web API server listening on %s", addr)
	if err := http.ListenAndServe(addr, corsMiddleware(mux)); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// corsMiddleware adds CORS headers allowing the Vite dev server to reach the API.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "http://localhost:5173")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// writeJSON serialises v to JSON and writes it with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON encode error: %v", err)
	}
}

// handleHealth responds to GET /api/health.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

// handleModels responds to GET /api/models with a list of available model names.
func handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}
	models := []string{
		"gpt-4o",
		"claude-sonnet-4.6",
		"claude-opus-4.5",
		"claude-haiku-3.5",
	}
	writeJSON(w, http.StatusOK, models)
}

// handleChat responds to POST /api/chat.
//
// For the PoC it returns a canned mock response. When a real provider is
// configured this handler should proxy the request through the provider.
func handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	if len(req.Messages) == 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "messages cannot be empty"})
		return
	}

	last := req.Messages[len(req.Messages)-1]
	reply := fmt.Sprintf("[mock %s] I received your message: %q — how can I help further?", req.Model, last.Content)

	writeJSON(w, http.StatusOK, chatResponse{Content: reply})
}

// handleSwarmEvents responds to GET /api/swarm/events with mock swarm activity.
func handleSwarmEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	now := time.Now().UTC()
	events := []swarmEvent{
		{
			ID:        "evt_001",
			Type:      "delegation",
			AgentName: "planner",
			Payload:   map[string]interface{}{"to": "explorer", "task": "investigate codebase structure"},
			Timestamp: now.Add(-30 * time.Second).Format(time.RFC3339),
		},
		{
			ID:        "evt_002",
			Type:      "tool_call",
			AgentName: "explorer",
			Payload:   map[string]interface{}{"tool": "bash", "args": "ls -la ./internal"},
			Timestamp: now.Add(-25 * time.Second).Format(time.RFC3339),
		},
		{
			ID:        "evt_003",
			Type:      "plan",
			AgentName: "planner",
			Payload:   map[string]interface{}{"steps": []string{"scaffold web/", "add API", "write tests"}},
			Timestamp: now.Add(-20 * time.Second).Format(time.RFC3339),
		},
		{
			ID:        "evt_004",
			Type:      "tool_call",
			AgentName: "hephaestus",
			Payload:   map[string]interface{}{"tool": "write", "path": "web/src/main.ts"},
			Timestamp: now.Add(-15 * time.Second).Format(time.RFC3339),
		},
		{
			ID:        "evt_005",
			Type:      "review",
			AgentName: "momus",
			Payload:   map[string]interface{}{"verdict": "PASS", "notes": "TypeScript types look correct"},
			Timestamp: now.Add(-5 * time.Second).Format(time.RFC3339),
		},
	}
	writeJSON(w, http.StatusOK, events)
}
