package api

import (
	"net/http"
	"strings"

	"github.com/baphled/flowstate/internal/streaming"
)

// SSEConsumer implements streaming.StreamConsumer for server-sent event responses.
type SSEConsumer struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

// NewSSEConsumer creates an SSEConsumer if the ResponseWriter supports flushing.
//
// Expected:
//   - w is an http.ResponseWriter that may implement http.Flusher.
//
// Returns:
//   - A configured SSEConsumer and true if w supports flushing.
//   - nil and false if w does not support flushing.
//
// Side effects:
//   - None.
func NewSSEConsumer(w http.ResponseWriter) (*SSEConsumer, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	return &SSEConsumer{w: w, flusher: flusher}, true
}

// WriteChunk writes a JSON-encoded content chunk as a server-sent event.
//
// Expected:
//   - content is the text to send in the SSE chunk.
//
// Returns:
//   - nil on success.
//   - An error if JSON marshalling fails (unlikely for string content).
//
// Side effects:
//   - Writes SSE data line with JSON-encoded chunk to the response.
//   - Flushes the response buffer.
func (c *SSEConsumer) WriteChunk(content string) error {
	writeSSEContent(c.w, c.flusher, content)
	return nil
}

// WriteError writes a JSON-encoded error as a server-sent event.
//
// Expected:
//   - err is the error to report to the client.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded error to the response.
//   - Flushes the response buffer.
func (c *SSEConsumer) WriteError(err error) {
	writeSSEError(c.w, c.flusher, err.Error())
}

// Done writes the completion sentinel as a server-sent event.
//
// Side effects:
//   - Writes SSE data line with "[DONE]" marker to the response.
//   - Flushes the response buffer.
func (c *SSEConsumer) Done() {
	writeSSEDone(c.w, c.flusher)
}

// WriteToolCall writes a JSON-encoded tool call event as a server-sent event.
//
// Expected:
//   - name is the name of the tool being invoked, optionally prefixed with "skill:".
//
// Side effects:
//   - Writes SSE data line with JSON-encoded skill load or tool call to the response.
//   - Flushes the response buffer.
func (c *SSEConsumer) WriteToolCall(name string) {
	if strings.HasPrefix(name, "skill:") {
		writeSSESkillLoad(c.w, c.flusher, strings.TrimPrefix(name, "skill:"))
		return
	}
	writeSSEToolCall(c.w, c.flusher, name)
}

// WriteToolResult writes a JSON-encoded tool result event as a server-sent event.
//
// Expected:
//   - content is the result content from the tool execution.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded tool result to the response.
//   - Flushes the response buffer.
func (c *SSEConsumer) WriteToolResult(content string) {
	writeSSEToolResult(c.w, c.flusher, content)
}

// WriteHarnessRetry writes a JSON-encoded harness retry event as a server-sent event.
//
// Expected:
//   - content describes the validation failure and retry reason.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded harness retry event to the response.
//   - Flushes the response buffer.
func (c *SSEConsumer) WriteHarnessRetry(content string) {
	writeSSEHarnessRetry(c.w, c.flusher, content)
}

// WriteAttemptStart writes a JSON-encoded harness attempt start event as a server-sent event.
//
// Expected:
//   - content describes the attempt being started.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded attempt start event to the response.
//   - Flushes the response buffer.
func (c *SSEConsumer) WriteAttemptStart(content string) {
	writeSSEAttemptStart(c.w, c.flusher, content)
}

// WriteComplete writes a JSON-encoded harness completion event as a server-sent event.
//
// Expected:
//   - content describes the evaluation outcome.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded harness complete event to the response.
//   - Flushes the response buffer.
func (c *SSEConsumer) WriteComplete(content string) {
	writeSSEHarnessComplete(c.w, c.flusher, content)
}

// WriteCriticFeedback writes a JSON-encoded harness critic feedback event as a server-sent event.
//
// Expected:
//   - content describes the critic's feedback on the plan.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded critic feedback event to the response.
//   - Flushes the response buffer.
func (c *SSEConsumer) WriteCriticFeedback(content string) {
	writeSSECriticFeedback(c.w, c.flusher, content)
}

// WriteDelegation writes a JSON-encoded delegation event as a server-sent event.
//
// Expected:
//   - event contains delegation metadata including source/target agents and status.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded delegation event to the response.
//   - Flushes the response buffer.
func (c *SSEConsumer) WriteDelegation(event streaming.DelegationEvent) error {
	writeSSEDelegation(c.w, c.flusher, event)
	return nil
}
