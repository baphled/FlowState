package streaming

import (
	"context"

	"github.com/baphled/flowstate/internal/provider"
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
		deliverToolCall(c, chunk.ToolCall)
		deliverToolResult(c, chunk.ToolResult)
		if chunk.Content != "" && writeErr == nil {
			writeErr = c.WriteChunk(chunk.Content)
		}
		if chunk.Done {
			break
		}
	}

	return writeErr
}

// deliverToolCall extracts the tool call name and delivers it to the consumer.
//
// Expected:
//   - c is a non-nil StreamConsumer.
//   - toolCall may be nil.
//
// Side effects:
//   - If toolCall is not nil and c implements ToolCallConsumer, calls c.WriteToolCall.
//   - Extracts the skill name from skill_load tool calls and prefixes with "skill:".
func deliverToolCall(c StreamConsumer, toolCall *provider.ToolCall) {
	if toolCall == nil {
		return
	}
	tc, ok := c.(ToolCallConsumer)
	if !ok {
		return
	}

	name := toolCall.Name
	if name == "skill_load" {
		if skillName, ok := toolCall.Arguments["name"].(string); ok && skillName != "" {
			name = "skill:" + skillName
		}
	}
	tc.WriteToolCall(name)
}

// deliverToolResult delivers the tool result to the consumer.
//
// Expected:
//   - c is a non-nil StreamConsumer.
//   - result may be nil.
//
// Side effects:
//   - If result is not nil and c implements ToolResultConsumer, calls c.WriteToolResult.
func deliverToolResult(c StreamConsumer, result *provider.ToolResultInfo) {
	if result == nil {
		return
	}
	trc, ok := c.(ToolResultConsumer)
	if !ok {
		return
	}
	trc.WriteToolResult(result.Content)
}
