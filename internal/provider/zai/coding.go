package zai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

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
	if err := p.validateCoding(req); err != nil {
		return nil, err
	}
	if p.codingRoute == codingRouteAnthropic {
		return p.codingChatAnthropic(ctx, req)
	}
	body, err := p.codingDo(ctx, req, false)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	var resp codingResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("zai: decode coding response: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, errors.New("zai: empty choices in coding response")
	}
	if calls := resp.Choices[0].Message.ToolCalls; len(calls) > 0 && string(calls) != "null" && string(calls) != "[]" {
		return nil, &ToolProtocolError{Reason: "nonstream tool responses are unsupported"}
	}
	return &provider.ChatResponse{Content: resp.Choices[0].Message.Content}, nil
}

func (p *Provider) codingStream(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	if err := p.validateCoding(req); err != nil {
		return codingFailure(ctx, err)
	}
	if p.codingRoute == codingRouteAnthropic {
		return p.codingStreamAnthropic(ctx, req)
	}
	return p.codingStreamOpenAI(ctx, req)
}

// codingStreamOpenAI translates coding SSE into complete domain events. Structured
// requests require both a finish reason and [DONE]; cancellation unblocks sends.
func (p *Provider) codingStreamOpenAI(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	out := make(chan provider.StreamEvent)
	go func() {
		defer close(out)
		body, err := p.codingDo(ctx, req, true)
		if err == nil {
			defer body.Close()
			err = codingRead(ctx, body, out, codingStructured(req))
		}
		if err != nil {
			codingSend(ctx, out, provider.StreamEvent{Kind: provider.StreamEventError, Err: err})
		}
	}()
	return out
}

// codingDo sends one request to the OpenAI-compatible coding
// endpoint with Bearer auth; non-2xx bodies become typed APIErrors.
func (p *Provider) codingDo(ctx context.Context, req *provider.ChatRequest, stream bool) (io.ReadCloser, error) {
	if err := p.validateCoding(req); err != nil {
		return nil, err
	}
	model := req.Model
	if model == "" {
		model = p.model
	}
	buf, err := codingEncode(req, model, stream)
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
