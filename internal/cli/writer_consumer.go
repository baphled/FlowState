package cli

import (
	"fmt"
	"io"
	"strings"
)

// WriterConsumer implements streaming.StreamConsumer for CLI output.
type WriterConsumer struct {
	w        io.Writer
	silent   bool
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
