package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/baphled/flowstate/internal/streaming"
)

// JSONConsumer implements streaming.StreamConsumer for JSON line output.
type JSONConsumer struct {
	w        io.Writer
	response string
	err      error
}

// NewJSONConsumer creates a JSONConsumer that writes JSON lines to w.
//
// Expected:
//   - w is a non-nil io.Writer for output delivery.
//
// Returns:
//   - A configured JSONConsumer ready to receive chunks.
//
// Side effects:
//   - None.
func NewJSONConsumer(w io.Writer) *JSONConsumer {
	return &JSONConsumer{w: w}
}

// WriteChunk writes a JSON-encoded chunk event.
//
// Expected:
//   - content is a non-empty string fragment.
//
// Returns:
//   - nil on success, or an error if marshaling or writing fails.
//
// Side effects:
//   - Appends content to the internal response buffer.
//   - Writes a JSON line to the writer.
func (c *JSONConsumer) WriteChunk(content string) error {
	c.response += content
	event := map[string]string{"type": "chunk", "content": content}
	return c.writeEvent(event)
}

// WriteError writes a JSON-encoded error event.
//
// Expected:
//   - err is the error encountered during streaming.
//
// Side effects:
//   - Stores the error for retrieval via Err().
//   - Writes a JSON line to the writer.
func (c *JSONConsumer) WriteError(err error) {
	c.err = err
	event := map[string]string{"type": "error", "error": err.Error()}
	if writeErr := c.writeEvent(event); writeErr != nil {
		c.err = writeErr
	}
}

// Done writes a JSON-encoded done event.
//
// Side effects:
//   - Writes a JSON line to the writer.
func (c *JSONConsumer) Done() {
	event := map[string]string{"type": "done"}
	if err := c.writeEvent(event); err != nil {
		c.err = err
	}
}

// Response returns the accumulated response content.
//
// Returns:
//   - The concatenated content from all WriteChunk calls.
//
// Side effects:
//   - None.
func (c *JSONConsumer) Response() string {
	return c.response
}

// Err returns the last error passed to WriteError.
//
// Returns:
//   - The stored error, or nil if no error occurred.
//
// Side effects:
//   - None.
func (c *JSONConsumer) Err() error {
	return c.err
}

// WriteToolCall writes a JSON-encoded tool call event.
//
// Expected:
//   - name is the tool name being invoked, optionally prefixed with "skill:".
//
// Side effects:
//   - Writes a JSON line to the writer.
func (c *JSONConsumer) WriteToolCall(name string) {
	event := map[string]string{"type": "tool_call", "name": name}
	if err := c.writeEvent(event); err != nil {
		c.err = err
	}
}

// WriteToolResult writes a JSON-encoded tool result event.
//
// Expected:
//   - content is the tool result content.
//
// Side effects:
//   - Writes a JSON line to the writer.
func (c *JSONConsumer) WriteToolResult(content string) {
	event := map[string]string{"type": "tool_result", "content": content}
	if err := c.writeEvent(event); err != nil {
		c.err = err
	}
}

// WriteDelegation writes a JSON-encoded delegation event.
//
// Expected:
//   - event contains the delegation metadata including source/target agents and status.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Writes a JSON line to the writer.
func (c *JSONConsumer) WriteDelegation(event streaming.DelegationEvent) error {
	delegationEvent := map[string]string{
		"type":   "delegation",
		"source": event.SourceAgent,
		"target": event.TargetAgent,
		"status": event.Status,
		"model":  event.ModelName,
	}
	if event.ProviderName != "" {
		delegationEvent["provider"] = event.ProviderName
	}
	return c.writeEvent(delegationEvent)
}

// writeEvent marshals and writes a JSON line to the writer.
//
// Expected:
//   - event is a map containing JSON-serializable values.
//
// Returns:
//   - nil on success, or an error if marshaling or writing fails.
//
// Side effects:
//   - Writes a JSON line terminated with newline to the writer.
func (c *JSONConsumer) writeEvent(event map[string]string) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshalling event: %w", err)
	}
	_, err = fmt.Fprintf(c.w, "%s\n", data)
	return err
}
