package api

import (
	"net/http"
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
