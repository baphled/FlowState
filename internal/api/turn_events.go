// Package api — per-turn SSE event stream.
//
// handleTurnEvents streams a Turn's live events over SSE at
// GET /api/v1/sessions/{id}/turns/{turn_id}/events. It reverses —
// deliberately and narrowly — the Phase-4-Commit-2 retirement of the
// session-scoped SSE bridge: unlike the retired handleSessionStream
// fan-out, this endpoint is scoped to a single turn_id, derives its
// events from the Turn registry (WaitForChange), and terminates on
// the turn's terminal state. The wire format matches what the
// frontend parser (flowstate-web src/lib/sseEvent.ts
// parseSSEPayload → chatStore.applyContentEvent) already accepts:
// plain `data: <payload>` lines, no named events, with the literal
// `[DONE]` sentinel terminating the stream.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/turn"
)

// turnEventWaitChunk is the maximum time a single WaitForChange call
// holds before the loop re-checks the request context and emits a
// heartbeat. Kept below the 20s sseHeartbeatInterval cadence budget
// so idle streams stay lively without busy-polling.
const turnEventWaitChunk = 10 * time.Second

// sseTurnToolCall is the wire shape for a tool_call event derived
// from a persisted Role:"tool_call" message.
type sseTurnToolCall struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Input  string `json:"input,omitempty"`
}

// sseTurnToolResult is the wire shape for a tool_result event
// derived from a persisted tool-role message.
type sseTurnToolResult struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// sseTurnToolError is the wire shape for a tool_error event derived
// from a persisted tool-role message whose Status is "error".
type sseTurnToolError struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// sseTurnThinking is the wire shape for a thinking event derived
// from a persisted Role:"thinking" message.
type sseTurnThinking struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// sseTurnDelegation is the wire shape for a delegation event derived
// from a persisted Role:"delegation_started" message, carrying the
// snake_case fields the frontend parser reads.
type sseTurnDelegation struct {
	Type        string `json:"type"`
	TargetAgent string `json:"target_agent,omitempty"`
	ChainID     string `json:"chain_id,omitempty"`
	ToolCalls   int    `json:"tool_calls,omitempty"`
	LastTool    string `json:"last_tool,omitempty"`
	Status      string `json:"status,omitempty"`
}

