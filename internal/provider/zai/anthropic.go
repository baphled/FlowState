package zai

// anthropic.go implements the selectable Anthropic-compatible
// coding-plan route (https://api.z.ai/api/anthropic), chosen via
// WithCodingRoute("anthropic") / providers.zai.route=anthropic /
// ZAI_CODING_ROUTE=anthropic. The default coding route is the
// OpenAI-compatible endpoint in coding.go; both surfaces are
// live-verified for coding-plan keys (evidence 2026-11-03).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/baphled/flowstate/internal/provider"
)

// codingBaseURL is the Anthropic-compatible endpoint used by the
// selectable anthropic coding route; overridable in tests.
var codingBaseURL = "https://api.z.ai/api/anthropic"

var (
	// ErrStreamEnded marks an anthropic-route stream that ended (EOF, or an
	// empty or non-event-stream body) before message_stop.
	ErrStreamEnded = errors.New("zai: stream ended early, before message_stop")
	// ErrSystemOnly marks a WithAnthropicSystem request that holds system
	// messages and nothing else; it is refused before any request is sent.
	ErrSystemOnly = errors.New("zai: request has only system messages; the Messages API needs a user message")
)

// TestHookSetAnthropicBaseURL redirects the anthropic coding default
// for httptest-backed specs outside this package.
func TestHookSetAnthropicBaseURL(u string) { codingBaseURL = u }

// codingChatAnthropic performs one non-streaming anthropic-route turn.
func (p *Provider) codingChatAnthropic(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	names, err := p.requestToolNames(req)
	if err != nil {
		return nil, err
	}
	body, err := p.codingPost(ctx, req, false)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	var resp anthropicResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("zai: decode coding response: %w", err)
	}
	var sb strings.Builder
	out := &provider.ChatResponse{}
	ids := make(map[string]bool)
	for _, c := range resp.Content {
		switch {
		case c.Type == "text":
			sb.WriteString(c.Text)
		case c.Type == "tool_use" && p.anthropicTools:
			if err := claimToolID(ids, c.ID); err != nil {
				return nil, err
			}
			call, err := newToolCall(c.ID, c.Name, c.Input, names)
			if err != nil {
				return nil, err
			}
			out.ToolCalls = append(out.ToolCalls, call)
		}
	}
	out.Content = sb.String()
	var acc usageAccumulator
	if err := acc.merge(resp.Usage); err != nil {
		return nil, err
	}
	out.Usage = acc.result()
	return out, nil
}

// codingStreamAnthropic streams text deltas from content_block_delta events.
// A stream that fails or ends before message_stop ends with one error event.
func (p *Provider) codingStreamAnthropic(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	out := make(chan provider.StreamEvent)
	go func() {
		defer close(out)
		names, err := p.requestToolNames(req)
		if err != nil {
			out <- provider.StreamEvent{Kind: provider.StreamEventError, Err: err}
			return
		}
		body, err := p.codingPost(ctx, req, true)
		if err != nil {
			out <- provider.StreamEvent{Err: err}
			return
		}
		defer body.Close()
		if err := p.readAnthropicStream(ctx, body, names, out); err != nil && ctx.Err() == nil {
			out <- provider.StreamEvent{Kind: provider.StreamEventError, Err: err}
		}
	}()
	return out
}

// readAnthropicStream returns nil at message_stop, else the first failure.
func (p *Provider) readAnthropicStream(ctx context.Context, body io.Reader, names *toolNames, out chan<- provider.StreamEvent) error {
	rd := bufio.NewReader(body)
	st := &anthropicStream{pending: make(map[int]*pendingToolUse), ids: make(map[string]bool), names: names}
	for {
		line, rerr := rd.ReadString('\n')
		if ev, ok := parseAnthropicEvent(line); ok {
			done, err := p.handleAnthropicEvent(ctx, ev, st, out)
			if err != nil || done {
				return err
			}
		}
		if rerr == io.EOF {
			return ErrStreamEnded
		}
		if rerr != nil {
			return fmt.Errorf("zai: read stream: %w", rerr)
		}
	}
}

