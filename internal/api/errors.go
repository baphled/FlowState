package api

// Error sanitization for HTTP, SSE, and WebSocket responses.
//
// All client-facing error messages are routed through clientError, which maps
// internal errors to a fixed set of canonical messages and emits a correlation
// ID so operators can locate the matching server log entry without exposing
// raw error text to callers.
//
// See writeJSONError (HTTP), writeSSEClientError (SSE), and the BuildWSChunkMsg
// helper (WebSocket) for the sanitized call sites.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
)

// clientError maps an internal error to a safe client-facing message and a
// random correlation ID for log lookup. The raw error is logged server-side
// only; it is never included in the returned safeMsg.
func clientError(err error, category string) (safeMsg string, correlationID string) {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	correlationID = hex.EncodeToString(b)
	slog.Error("api error", "correlation_id", correlationID, "category", category, "error", err)
	switch category {
	case "session_not_found":
		safeMsg = "session not found"
	case "rate_limited":
		safeMsg = "rate limited"
	case "stream_error":
		safeMsg = "stream error"
	case "stream_critical":
		// Used by handleSessionStream's chunk-error gate to distinguish
		// fatal provider errors (revoked OAuth, 401, model-not-found,
		// quota lockout) from self-healing transient errors. The
		// critical-class category triggers the SSE fan-out to break
		// the receive loop and emit [DONE] so the session settles
		// into a state the UI can render. Sanitisation rules are the
		// same as stream_error — the underlying message is logged
		// server-side under the correlation ID, never sent to the
		// client.
		safeMsg = "critical stream error"
	case "stream_critical_context_exceeded":
		// Sibling of stream_critical for the proactive context-window
		// overflow gate (engine.checkContextWindowOverflow → wraps
		// provider.ErrorTypeContextWindowExceeded). The wire shape is
		// the same {error, correlation_id} envelope the chat store
		// already routes to CriticalErrorBanner, but the safeMsg is
		// distinct and user-actionable — the user can recover by
		// trimming recent tool results or starting a fresh session,
		// unlike a revoked-OAuth fatal error which requires operator
		// intervention. The Vue parser (web/src/lib/sseEvent.ts)
		// recognises the message text and routes the same way as the
		// generic stream_critical category.
		safeMsg = "context window exceeded — start a fresh session or trim recent tool results before retrying"
	case "cancel_error":
		safeMsg = "cancel failed"
	case "swarm_error":
		safeMsg = "invalid request"
	default:
		safeMsg = "internal error"
	}
	return safeMsg, correlationID
}

// writeJSONError writes a safe JSON error response of the form:
//
//	{"error":"<safeMsg>","correlation_id":"<id>"}
//
// The raw error is logged server-side with the correlation ID; callers can use
// the ID to locate the log entry.
func writeJSONError(w http.ResponseWriter, err error, category string, statusCode int) {
	safeMsg, cid := clientError(err, category)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	body := `{"error":` + jsonString(safeMsg) + `,"correlation_id":` + jsonString(cid) + `}`
	_, _ = w.Write([]byte(body))
}

// writeSSEClientError emits a sanitized SSE error event. The raw error is
// logged server-side; only the canonical message and correlation ID are sent
// to the client.
func writeSSEClientError(w http.ResponseWriter, flusher http.Flusher, err error, category string) {
	safeMsg, cid := clientError(err, category)
	writeSSEErrorMsg(w, flusher, safeMsg, cid)
}

// writeSSEErrorMsg emits an already-safe message and correlation ID as an SSE
// error event. It is the low-level variant used by writeSSEClientError; callers
// that already hold a safe message (e.g. after calling clientError directly)
// should use this function instead of writeSSEError.
func writeSSEErrorMsg(w http.ResponseWriter, flusher http.Flusher, safeMsg, correlationID string) {
	type sseClientError struct {
		Error         string `json:"error"`
		CorrelationID string `json:"correlation_id"`
	}
	data := sseClientError{Error: safeMsg, CorrelationID: correlationID}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// jsonString returns s encoded as a JSON string literal (including surrounding
// quotes). It uses encoding/json.Marshal so all special characters are properly
// escaped, preventing JSON injection when the value is embedded in a manually
// constructed JSON body.
func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		// json.Marshal fails only for non-UTF-8 strings or cycles; a plain
		// string cannot produce a cycle, so fall back to a safe literal.
		return `"internal error"`
	}
	return string(b)
}
