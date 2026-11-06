package zai

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/baphled/flowstate/internal/provider"
)

func (state *codingAssembly) fragment(delta codingDelta) error {
	if delta.Index == nil || *delta.Index < 0 || *delta.Index > len(state.calls) {
		return &ToolProtocolError{Reason: "unknown tool index"}
	}
	index := *delta.Index
	if index == len(state.calls) {
		if delta.ID == "" || delta.Type != "function" {
			return &ToolProtocolError{Reason: "missing initial tool metadata"}
		}
		for _, call := range state.calls {
			if call.ID == delta.ID {
				return &ToolProtocolError{Reason: "duplicate tool ID"}
			}
		}
		state.calls = append(state.calls, codingCall{ID: delta.ID, Type: delta.Type})
	}
	call := &state.calls[index]
	if (delta.ID != "" && delta.ID != call.ID) || (delta.Type != "" && delta.Type != call.Type) {
		return &ToolProtocolError{Reason: "conflicting tool metadata"}
	}
	call.Function.Name += delta.Function.Name
	call.Function.Arguments += delta.Function.Arguments
	return nil
}
func (state *codingAssembly) chunk(ctx context.Context, out chan<- provider.StreamEvent, payload string) error {
	var chunk codingChunk
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return &ToolProtocolError{Reason: "malformed stream JSON"}
	}
	for _, choice := range chunk.Choices {
		if choice.Index != 0 || state.finish != "" {
			return &ToolProtocolError{Reason: "unexpected choice or delta after finish"}
		}
		if len(choice.Delta.ToolCalls) > 0 && !state.strict {
			return &ToolProtocolError{Reason: "unsolicited tool calls"}
		}
		for _, delta := range choice.Delta.ToolCalls {
			if err := state.fragment(delta); err != nil {
				return err
			}
		}
		if choice.Delta.Content != "" {
			codingSend(ctx, out, provider.StreamEvent{Kind: provider.StreamEventText, Content: choice.Delta.Content})
		}
		state.finish = choice.FinishReason
	}
	return nil
}
func (state *codingAssembly) complete(ctx context.Context, out chan<- provider.StreamEvent) error {
	if !state.strict {
		return nil
	}
	if state.finish == "" {
		return &ToolProtocolError{Reason: "missing finish reason"}
	}
	if (len(state.calls) > 0) != (state.finish == "tool_calls") {
		return &ToolProtocolError{Reason: "finish reason contradicts tool calls"}
	}
	calls := make([]provider.ToolCall, 0, len(state.calls))
	for _, wire := range state.calls {
		call := provider.ToolCall{ID: wire.ID, Name: wire.Function.Name, Arguments: json.RawMessage(wire.Function.Arguments)}
		if err := call.Validate(); err != nil {
			return &ToolProtocolError{Reason: err.Error()}
		}
		calls = append(calls, call)
	}
	for _, call := range calls {
		if !codingSend(ctx, out, provider.StreamEvent{Kind: provider.StreamEventToolCall, ToolCall: &call}) {
			return ctx.Err()
		}
	}
	codingSend(ctx, out, provider.StreamEvent{Kind: provider.StreamEventFinish, FinishReason: state.finish})
	return nil
}
func codingRead(ctx context.Context, body io.Reader, out chan<- provider.StreamEvent, strict bool) error {
	state := codingAssembly{strict: strict}
	rd := bufio.NewReader(body)
	for {
		line, err := rd.ReadString('\n')
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
			if payload == "[DONE]" {
				return state.complete(ctx, out)
			}
			if parseErr := state.chunk(ctx, out, payload); parseErr != nil {
				return parseErr
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if strict {
				return &ToolProtocolError{Reason: "stream ended before [DONE]"}
			}
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}