// parseAnthropicEvent decodes one SSE data line; ok is false for any other line.
func parseAnthropicEvent(line string) (anthropicEvent, bool) {
	trimmed := strings.TrimSpace(line)
	var ev anthropicEvent
	if !strings.HasPrefix(trimmed, "data:") {
		return ev, false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	return ev, json.Unmarshal([]byte(payload), &ev) == nil
}

// handleAnthropicEvent forwards text, remembers the stop reason, fails on an
// error event and finishes on message_stop (done is true).
func (p *Provider) handleAnthropicEvent(ctx context.Context, ev anthropicEvent, st *anthropicStream, out chan<- provider.StreamEvent) (bool, error) {
	if p.anthropicTools {
		if handled, err := st.toolEvent(ctx, ev, out); handled {
			return false, err
		}
	}
	switch ev.Type {
	case "content_block_delta":
		if ev.Delta.Text == "" {
			return false, nil
		}
		return false, sendAnthropic(ctx, out, provider.StreamEvent{Content: ev.Delta.Text})
	case "message_start":
		return false, st.usage.merge(startUsage(ev.Message))
	case "message_delta":
		if ev.Delta.StopReason != "" {
			st.stop = ev.Delta.StopReason
		}
		return false, st.usage.merge(ev.Usage)
	case "error":
		return false, fmt.Errorf("zai: stream error %s: %s", ev.Error.Type, ev.Error.Message)
	case "message_stop":
		if err := st.unfinished(); err != nil {
			return true, err
		}
		if !p.anthropicFinish {
			return true, nil
		}
		fin := provider.StreamEvent{Kind: provider.StreamEventFinish, FinishReason: finishReason(st.stop), Usage: st.usage.result()}
		return true, sendAnthropic(ctx, out, fin)
	}
	return false, nil
}

// sendAnthropic delivers ev unless ctx ends first.
func sendAnthropic(ctx context.Context, out chan<- provider.StreamEvent, ev provider.StreamEvent) error {
	select {
	case out <- ev:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// finishReason maps a stop_reason: end_turn, stop_sequence and absent are
// "stop", max_tokens is "length", anything else passes through.
func finishReason(stopReason string) string {
	switch stopReason {
	case "", "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return provider.FinishLength
	case "tool_use":
		return provider.FinishToolCalls
	}
	return stopReason
}

// requestToolNames is the wire name map of req, or nil when the route sends no tools.
func (p *Provider) requestToolNames(req *provider.ChatRequest) (*toolNames, error) {
	if !p.anthropicTools {
		return nil, nil
	}
	return newToolNames(req)
}

// codingPost sends one request to the Anthropic-compatible endpoint; the host honours codingBase() so explicit config hosts still win.
func (p *Provider) codingPost(ctx context.Context, req *provider.ChatRequest, stream bool) (io.ReadCloser, error) {
	if p.apiKey == "" {
		return nil, errors.New("zai: empty API key")
	}
	model := req.Model
	if model == "" {
		model = p.model
	}
	wire := anthropicRequest{Model: model, MaxTokens: ClampMaxTokens(req.MaxTokens), Stream: stream}
	msgs := req.Messages
	if p.systemTopLevel {
		wire.System, msgs = splitSystem(req.Messages)
		if wire.System != "" && len(msgs) == 0 {
			return nil, ErrSystemOnly
		}
	}
	wire.Messages = msgs
	if p.anthropicTools {
		wire.Messages, wire.Tools = anthropicToolsWire(msgs, req.Tools)
	}
	buf, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.codingBase()+"/v1/messages", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("Content-Type", "application/json")
	if stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("zai: request: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		apiErr := &APIError{Status: resp.StatusCode}
		var body struct {
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if jerr := json.NewDecoder(resp.Body).Decode(&body); jerr == nil && body.Error.Message != "" {
			apiErr.Code = body.Error.Type
			apiErr.Message = body.Error.Message
		} else {
			apiErr.Message = resp.Status
		}
		return nil, apiErr
	}
	return resp.Body, nil
}

// splitSystem separates system-role messages (joined by a blank line) from
// the conversation, preserving the order of the remaining messages.
func splitSystem(msgs []provider.Message) (string, []provider.Message) {
	var system []string
	rest := make([]provider.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "system" {
			system = append(system, m.Content)
			continue
		}
		rest = append(rest, m)
	}
	return strings.Join(system, "\n\n"), rest
}

// startUsage is the usage object of a message_start message; a message that
// is not an object (or has no usage) reports no usage.
func startUsage(message json.RawMessage) json.RawMessage {
	var m struct {
		Usage json.RawMessage `json:"usage"`
	}
	if json.Unmarshal(message, &m) != nil {
		return nil
	}
	return m.Usage
}
