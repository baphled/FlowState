package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/baphled/flowstate/internal/streaming"
)

// WriterConsumer implements streaming.StreamConsumer for CLI output.
type WriterConsumer struct {
	w        io.Writer
	silent   bool
	jsonMode bool
	response strings.Builder
	err      error
}

// NewWriterConsumer creates a WriterConsumer that writes to w unless silent is true.
//
// Expected:
//   - w is a non-nil io.Writer for output delivery.
//   - silent controls whether content is written to w (true skips writing).
//
// Returns:
//   - A configured WriterConsumer ready to receive chunks.
//
// Side effects:
//   - None.
func NewWriterConsumer(w io.Writer, silent bool) *WriterConsumer {
	return &WriterConsumer{w: w, silent: silent}
}

// WriteChunk accumulates content and optionally writes it to the underlying writer.
//
// Expected:
//   - content is a non-empty string fragment.
//
// Returns:
//   - nil on success, or an error if writing to the underlying writer fails.
//
// Side effects:
//   - Appends content to the internal response buffer.
//   - Writes content to the writer unless silent is true.
func (c *WriterConsumer) WriteChunk(content string) error {
	c.response.WriteString(content)
	if !c.silent {
		_, err := fmt.Fprint(c.w, content)
		return err
	}
	return nil
}

// WriteError stores a streaming error for later retrieval.
//
// Expected:
//   - err is the error encountered during streaming.
//
// Side effects:
//   - Stores the error for retrieval via Err().
func (c *WriterConsumer) WriteError(err error) {
	c.err = err
}

// Done signals stream completion. This is a no-op for CLI consumers.
//
// Side effects:
//   - None.
func (c *WriterConsumer) Done() {
}

// Response returns the accumulated response content.
//
// Returns:
//   - The concatenated content from all WriteChunk calls.
//
// Side effects:
//   - None.
func (c *WriterConsumer) Response() string {
	return c.response.String()
}

// Err returns the last error passed to WriteError.
//
// Returns:
//   - The stored error, or nil if no error occurred.
//
// Side effects:
//   - None.
func (c *WriterConsumer) Err() error {
	return c.err
}

// WriteToolCall notifies the consumer of a tool invocation by name.
//
// Expected:
//   - name is the tool name being invoked, optionally prefixed with "skill:".
//
// Side effects:
//   - Writes "📚 <name>\n" for skill calls, or "🔧 <name>...\n" for other tools, to the writer unless silent is true.
func (c *WriterConsumer) WriteToolCall(name string) {
	if c.silent {
		return
	}
	if strings.HasPrefix(name, "skill:") {
		fmt.Fprintf(c.w, "📚 %s\n", strings.TrimPrefix(name, "skill:"))
		return
	}
	fmt.Fprintf(c.w, "🔧 %s...\n", name)
}

// WriteToolResult notifies the consumer of a tool result.
//
// Expected:
//   - content is the tool result content.
//
// Side effects:
//   - Writes "📤 <content>\n" to the writer unless silent is true.
func (c *WriterConsumer) WriteToolResult(content string) {
	if !c.silent {
		fmt.Fprintf(c.w, "📤 %s\n", content)
	}
}

// WriteHarnessRetry notifies the consumer that plan validation failed and a retry is starting.
//
// Expected:
//   - content describes the validation failure and retry reason.
//
// Side effects:
//   - Writes a retry banner to the writer unless silent is true.
func (c *WriterConsumer) WriteHarnessRetry(content string) {
	if !c.silent {
		fmt.Fprintf(c.w, "\n🔄 %s\n\n", content)
	}
}

// WithJSONMode returns the consumer configured to emit JSON lines instead of human-readable text.
//
// Returns:
//   - The same WriterConsumer with JSON mode enabled, for chaining.
//
// Side effects:
//   - Mutates the jsonMode flag on the receiver.
func (c *WriterConsumer) WithJSONMode() *WriterConsumer {
	c.jsonMode = true
	return c
}

// WriteDelegation delivers a delegation status event to the consumer.
//
// Expected:
//   - event contains the delegation metadata including target agent and status.
//
// Returns:
//   - nil on success, or an error if writing to the underlying writer fails.
//
// Side effects:
//   - Writes formatted delegation status to the writer unless silent is true.
//   - In JSON mode, emits the event as a single JSON line.
func (c *WriterConsumer) WriteDelegation(event streaming.DelegationEvent) error {
	if c.silent {
		return nil
	}
	if c.jsonMode {
		return c.writeDelegationJSON(event)
	}
	return c.writeDelegationText(event)
}

// writeDelegationJSON emits a delegation event as a JSON line.
//
// Expected:
//   - event contains the delegation metadata to serialise.
//
// Returns:
//   - nil on success, or an error if marshaling or writing fails.
//
// Side effects:
//   - Writes a single JSON line to the underlying writer.
func (c *WriterConsumer) writeDelegationJSON(event streaming.DelegationEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshalling delegation event: %w", err)
	}
	_, err = fmt.Fprintf(c.w, "%s\n", data)
	return err
}

// writeDelegationText emits a delegation event as human-readable text.
//
// Expected:
//   - event contains the delegation metadata to format.
//
// Returns:
//   - nil on success, or an error if writing fails.
//
// Side effects:
//   - Writes formatted delegation text to the underlying writer.
func (c *WriterConsumer) writeDelegationText(event streaming.DelegationEvent) error {
	line := formatDelegationText(event)
	_, err := fmt.Fprint(c.w, line)
	return err
}

// formatDelegationText returns the formatted text for a delegation event.
//
// Expected:
//   - event contains the delegation metadata and status.
//
// Returns:
//   - A human-readable delegation status line with a trailing newline.
//
// Side effects:
//   - None.
func formatDelegationText(event streaming.DelegationEvent) string {
	switch event.Status {
	case "started":
		return fmt.Sprintf("⟶ Delegating to %s (%s via %s): %s\n",
			event.TargetAgent, event.ModelName, event.ProviderName, event.Description)
	case "completed":
		return fmt.Sprintf("✓ Delegation to %s completed (%d tool calls)\n",
			event.TargetAgent, event.ToolCalls)
	case "failed":
		return fmt.Sprintf("✗ Delegation to %s failed\n", event.TargetAgent)
	default:
		return fmt.Sprintf("⟶ Delegation to %s: %s\n", event.TargetAgent, event.Status)
	}
}
