package api

import (
	"encoding/json"
	"net/http"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
)

// SSE writer helpers for the ephemeral `/api/chat` endpoint.
//
// Phase-4-Commit-2 of "Turn-Based Post-Then-Poll Architecture
// (May 2026)" retired the session-scoped SSE bridge (handleSessionStream,
// the SessionBroker fan-out, and the EventBus → SSE projection at
// event_bridge.go). Long-poll on GET /api/v1/sessions/{id}/turns/{turn_id}
// is now the sole live channel for sessioned chat.
//
// What survives in this file: only the helpers /api/chat's SSEConsumer
// (sse_consumer.go) and errors.go's writeSSEClientError still call.
// The retired session-scoped writers (writeSSEContextUsage,
// writeSSEContextCompacted, writeSSEGateFailed, writeSSEStreamingHeartbeat,
// writeSSEProviderChanged, writeSSEProviderQuota, writeSSEModelActive)
// were exclusively wired into handleSessionStream and went with it.

// sseChunk represents a single content chunk in a server-sent event stream.
type sseChunk struct {
	Content string `json:"content"`
}

// sseToolCall represents a tool call event in a server-sent event stream.
type sseToolCall struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Input  string `json:"input,omitempty"`
}

// sseSkillLoad represents a skill load event in a server-sent event stream.
type sseSkillLoad struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// sseToolResult represents a tool result event in a server-sent event stream.
type sseToolResult struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// sseHarnessRetry represents a harness retry event in a server-sent event stream.
type sseHarnessRetry struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// sseAttemptStart represents a harness attempt start event in a server-sent event stream.
type sseAttemptStart struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// sseHarnessComplete represents a harness completion event in a server-sent event stream.
type sseHarnessComplete struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// sseCriticFeedback represents a harness critic feedback event in a server-sent event stream.
type sseCriticFeedback struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// writeSSEContent marshals content as a JSON chunk and writes it as a server-sent event.
func writeSSEContent(w http.ResponseWriter, flusher http.Flusher, content string) {
	data := sseChunk{Content: content}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSEDone writes the completion marker as a server-sent event.
func writeSSEDone(w http.ResponseWriter, flusher http.Flusher) {
	writeSSE(w, flusher, "[DONE]")
}

// writeSSEToolCall marshals a tool call as a JSON event and writes it as a server-sent event.
func writeSSEToolCall(w http.ResponseWriter, flusher http.Flusher, name, input string) {
	data := sseToolCall{Type: "tool_call", Name: name, Status: "running", Input: input}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSESkillLoad marshals a skill load as a JSON event and writes it as a server-sent event.
func writeSSESkillLoad(w http.ResponseWriter, flusher http.Flusher, name string) {
	data := sseSkillLoad{Type: "skill_load", Name: name}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSEToolResult marshals a tool result as a JSON event and writes it as a server-sent event.
func writeSSEToolResult(w http.ResponseWriter, flusher http.Flusher, content string) {
	data := sseToolResult{Type: "tool_result", Content: content}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSEHarnessRetry marshals a harness retry as a JSON event and writes it as a server-sent event.
func writeSSEHarnessRetry(w http.ResponseWriter, flusher http.Flusher, content string) {
	data := sseHarnessRetry{Type: "harness_retry", Content: content}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSEAttemptStart marshals a harness attempt start as a JSON event and writes it as a server-sent event.
func writeSSEAttemptStart(w http.ResponseWriter, flusher http.Flusher, content string) {
	data := sseAttemptStart{Type: "harness_attempt_start", Content: content}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSEHarnessComplete marshals a harness completion as a JSON event and writes it as a server-sent event.
func writeSSEHarnessComplete(w http.ResponseWriter, flusher http.Flusher, content string) {
	data := sseHarnessComplete{Type: "harness_complete", Content: content}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSECriticFeedback marshals harness critic feedback as a JSON event and writes it as a server-sent event.
func writeSSECriticFeedback(w http.ResponseWriter, flusher http.Flusher, content string) {
	data := sseCriticFeedback{Type: "harness_critic_feedback", Content: content}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSEDelegation marshals a delegation event as JSON and writes it as a server-sent event.
func writeSSEDelegation(w http.ResponseWriter, flusher http.Flusher, event streaming.DelegationEvent) {
	jsonData, err := json.Marshal(event)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSEDelegationInfo emits a plain SSE data line carrying the JSON-encoded
// provider DelegationInfo payload. A "type":"delegation" field is injected so
// the frontend message listener can route by type rather than by SSE event name.
// Named SSE events (event: delegation) are only delivered to addEventListener
// listeners registered for that specific name — not to the generic "message"
// listener the frontend uses — so we must emit a plain data: line instead.
func writeSSEDelegationInfo(w http.ResponseWriter, flusher http.Flusher, info *provider.DelegationInfo) {
	if info == nil {
		return
	}
	type payload struct {
		Type string `json:"type"`
		*provider.DelegationInfo
	}
	data := payload{Type: "delegation", DelegationInfo: info}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSE writes a server-sent event data line and flushes the response buffer.
func writeSSE(w http.ResponseWriter, flusher http.Flusher, data string) {
	if _, err := w.Write([]byte("data: " + data + "\n\n")); err != nil {
		return
	}
	flusher.Flush()
}