// sseTurnModelActive is the wire shape for a model_active event
// emitted once at stream start when the turn records its
// provider/model pair.
type sseTurnModelActive struct {
	Type     string `json:"type"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// sseTurnError is the wire shape for an error event emitted when the
// turn settles with a critical error or failure.
type sseTurnError struct {
	Type    string `json:"type"`
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

// handleTurnEvents streams a Turn's events as SSE.
//
// Expected:
//   - Request path parameter "turn_id" is the Turn UUID returned by
//     POST /messages ("id" is kept for route symmetry; the registry
//     is keyed by turn_id alone).
//
// Returns:
//   - 200 with text/event-stream; one `data: <payload>` line per
//     new registry event, `[DONE]` on terminal state.
//   - 404 when the turn_id is unknown.
//   - 501 when the dispatcher / TurnRegistry is not configured.
//
// Side effects:
//   - Holds the connection open until terminal state, client
//     disconnect, or write failure; heartbeat comments every 20s
//     of idleness via the shared sse_writers helpers.
func (s *Server) handleTurnEvents(w http.ResponseWriter, r *http.Request) {
	registry, initial, flusher, ok := s.openTurnEventStream(w, r)
	if !ok {
		return
	}

	if initial.CurrentProvider != "" || initial.CurrentModel != "" {
		writeSSE(w, flusher, mustJSON(sseTurnModelActive{
			Type:     "model_active",
			Provider: initial.CurrentProvider,
			Model:    initial.CurrentModel,
		}))
	}

	turnID := r.PathValue("turn_id")
	sent := 0
	lastProvider := initial.CurrentProvider
	lastModel := initial.CurrentModel
	writeNewMessages(w, flusher, initial.MessagesAdded, &sent)

	snapshot := initial
	heartbeat := time.NewTimer(sseHeartbeatInterval)
	defer heartbeat.Stop()

	for snapshot.Status == turn.StatusRunning {
		t, changed := waitTurnChange(registry, r, turnID, sent, snapshot)
		if !changed {
			var open bool
			snapshot, open = handleTurnIdle(w, r, flusher, heartbeat, t, snapshot)
			if !open {
				return
			}
			continue
		}
		snapshot = t
		lastProvider, lastModel = writeTurnModelChange(
			w, flusher, lastProvider, lastModel,
			snapshot.CurrentProvider, snapshot.CurrentModel,
		)
		writeNewMessages(w, flusher, snapshot.MessagesAdded, &sent)
		if snapshot.CriticalError != nil {
			writeSSE(w, flusher, mustJSON(sseTurnError{
				Type:  "error",
				Error: "critical stream error",
			}))
		}
		resetSSEHeartbeat(heartbeat)
	}

	writeTurnTerminal(w, flusher, snapshot)
}

// openTurnEventStream validates the per-turn stream request, writes
// the SSE response headers, and returns the turn registry alongside
// the turn's initial snapshot and the response flusher.
//
// Expected: the request carries a non-empty "turn_id" path value.
//
// Returns:
//   - ok=false after writing 400/404/501/500 directly; the caller
//     must return without further writes.
//
// Side effects: writes SSE response headers and flushes on success;
// writes an HTTP error on failure.
func (s *Server) openTurnEventStream(w http.ResponseWriter, r *http.Request) (*turn.Registry, turn.Turn, http.Flusher, bool) {
	if s.dispatcher == nil {
		http.Error(w, "dispatcher not configured", http.StatusNotImplemented)
		return nil, turn.Turn{}, nil, false
	}
	registry := s.dispatcher.TurnRegistry()
	if registry == nil {
		http.Error(w, "turn registry not configured", http.StatusNotImplemented)
		return nil, turn.Turn{}, nil, false
	}
	turnID := r.PathValue("turn_id")
	if turnID == "" {
		http.Error(w, "turn_id required", http.StatusBadRequest)
		return nil, turn.Turn{}, nil, false
	}
	initial, err := registry.Get(turnID)
	if err != nil {
		if errors.Is(err, turn.ErrTurnNotFound) {
			http.Error(w, "turn not found", http.StatusNotFound)
			return nil, turn.Turn{}, nil, false
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, turn.Turn{}, nil, false
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return nil, turn.Turn{}, nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	return registry, initial, flusher, true
}

// waitTurnChange awaits the next registry change for the turn,
// chunking the wait so the caller can re-check its request context.
//
// Expected: registry and snapshot refer to turnID; sent is the
// caller's last-known change cursor.
//
// Side effects: None.
//
// Returns: the registry's turn snapshot and whether anything changed.
func waitTurnChange(
	registry *turn.Registry,
	r *http.Request,
	turnID string,
	sent int,
	snapshot turn.Turn,
) (turn.Turn, bool) {
	waitCtx, cancel := context.WithTimeout(r.Context(), turnEventWaitChunk)
	defer cancel()
	return registry.WaitForChange(
		waitCtx,
		turnID,
		sent,
		snapshot.Phase,
		snapshot.TokenCount,
		snapshot.CurrentProvider,
		snapshot.CurrentModel,
		snapshot.ContextUsage,
		snapshot.ProviderQuotas,
		len(snapshot.CompactionEvents),
		len(snapshot.GateFailures),
		snapshot.CriticalError,
		snapshot.PermissionRequests,
		snapshot.QuestionRequests,
		turnEventWaitChunk,
	)
}

// handleTurnIdle handles an unchanged WaitForChange wake-up: client
// disconnect ends the stream, otherwise a heartbeat comment is
// written and the snapshot is refreshed when the registry returned
// a populated turn.
//
// Expected: flusher and heartbeat belong to the caller's stream.
//
// Side effects: writes an SSE heartbeat comment and resets the
// heartbeat timer.
//
// Returns: the (possibly refreshed) snapshot and whether the stream
// is still open.
func handleTurnIdle(
	w http.ResponseWriter,
	r *http.Request,
	flusher http.Flusher,
	heartbeat *time.Timer,
	t turn.Turn,
	snapshot turn.Turn,
) (turn.Turn, bool) {
	if r.Context().Err() != nil {
		return snapshot, false
	}
	if t.ID != "" {
		snapshot = t
	}
	writeSSEComment(w, flusher, "ping")
	resetSSEHeartbeat(heartbeat)
	return snapshot, true
}

// writeTurnModelChange emits a model_active event when the turn's
// provider/model pair changed since the last emission, updating the
// caller's last-seen pair.
//
// Side effects: writes one SSE event when a non-empty pair changed.
//
// Expected: lastProvider/lastModel are the previously emitted pair.
//
// Returns: the updated last-seen provider and model.
func writeTurnModelChange(
	w http.ResponseWriter,
	flusher http.Flusher,
	lastProvider, lastModel, provider, model string,
) (string, string) {
	if provider == lastProvider && model == lastModel {
		return lastProvider, lastModel
	}
	if provider == "" && model == "" {
		return provider, model
	}
	writeSSE(w, flusher, mustJSON(sseTurnModelActive{
		Type:     "model_active",
		Provider: provider,
		Model:    model,
	}))
	return provider, model
}

// writeTurnTerminal emits the terminal error (if any) and the [DONE]
// sentinel that closes a turn event stream.
//
// Expected: snapshot is the final turn state after the stream loop.
//
// Side effects: writes SSE events.
func writeTurnTerminal(w http.ResponseWriter, flusher http.Flusher, snapshot turn.Turn) {
	if snapshot.CriticalError != nil {
		writeSSE(w, flusher, mustJSON(sseTurnError{
			Type:  "error",
			Error: "critical stream error",
		}))
	} else if snapshot.Status == turn.StatusFailed {
		writeSSE(w, flusher, mustJSON(sseTurnError{
			Type:  "error",
			Error: "turn failed",
		}))
	}
	writeSSEDone(w, flusher)
}

// writeNewMessages emits SSE payloads for every MessagesAdded row past
// the sent cursor, mapping persisted roles onto the frontend's
// discriminated-union wire shapes.
//
// Expected:
//   - msgs is a snapshot copy of Turn.MessagesAdded; *sent is the
//     count of rows already emitted in prior calls.
//
// Side effects:
//   - Writes SSE events and advances *sent.
func writeNewMessages(w http.ResponseWriter, flusher http.Flusher, msgs []session.Message, sent *int) {
	for *sent < len(msgs) {
		msg := msgs[*sent]
		*sent++
		emitTurnMessage(w, flusher, msg)
	}
}

// emitTurnMessage maps a single persisted message onto the SSE wire.
//
// Expected: msg is a MessagesAdded row snapshot.
//
// Side effects: writes one SSE event (or nothing for unmapped roles).
func emitTurnMessage(w http.ResponseWriter, flusher http.Flusher, msg session.Message) {
	switch msg.Role {
	case "assistant":
		writeSSEContent(w, flusher, msg.Content)
	case "thinking":
		writeSSE(w, flusher, mustJSON(sseTurnThinking{
			Type:    "thinking",
			Content: msg.Content,
		}))
	case "tool_call":
		writeSSE(w, flusher, mustJSON(sseTurnToolCall{
			Type:   "tool_call",
			Name:   msg.ToolName,
			Status: "running",
			Input:  msg.ToolInput,
		}))
	case "tool":
		if msg.Status == "error" {
			writeSSE(w, flusher, mustJSON(sseTurnToolError{
				Type:    "tool_error",
				Content: msg.Content,
			}))
		} else {
			writeSSE(w, flusher, mustJSON(sseTurnToolResult{
				Type:    "tool_result",
				Content: msg.Content,
			}))
		}
	case "delegation_started", "delegation":
		writeSSE(w, flusher, mustJSON(sseTurnDelegation{
			Type:        "delegation",
			TargetAgent: msg.TargetAgent,
			ChainID:     msg.ChainID,
			ToolCalls:   msg.ToolCalls,
			LastTool:    msg.LastTool,
			Status:      msg.Status,
		}))
	}
}

// rejectEmptyTurnID returns a middleware that rejects per-turn
// requests whose turn_id path segment is empty before ServeMux's
// path-cleaning pass can answer with a 307 redirect.
//
// Expected: wrapped handlers see a non-empty "turns/{turn_id}" segment.
//
// Side effects: writes 400 for paths with an empty turn segment.
//
// Returns: the wrapping middleware handler.
func rejectEmptyTurnID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if turnSegmentEmpty(r.URL.Path) {
			http.Error(w, "turn_id required", http.StatusBadRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// turnSegmentEmpty reports whether path contains an empty turn id
// segment, i.e. a "turns/" immediately followed by "/".
//
// Expected: path is a request path under the session-scope router.
//
// Side effects: None.
//
// Returns: true when such a segment exists.
func turnSegmentEmpty(path string) bool {
	for i := 0; i+8 <= len(path); i++ {
		if path[i:i+8] == "/turns//" && (i+8 == len(path) || path[i+8] != '/') {
			return true
		}
	}
	return false
}

// mustJSON marshals v, returning "{}" on failure. Used only with the
// flat struct payloads above whose marshal cannot fail.
//
// Side effects: None.
//
// Expected: v is one of the flat SSE payload structs above.
//
// Returns: the JSON encoding of v, or "{}" on marshal failure.
func mustJSON(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(data)
}
