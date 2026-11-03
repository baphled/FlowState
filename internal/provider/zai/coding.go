package zai

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

// Coding-plan delivery routes. Default wire format is the OpenAI-
// compatible endpoint https://api.z.ai/api/coding/paas/v4
// (live-verified 2026-11-03: HTTP 200 serving glm-5.3 to a
// coding-plan key, matching the agent-platform prototype); the
// Anthropic-compatible https://api.z.ai/api/anthropic is selectable
// via WithCodingRoute("anthropic") and is also live-verified.
const (
	codingRouteOpenAI    = "openai"
	codingRouteAnthropic = "anthropic"
)

// codingOpenAIBaseURL is the default coding-plan endpoint.
var codingOpenAIBaseURL = "https://api.z.ai/api/coding/paas/v4"

// TestHookSetCodingBaseURL redirects the openai coding default for
// httptest-backed specs outside this package.
func TestHookSetCodingBaseURL(u string) { codingOpenAIBaseURL = u }

// codingBase resolves the coding-plan host: an explicitly set base
// URL (config host / env) always wins, else the route default.
func (p *Provider) codingBase() string {
	if p.baseURLSet {
		return p.baseURL
	}
	if p.codingRoute == codingRouteAnthropic {
		return codingBaseURL
	}
	return codingOpenAIBaseURL
}

// codingChat / codingStream dispatch on the selected wire format.
func (p *Provider) codingChat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	if p.codingRoute == codingRouteAnthropic {
		return p.codingChatAnthropic(ctx, req)
	}
	body, err := p.codingDo(ctx, req, false)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	var resp response
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("zai: decode coding response: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, errors.New("zai: empty choices in coding response")
	}
	return &provider.ChatResponse{Content: resp.Choices[0].Message.Content}, nil
}

func (p *Provider) codingStream(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	if p.codingRoute == codingRouteAnthropic {
		return p.codingStreamAnthropic(ctx, req)
	}
	return p.codingStreamOpenAI(ctx, req)
}

// codingStreamOpenAI emits content deltas from chat.completion.chunk
// SSE events, ending at [DONE].
func (p *Provider) codingStreamOpenAI(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	out := make(chan provider.StreamEvent)
	go func() {
		defer close(out)
		body, err := p.codingDo(ctx, req, true)
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
				if payload == "[DONE]" {
					return
				}
				var chunk streamChunk
				if json.Unmarshal([]byte(payload), &chunk) == nil {
					for _, c := range chunk.Choices {
						if c.Delta.Content != "" {
							select {
							case out <- provider.StreamEvent{Content: c.Delta.Content}:
							case <-ctx.Done():
								return
							}
						}
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

// codingDo sends one request to the OpenAI-compatible coding
// endpoint with Bearer auth; non-2xx bodies become typed APIErrors.
func (p *Provider) codingDo(ctx context.Context, req *provider.ChatRequest, stream bool) (io.ReadCloser, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}
	buf, err := json.Marshal(request{Model: model, Messages: req.Messages, MaxTokens: ClampMaxTokens(req.MaxTokens), Stream: stream})
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.codingBase()+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	if stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("zai: request: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, codingAPIError(resp)
	}
	return resp.Body, nil
}

// codingAPIError converts a non-2xx coding response into an APIError.
func codingAPIError(resp *http.Response) error {
	defer resp.Body.Close()
	apiErr := &APIError{Status: resp.StatusCode}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if jerr := json.NewDecoder(resp.Body).Decode(&body); jerr == nil && body.Error.Message != "" {
		apiErr.Code = body.Error.Code
		if apiErr.Code == "" {
			apiErr.Code = body.Error.Type
		}
		apiErr.Message = body.Error.Message
	} else {
		apiErr.Message = resp.Status
	}
	return apiErr
}
