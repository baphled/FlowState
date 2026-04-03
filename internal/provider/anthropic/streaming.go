package anthropic

import (
	"encoding/json"
	"strings"

	anthropicAPI "github.com/anthropics/anthropic-sdk-go"
	"github.com/baphled/flowstate/internal/provider"
)

// streamEventHandler accumulates tool call arguments across streaming events.
//
// Expected:
//   - Created via newStreamEventHandler before processing a stream.
//
// Side effects:
//   - None (state is internal to the handler).
type streamEventHandler struct {
	toolArgsBuf      map[int64]*strings.Builder
	pendingToolCalls map[int64]*provider.ToolCall
}

// newStreamEventHandler creates a handler for processing Anthropic streaming events.
//
// Returns:
//   - An initialised streamEventHandler ready to process events.
//
// Side effects:
//   - None.
func newStreamEventHandler() *streamEventHandler {
	return &streamEventHandler{
		toolArgsBuf:      make(map[int64]*strings.Builder),
		pendingToolCalls: make(map[int64]*provider.ToolCall),
	}
}

// handleEvent processes a single Anthropic streaming event and returns a chunk if one should be sent.
//
// Expected:
//   - event is a valid Anthropic streaming event.
//
// Returns:
//   - A StreamChunk and true if the event produced sendable output.
//   - An empty StreamChunk and false if the event was consumed silently.
//
// Side effects:
//   - May mutate internal accumulation buffers for tool call arguments.
func (h *streamEventHandler) handleEvent(
	event anthropicAPI.MessageStreamEventUnion,
) (provider.StreamChunk, bool) {
	switch event.Type {
	case "content_block_start":
		return h.handleContentBlockStart(event)
	case "content_block_delta":
		return h.handleContentBlockDelta(event)
	case "content_block_stop":
		return h.handleContentBlockStop(event)
	case "message_stop":
		return provider.StreamChunk{Done: true}, true
	default:
		return provider.StreamChunk{}, false
	}
}

// handleContentBlockStart registers a new tool call when a tool_use block begins.
//
// Expected:
//   - event.Type is "content_block_start".
//
// Returns:
//   - An empty StreamChunk and false (tool calls are emitted on block stop).
//
// Side effects:
//   - Registers the tool call ID, name, and argument buffer for the block index.
func (h *streamEventHandler) handleContentBlockStart(
	event anthropicAPI.MessageStreamEventUnion,
) (provider.StreamChunk, bool) {
	if event.ContentBlock.Type == "tool_use" {
		h.pendingToolCalls[event.Index] = &provider.ToolCall{
			ID:   event.ContentBlock.ID,
			Name: event.ContentBlock.Name,
		}
		h.toolArgsBuf[event.Index] = &strings.Builder{}
	}
	return provider.StreamChunk{}, false
}

// handleContentBlockDelta processes text or tool argument fragments.
//
// Expected:
//   - event.Type is "content_block_delta".
//
// Returns:
//   - A text StreamChunk and true for text_delta events.
//   - An empty StreamChunk and false for input_json_delta events (accumulated silently).
//
// Side effects:
//   - Appends JSON fragments to the argument buffer for the matching block index.
func (h *streamEventHandler) handleContentBlockDelta(
	event anthropicAPI.MessageStreamEventUnion,
) (provider.StreamChunk, bool) {
	if event.Delta.Type == "text_delta" {
		return provider.StreamChunk{Content: event.Delta.Text}, true
	}
	if event.Delta.Type == "input_json_delta" {
		if buf, ok := h.toolArgsBuf[event.Index]; ok {
			buf.WriteString(event.Delta.PartialJSON)
		}
	}
	return provider.StreamChunk{}, false
}

// handleContentBlockStop finalises a tool call by assembling accumulated arguments.
//
// Expected:
//   - event.Type is "content_block_stop".
//
// Returns:
//   - A tool_call StreamChunk and true if the block was a pending tool call.
//   - An empty StreamChunk and false for non-tool blocks.
//
// Side effects:
//   - Parses accumulated JSON into the ToolCall.Arguments map.
//   - Removes the tool call and buffer from the handler state.
func (h *streamEventHandler) handleContentBlockStop(
	event anthropicAPI.MessageStreamEventUnion,
) (provider.StreamChunk, bool) {
	tc, hasTool := h.pendingToolCalls[event.Index]
	if !hasTool {
		return provider.StreamChunk{}, false
	}
	if buf, ok := h.toolArgsBuf[event.Index]; ok {
		tc.Arguments = parseToolArguments(buf.String())
	}
	delete(h.pendingToolCalls, event.Index)
	delete(h.toolArgsBuf, event.Index)
	return provider.StreamChunk{
		EventType: "tool_call",
		ToolCall:  tc,
	}, true
}

// parseToolArguments deserialises a JSON string into a map of tool arguments.
//
// Expected:
//   - raw is a valid JSON object string, or empty.
//
// Returns:
//   - A map of argument key-value pairs on success.
//   - nil if raw is empty or cannot be parsed.
//
// Side effects:
//   - None.
func parseToolArguments(raw string) map[string]interface{} {
	if raw == "" {
		return nil
	}
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil
	}
	return args
}
