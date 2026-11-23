package zai

// anthropic_tools.go translates structured tools to and from the Anthropic
// Messages wire shape for routes built with WithAnthropicTools: tools carry an
// input_schema, an assistant call is a tool_use block, and tool results go back
// as tool_result blocks of one user message.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/provider"
)

// maxToolInput caps the input bytes accumulated for one streamed tool_use.
const maxToolInput = 1 << 20

// wireToolName is the Anthropic spelling of a neutral tool name: the API only
// accepts ^[a-zA-Z0-9_-]{1,128}$, so the dots of fs.read and exec.run become "_".
func wireToolName(name string) string { return strings.ReplaceAll(name, ".", "_") }

// newToolNames builds the wire to neutral name map of one request from its tool
// definitions and the calls in its history. Two neutral names sharing a wire name
// (a.b and a_b) are refused, so no request is sent for them.
func newToolNames(req *provider.ChatRequest) (*toolNames, error) {
	names := &toolNames{byWire: make(map[string]string)}
	add := func(neutral string) error {
		wire := wireToolName(neutral)
		if prev, ok := names.byWire[wire]; ok && prev != neutral {
			return &ToolProtocolError{Reason: fmt.Sprintf("tool names %q and %q share the wire name %q", prev, neutral, wire)}
		}
		names.byWire[wire] = neutral
		return nil
	}
	for _, d := range req.Tools {
		if err := add(d.Name); err != nil {
			return nil, err
		}
	}
	for _, m := range req.Messages {
		for _, c := range m.ToolCalls {
			if err := add(c.Name); err != nil {
				return nil, err
			}
		}
	}
	return names, nil
}

// neutral maps a wire tool name from a reply back; an unknown name is refused.
func (t *toolNames) neutral(wire string) (string, error) {
	if neutral, ok := t.byWire[wire]; ok {
		return neutral, nil
	}
	return "", &ToolProtocolError{Reason: fmt.Sprintf("reply names unknown tool %q", wire)}
}

// claimToolID records a tool_use id; a repeated id is refused.
func claimToolID(ids map[string]bool, id string) error {
	if ids[id] {
		return &ToolProtocolError{Reason: fmt.Sprintf("duplicate tool_use id %q", id)}
	}
	ids[id] = true
	return nil
}

// anthropicToolsWire maps neutral messages and tool definitions to the wire.
func anthropicToolsWire(msgs []provider.Message, defs []provider.ToolDefinition) ([]anthropicMessage, []anthropicTool) {
	tools := make([]anthropicTool, 0, len(defs))
	for _, d := range defs {
		tools = append(tools, anthropicTool{Name: wireToolName(d.Name), Description: d.Description, InputSchema: d.Parameters})
	}
	out := make([]anthropicMessage, 0, len(msgs))
	resultsOpen := false
	for _, m := range msgs {
		switch {
		case m.Role == "tool":
			block := anthropicBlock{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content}
			if resultsOpen {
				last := &out[len(out)-1]
				last.Content = append(last.Content.([]anthropicBlock), block)
			} else {
				out = append(out, anthropicMessage{Role: "user", Content: []anthropicBlock{block}})
			}
			resultsOpen = true
		case len(m.ToolCalls) > 0:
			out = append(out, anthropicMessage{Role: m.Role, Content: assistantBlocks(m)})
			resultsOpen = false
		default:
			out = append(out, anthropicMessage{Role: m.Role, Content: m.Content})
			resultsOpen = false
		}
	}
	return out, tools
}

// assistantBlocks is the optional text followed by one tool_use block per call.
func assistantBlocks(m provider.Message) []anthropicBlock {
	var blocks []anthropicBlock
	if strings.TrimSpace(m.Content) != "" {
		blocks = append(blocks, anthropicBlock{Type: "text", Text: m.Content})
	}
	for _, c := range m.ToolCalls {
		blocks = append(blocks, anthropicBlock{Type: "tool_use", ID: c.ID, Name: wireToolName(c.Name), Input: c.Arguments})
	}
	return blocks
}

// newToolCall builds and validates a tool call, mapping its wire name back to
// the neutral one; an absent input is an empty object.
func newToolCall(id, wireName string, input json.RawMessage, names *toolNames) (provider.ToolCall, error) {
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	name, err := names.neutral(wireName)
	if err != nil {
		return provider.ToolCall{}, err
	}
	call := provider.ToolCall{ID: id, Name: name, Arguments: input}
	if err := call.Validate(); err != nil {
		return provider.ToolCall{}, &ToolProtocolError{Reason: err.Error()}
	}
	return call, nil
}

// toolEvent consumes the tool_use stream events: handled is true for them. A
// call is emitted complete at its content_block_stop, never as fragments.
func (s *anthropicStream) toolEvent(ctx context.Context, ev anthropicEvent, out chan<- provider.StreamEvent) (bool, error) {
	switch {
	case ev.Type == "content_block_start" && ev.ContentBlock.Type == "tool_use":
		if s.pending[ev.Index] != nil {
			return true, &ToolProtocolError{Reason: fmt.Sprintf("duplicate content block index %d", ev.Index)}
		}
		if err := claimToolID(s.ids, ev.ContentBlock.ID); err != nil {
			return true, err
		}
		s.pending[ev.Index] = &pendingToolUse{call: provider.ToolCall{ID: ev.ContentBlock.ID, Name: ev.ContentBlock.Name}}
		return true, nil
	case ev.Type == "content_block_delta" && ev.Delta.Type == "input_json_delta":
		if pend := s.pending[ev.Index]; pend != nil {
			if len(pend.args)+len(ev.Delta.PartialJSON) > maxToolInput {
				return true, &ToolProtocolError{Reason: fmt.Sprintf("tool input exceeds %d bytes", maxToolInput)}
			}
			pend.args = append(pend.args, ev.Delta.PartialJSON...)
		}
		return true, nil
	case ev.Type == "content_block_stop" && s.pending[ev.Index] != nil:
		pend := s.pending[ev.Index]
		delete(s.pending, ev.Index)
		call, err := newToolCall(pend.call.ID, pend.call.Name, json.RawMessage(pend.args), s.names)
		if err != nil {
			return true, err
		}
		return true, sendAnthropic(ctx, out, provider.StreamEvent{Kind: provider.StreamEventToolCall, ToolCall: &call})
	}
	return false, nil
}

// unfinished is an error when the message ends with a tool_use that never got
// its content_block_stop, unless the model ran out of tokens.
func (s *anthropicStream) unfinished() error {
	if len(s.pending) > 0 && s.stop != "max_tokens" {
		return &ToolProtocolError{Reason: "message ended with an unfinished tool_use"}
	}
	return nil
}
