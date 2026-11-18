package zai

import (
	"context"
	"encoding/json"

	"github.com/baphled/flowstate/internal/provider"
)

// Error implements error with the structured transport failure reason.
func (e *ToolProtocolError) Error() string { return "zai: tool protocol: " + e.Reason }

func codingStructured(req *provider.ChatRequest) bool {
	if len(req.Tools) > 0 {
		return true
	}
	for _, m := range req.Messages {
		if len(m.ToolCalls) > 0 || m.ToolCallID != "" || m.Role == "tool" {
			return true
		}
	}
	return false
}
func (p *Provider) validateCoding(req *provider.ChatRequest) error {
	if req == nil {
		return &ToolProtocolError{Reason: "nil request"}
	}
	if err := req.Validate(); err != nil {
		return &ToolProtocolError{Reason: err.Error()}
	}
	if p.codingRoute == codingRouteAnthropic && codingStructured(req) {
		return &ToolProtocolError{Reason: "Anthropic coding route does not support structured tools"}
	}
	return nil
}
func codingEncode(req *provider.ChatRequest, model string, stream bool) ([]byte, error) {
	wire := codingRequest{Model: model, MaxTokens: ClampMaxTokens(req.MaxTokens), Stream: stream, Messages: make([]codingMessage, 0, len(req.Messages))}
	for _, t := range req.Tools {
		wire.Tools = append(wire.Tools, codingTool{Type: "function", Function: t})
	}
	for _, m := range req.Messages {
		msg := codingMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		for _, call := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, codingCall{ID: call.ID, Type: "function", Function: codingFunction{Name: call.Name, Arguments: string(call.Arguments)}})
		}
		wire.Messages = append(wire.Messages, msg)
	}
	return json.Marshal(wire)
}
func codingSend(ctx context.Context, out chan<- provider.StreamEvent, ev provider.StreamEvent) bool {
	select {
	case <-ctx.Done():
		return false
	default:
	}
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}
func codingFailure(ctx context.Context, err error) <-chan provider.StreamEvent {
	out := make(chan provider.StreamEvent)
	go func() {
		defer close(out)
		codingSend(ctx, out, provider.StreamEvent{Kind: provider.StreamEventError, Err: err})
	}()
	return out
}
