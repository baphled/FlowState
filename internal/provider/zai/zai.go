// Package zai implements the Z.ai provider adapter against the
// provider port (M1-PROVIDER). Z.ai exposes an OpenAI-compatible
// chat-completions API; this adapter owns transport/auth only and
// keeps provider-specific logic out of the engine per
// M1-CORE-REQUIREMENTS §3.
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
	"os"
	"strings"

	"github.com/baphled/flowstate/internal/provider"
)

// DefaultBaseURL is the Z.ai OpenAI-compatible endpoint.
const DefaultBaseURL = "https://api.z.ai/api/paas/v4"

// DefaultModel is the current delivery-route model (owner direction
// 2026-11-03: glm-5.3 flagship, glm-4.5-flash free fallback).
const DefaultModel = "glm-5.3"

// ErrUnauthorized marks Z.ai authentication failures so callers can
// branch without string matching; other failures stay generic per
// the no-silent-retry failure path.
var ErrUnauthorized = errors.New("zai: unauthorized")

// New returns a provider bound to an explicit API key.
func New(apiKey string, opts ...Option) *Provider {
	p := &Provider{
		apiKey:  apiKey,
		baseURL: DefaultBaseURL,
		model:   DefaultModel,
		client:  http.DefaultClient,
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// The Anthropic-compatible coding endpoint lives in anthropic.go
// endpoint serving GLM Coding Plan subscriptions.

// WithCoding switches the provider onto the coding-plan (Anthropic-
// compatible) delivery route. Coding-plan keys receive 429 code=1113
// on the OpenAI-compatible route for models like glm-5.3; the
// anthropic route serves them (evidence 2026-11-03).
func WithCoding() Option {
	return func(p *Provider) {
		p.coding = true
		// The coding route must hit the Anthropic-compatible
		// endpoint; switch baseURL unless explicitly overridden
		// (ZAI_BASE_URL / WithBaseURL still wins — those options
		// run later in the chain and set baseURLSet).
		if !p.baseURLSet {
			p.baseURL = codingBaseURL
		}
	}
}

// NewFromEnv builds the provider from ZAI_API_KEY, matching the
// existing provider conventions (per-adapter env var).
func NewFromEnv() (*Provider, error) {
	key := os.Getenv("ZAI_API_KEY")
	if key == "" {
		return nil, errors.New("zai: ZAI_API_KEY is not set")
	}
	opts := []Option{}
	if u := os.Getenv("ZAI_BASE_URL"); u != "" {
		opts = append(opts, WithBaseURL(u))
	}
	if os.Getenv("ZAI_PLAN") == "coding" {
		opts = append(opts, WithCoding())
	}
	return New(key, opts...), nil
}

// WithBaseURL overrides the API base URL.
func WithBaseURL(u string) Option {
	return func(p *Provider) {
		p.baseURL = strings.TrimRight(u, "/")
		p.baseURLSet = true
	}
}

// MaxTokensMax is the largest max_tokens the Z.ai Anthropic-
// compatible endpoint accepts; values outside [1,131072] fail
// with 400 error 1210 (evidence 2026-11-03).
const MaxTokensMax = 131072

// ClampMaxTokens bounds max_tokens into [1, MaxTokensMax] so
// unset (0) or oversized budgets never trigger error 1210.
func ClampMaxTokens(n int64) int64 {
	if n < 1 {
		return 1
	}
	if n > MaxTokensMax {
		return MaxTokensMax
	}
	return n
}

// WithModel overrides the default model.
func WithModel(m string) Option { return func(p *Provider) { p.model = m } }

// WithClient overrides the HTTP client.
func WithClient(c *http.Client) Option { return func(p *Provider) { p.client = c } }

// Name identifies the provider in session records and attribution.
func (p *Provider) Name() string { return "z.ai" }

// Coding reports whether the provider is on the coding-plan route.
func (p *Provider) Coding() bool { return p.coding }

// Models lists models offered by the current delivery route.
func (p *Provider) Models() []string { return []string{DefaultModel, "glm-4.5"} }

// Error implements error for the typed provider failure; see
// types.go for the declaration.
func (e *APIError) Error() string {
	return fmt.Sprintf("zai: api error %d code=%s: %s", e.Status, e.Code, e.Message)
}

// Unwrap maps auth failures onto ErrUnauthorized.
func (e *APIError) Unwrap() error {
	if e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden {
		return ErrUnauthorized
	}
	return nil
}

// Chat performs one non-streaming turn.
func (p *Provider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	if p.coding {
		return p.codingChat(ctx, req)
	}
	body, err := p.post(ctx, req, false)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	var resp response
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("zai: decode response: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, errors.New("zai: empty choices in response")
	}
	return &provider.ChatResponse{Content: resp.Choices[0].Message.Content}, nil
}

// Stream performs one streaming turn, emitting content deltas in
// order. The channel closes after the stream ends or a terminal
// error event is emitted; ctx cancellation stops the stream.
func (p *Provider) Stream(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	if p.coding {
		return p.codingStream(ctx, req)
	}
	out := make(chan provider.StreamEvent)
	go func() {
		defer close(out)
		body, err := p.post(ctx, req, true)
		if err != nil {
			out <- provider.StreamEvent{Err: err}
			return
		}
		defer body.Close()
		rd := bufio.NewReader(body)
		for {
			line, err := rd.ReadString('\n')
			if line != "" {
				if done := p.emitSSELine(ctx, out, line); done {
					return
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					select {
					case out <- provider.StreamEvent{Err: fmt.Errorf("zai: stream read: %w", err)}:
					case <-ctx.Done():
					}
				}
				return
			}
		}
	}()
	return out
}

// emitSSELine parses one SSE line and emits any content delta; it
// reports true when the stream is complete.
func (p *Provider) emitSSELine(ctx context.Context, out chan<- provider.StreamEvent, line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "data:") {
		return false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	if payload == "[DONE]" {
		return true
	}
	var chunk streamChunk
	if jerr := json.Unmarshal([]byte(payload), &chunk); jerr != nil {
		out <- provider.StreamEvent{Err: fmt.Errorf("zai: bad stream chunk: %w", jerr)}
		return true
	}
	for _, c := range chunk.Choices {
		if c.Delta.Content != "" {
			select {
			case out <- provider.StreamEvent{Content: c.Delta.Content}:
			case <-ctx.Done():
				return true
			}
		}
	}
	return false
}

// post sends one request and returns the response body, converting
// non-2xx statuses into a typed APIError.
func (p *Provider) post(ctx context.Context, req *provider.ChatRequest, stream bool) (io.ReadCloser, error) {
	if p.apiKey == "" {
		return nil, errors.New("zai: empty API key")
	}
	model := req.Model
	if model == "" {
		model = p.model
	}
	buf, err := json.Marshal(request{Model: model, Messages: req.Messages, MaxTokens: req.MaxTokens, Stream: stream})
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(buf))
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
		defer resp.Body.Close()
		apiErr := &APIError{Status: resp.StatusCode}
		var body struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if jerr := json.NewDecoder(resp.Body).Decode(&body); jerr == nil && body.Error.Message != "" {
			apiErr.Code = body.Error.Code
			apiErr.Message = body.Error.Message
		} else {
			apiErr.Message = resp.Status
		}
		if apiErr.Message == "" {
			apiErr.Message = resp.Status
		}
		return nil, apiErr
	}
	return resp.Body, nil
}
