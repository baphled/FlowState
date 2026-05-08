package anthropic

import (
	"strings"

	anthropicAPI "github.com/anthropics/anthropic-sdk-go"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/shared"
)

// blockKindThinking and blockKindRedactedThinking identify the
// per-content-block tracking states used to decide which payload to
// emit on content_block_stop. The empty string represents "untracked
// or non-thinking" blocks (text, tool_use) which are handled via
// pendingToolCalls / direct passthrough.
const (
	blockKindThinking         = "thinking"
	blockKindRedactedThinking = "redacted_thinking"
)

// streamEventHandler accumulates tool call arguments, thinking content,
// signatures, and redacted thinking payloads across streaming events.
//
// Per-block state is keyed by the upstream content-block index
// (event.Index) because Anthropic's protocol guarantees deltas carry
// the index of the block they belong to. This lets the handler
// correctly attribute signature_delta events to the right thinking
// block when multiple thinking blocks appear in one turn (signed and
// redacted blocks can interleave with text and tool_use).
//
// Expected:
//   - Created via newStreamEventHandler before processing a stream.
//
// Side effects:
//   - None (state is internal to the handler).
type streamEventHandler struct {
	toolArgsBuf      map[int64]*strings.Builder
	pendingToolCalls map[int64]*provider.ToolCall
	// thinkingBufs holds visible thinking content per block index. A
	// signed thinking block accumulates here on thinking_delta and is
	// flushed on content_block_stop.
	thinkingBufs map[int64]*strings.Builder
	// signatureBufs holds the per-block signature accumulated across
	// signature_delta events. Flushed alongside the thinking content
	// on content_block_stop.
	signatureBufs map[int64]*strings.Builder
	// redactedData holds the encrypted payload from a redacted_thinking
	// content_block_start event, keyed by block index. Flushed on
	// content_block_stop.
	redactedData map[int64]string
	// blockKinds tracks which kind of thinking block (signed vs
	// redacted) is open at each index, so content_block_stop knows
	// which payload to emit.
	blockKinds map[int64]string
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
		thinkingBufs:     make(map[int64]*strings.Builder),
		signatureBufs:    make(map[int64]*strings.Builder),
		redactedData:     make(map[int64]string),
		blockKinds:       make(map[int64]string),
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
//   - May mutate internal accumulation buffers for tool call arguments,
//     thinking content, signatures, and redacted thinking payloads.
func (h *streamEventHandler) handleEvent(
	event anthropicAPI.MessageStreamEventUnion,
) (provider.StreamChunk, bool) {
	switch event.Type {
	case "message_start":
		return h.handleMessageStart(event)
	case "content_block_start":
		return h.handleContentBlockStart(event)
	case "content_block_delta":
		return h.handleContentBlockDelta(event)
	case "content_block_stop":
		return h.handleContentBlockStop(event)
	case "message_delta":
		return h.handleMessageDelta(event)
	case "message_stop":
		return provider.StreamChunk{Done: true}, true
	case "ping":
		return provider.StreamChunk{EventType: "ping"}, true
	default:
		return provider.StreamChunk{}, false
	}
}

// handleMessageStart captures usage and identity data from the
// `message_start` event. Cache-stats fields (cache_read_input_tokens,
// cache_creation_input_tokens) are only delivered on this event — NOT
// on `message_delta` — so dropping the event under-reports cache
// activity. The wire-confirmed model from message.model is also
// captured here as evidence of the actual model the API selected.
//
// Expected:
//   - event.Type is "message_start".
//
// Returns:
//   - A StreamChunk with EventType="usage" and a populated Usage
//     pointer when usage data is present.
//   - An empty StreamChunk and false when no usage data is present
//     (zero-value MessageStartEvent).
//
// Side effects:
//   - None.
func (h *streamEventHandler) handleMessageStart(
	event anthropicAPI.MessageStreamEventUnion,
) (provider.StreamChunk, bool) {
	usage := &provider.UsageDelta{
		InputTokens:              event.Message.Usage.InputTokens,
		OutputTokens:             event.Message.Usage.OutputTokens,
		CacheCreationInputTokens: event.Message.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     event.Message.Usage.CacheReadInputTokens,
		RequestID:                event.Message.ID,
		Model:                    event.Message.Model,
	}
	// Suppress empty usage chunks — a totally zero-value message_start
	// (e.g. from a degenerate test fixture) should not synthesise a
	// usage event.
	if isUsageEmpty(usage) {
		return provider.StreamChunk{}, false
	}
	return provider.StreamChunk{EventType: "usage", Usage: usage}, true
}

// handleMessageDelta captures the cumulative token usage and stop
// metadata from the terminal `message_delta` event. Anthropic ships
// stop_reason here (including the Claude 4+ additions "refusal" and
// "model_context_window_exceeded"), the optional matched stop_sequence,
// and a cumulative usage snapshot. Dropping this event makes the
// engine unable to distinguish a refusal from a normal end_turn.
//
// Expected:
//   - event.Type is "message_delta".
//
// Returns:
//   - A StreamChunk with EventType="stop_reason" carrying the parsed
//     stop_reason, optional stop_sequence, and a Usage snapshot.
//   - shouldSend is true unless every captured field is the zero value
//     (which only happens in malformed/test fixtures).
//
// Side effects:
//   - None.
func (h *streamEventHandler) handleMessageDelta(
	event anthropicAPI.MessageStreamEventUnion,
) (provider.StreamChunk, bool) {
	usage := &provider.UsageDelta{
		InputTokens:              event.Usage.InputTokens,
		OutputTokens:             event.Usage.OutputTokens,
		CacheCreationInputTokens: event.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     event.Usage.CacheReadInputTokens,
	}
	chunk := provider.StreamChunk{
		EventType:    "stop_reason",
		StopReason:   string(event.Delta.StopReason),
		StopSequence: event.Delta.StopSequence,
		Usage:        usage,
	}
	if chunk.StopReason == "" && chunk.StopSequence == "" && isUsageEmpty(usage) {
		return provider.StreamChunk{}, false
	}
	if isUsageEmpty(usage) {
		chunk.Usage = nil
	}
	return chunk, true
}

// isUsageEmpty reports whether a UsageDelta has any non-zero/empty
// field. Used to suppress synthesised chunks that would carry no
// information.
func isUsageEmpty(u *provider.UsageDelta) bool {
	if u == nil {
		return true
	}
	return u.InputTokens == 0 &&
		u.OutputTokens == 0 &&
		u.CacheCreationInputTokens == 0 &&
		u.CacheReadInputTokens == 0 &&
		u.RequestID == "" &&
		u.Model == ""
}

// handleContentBlockStart registers a new tool call when a tool_use
// block begins, opens a thinking buffer for a thinking block, or
// captures the encrypted payload of a redacted_thinking block.
//
// Expected:
//   - event.Type is "content_block_start".
//
// Returns:
//   - An empty StreamChunk and false (terminal payloads are emitted on
//     content_block_stop).
//
// Side effects:
//   - Registers tool-call ID/name/argument buffer, thinking buffer,
//     signature buffer, redacted-thinking payload, and the per-block
//     kind tag for the block index.
func (h *streamEventHandler) handleContentBlockStart(
	event anthropicAPI.MessageStreamEventUnion,
) (provider.StreamChunk, bool) {
	switch event.ContentBlock.Type {
	case "tool_use":
		h.pendingToolCalls[event.Index] = &provider.ToolCall{
			ID:   event.ContentBlock.ID,
			Name: event.ContentBlock.Name,
		}
		h.toolArgsBuf[event.Index] = &strings.Builder{}
	case "thinking":
		h.thinkingBufs[event.Index] = &strings.Builder{}
		h.signatureBufs[event.Index] = &strings.Builder{}
		h.blockKinds[event.Index] = blockKindThinking
	case "redacted_thinking":
		// Redacted thinking has no deltas — the encrypted payload
		// arrives in full on content_block_start. Buffer it for emit
		// on content_block_stop so consumers see a single chunk
		// pairing block-start with block-end (mirrors the signed
		// thinking shape).
		h.redactedData[event.Index] = event.ContentBlock.Data
		h.blockKinds[event.Index] = blockKindRedactedThinking
	}
	return provider.StreamChunk{}, false
}

// handleContentBlockDelta processes text, tool argument, thinking, and
// signature fragments.
//
// Expected:
//   - event.Type is "content_block_delta".
//
// Returns:
//   - A text StreamChunk and true for text_delta events.
//   - An empty StreamChunk and false for input_json_delta,
//     thinking_delta, and signature_delta events (accumulated
//     silently).
//
// Side effects:
//   - Appends fragments to the per-block argument buffer
//     (input_json_delta), thinking buffer (thinking_delta), or
//     signature buffer (signature_delta).
func (h *streamEventHandler) handleContentBlockDelta(
	event anthropicAPI.MessageStreamEventUnion,
) (provider.StreamChunk, bool) {
	switch event.Delta.Type {
	case "text_delta":
		return provider.StreamChunk{Content: event.Delta.Text}, true
	case "input_json_delta":
		if buf, ok := h.toolArgsBuf[event.Index]; ok {
			buf.WriteString(event.Delta.PartialJSON)
		}
	case "thinking_delta":
		if buf, ok := h.thinkingBufs[event.Index]; ok {
			buf.WriteString(event.Delta.Thinking)
		}
	case "signature_delta":
		// Signatures are encrypted continuity data tied to thinking
		// blocks. The Anthropic API requires this signature be sent
		// back UNCHANGED on the next turn's assistant message —
		// without it the server silently disables thinking. Append to
		// the buffer for the matching thinking block.
		if buf, ok := h.signatureBufs[event.Index]; ok {
			buf.WriteString(event.Delta.Signature)
		}
	}
	return provider.StreamChunk{}, false
}

// handleContentBlockStop finalises a tool call by assembling
// accumulated arguments, or emits a thinking / redacted_thinking chunk
// with its associated signature / encrypted payload.
//
// Expected:
//   - event.Type is "content_block_stop".
//
// Returns:
//   - A thinking StreamChunk (with Signature) for signed thinking
//     blocks.
//   - A StreamChunk with RedactedThinking populated for
//     redacted_thinking blocks.
//   - A tool_call StreamChunk for pending tool calls.
//   - An empty StreamChunk and false for non-tool, non-thinking blocks
//     (e.g. plain text blocks whose deltas were already forwarded).
//
// Side effects:
//   - Releases per-block state from the handler maps.
func (h *streamEventHandler) handleContentBlockStop(
	event anthropicAPI.MessageStreamEventUnion,
) (provider.StreamChunk, bool) {
	switch h.blockKinds[event.Index] {
	case blockKindThinking:
		thinking := ""
		if buf, ok := h.thinkingBufs[event.Index]; ok {
			thinking = buf.String()
		}
		signature := ""
		if buf, ok := h.signatureBufs[event.Index]; ok {
			signature = buf.String()
		}
		delete(h.thinkingBufs, event.Index)
		delete(h.signatureBufs, event.Index)
		delete(h.blockKinds, event.Index)
		return provider.StreamChunk{
			Thinking:  thinking,
			Signature: signature,
		}, true
	case blockKindRedactedThinking:
		data := h.redactedData[event.Index]
		delete(h.redactedData, event.Index)
		delete(h.blockKinds, event.Index)
		return provider.StreamChunk{
			RedactedThinking: data,
		}, true
	}
	tc, hasTool := h.pendingToolCalls[event.Index]
	if !hasTool {
		return provider.StreamChunk{}, false
	}
	if buf, ok := h.toolArgsBuf[event.Index]; ok {
		tc.Arguments = shared.ParseToolArguments(buf.String())
	}
	delete(h.pendingToolCalls, event.Index)
	delete(h.toolArgsBuf, event.Index)
	return provider.StreamChunk{
		EventType:  "tool_call",
		ToolCall:   tc,
		ToolCallID: tc.ID,
	}, true
}
