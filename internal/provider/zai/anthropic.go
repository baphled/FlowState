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

// TestHookSetAnthropicBaseURL redirects the anthropic coding default
// for httptest-backed specs outside this package.
func TestHookSetAnthropicBaseURL(u string) { codingBaseURL = u }

// codingChatAnthropic performs one non-streaming anthropic-route turn.
func (p *Provider) codingChatAnthropic(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
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
	for _, c := range resp.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	return &provider.ChatResponse{Content: sb.String()}, nil
}

// codingStreamAnthropic streams text deltas from content_block_delta events.
func (p *Provider) codingStreamAnthropic(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	out := make(chan provider.StreamEvent)
	go func() {
		defer close(out)
		body, err := p.codingPost(ctx, req, true)
		if err != nil {
			out <- provider.StreamEvent{Err: err}
			return
		}
		defer body.Close()
		rd := bufio.NewReader(body)
		for {
			line, err := rd.ReadString('\n')
			if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "data:") {
				payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
				var ev anthropicEvent
				if jerr := json.Unmarshal([]byte(payload), &ev); jerr == nil {
					if ev.Type == "content_block_delta" && ev.Delta.Text != "" {
						select {
						case out <- provider.StreamEvent{Content: ev.Delta.Text}:
						case <-ctx.Done():
							return
						}
					}
					if ev.Type == "message_stop" {
						return
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return out
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
	buf, err := json.Marshal(anthropicRequest{Model: model, MaxTokens: ClampMaxTokens(req.MaxTokens), Stream: stream, Messages: req.Messages})
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
