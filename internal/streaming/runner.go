package streaming

import (
	"context"
)

// Run drives a Streamer into a StreamConsumer, coordinating the streaming lifecycle.
//
// Expected:
//   - ctx is a valid context for the streaming operation.
//   - s is a non-nil Streamer implementation.
//   - c is a non-nil StreamConsumer implementation.
//   - agentID identifies the agent to stream from.
//   - message is the user's input text.
//
// Returns:
//   - nil on success.
//   - The Stream error if the initial stream call fails.
//   - The first WriteChunk error if content delivery fails.
//
// Side effects:
//   - Calls c.WriteError for stream-level and chunk-level errors.
//   - Calls c.Done after the stream completes regardless of errors.
func Run(ctx context.Context, s Streamer, c StreamConsumer, agentID, message string) error {
	defer c.Done()

	ch, err := s.Stream(ctx, agentID, message)
	if err != nil {
		c.WriteError(err)
		return err
	}

	var writeErr error
	for chunk := range ch {
		if chunk.Error != nil {
			c.WriteError(chunk.Error)
			continue
		}
		if chunk.ToolCall != nil {
			if tc, ok := c.(ToolCallConsumer); ok {
				tc.WriteToolCall(chunk.ToolCall.Name)
			}
		}
		if chunk.Content != "" && writeErr == nil {
			writeErr = c.WriteChunk(chunk.Content)
		}
		if chunk.Done {
			break
		}
	}

	return writeErr
}
