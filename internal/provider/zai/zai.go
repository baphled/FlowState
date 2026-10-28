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
// 2026-10-22; glm-4.6 per M1 evidence).
const DefaultModel = "glm-4.6"

// Sentinel error classes so callers can branch without string
// matching. Only ErrUnauthorized is distinguished; other failures
// stay generic per the no-silent-retry failure path.
var ErrUnauthorized = errors.New("zai: unauthorized")

// APIError is a typed provider failure with the observed HTTP status
// and the provider's own error code/message. It surfaces to callers
// as a typed error event; the adapter never retries (M1-BUILD-SPEC
// M1-PROVIDER failure path).
type APIError struct {
	Status  int
	Code    string
	Message string
}

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

// Provider is the Z.ai adapter. It implements provider.Provider and
// adds Stream for streamed turns.
type Provider struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
}

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

// NewFromEnv builds the provider from ZAI_API_KEY, matching the
// existing provider conventions (per-adapter env var).
func NewFromEnv() (*Provider, error) {
	key := os.Getenv("ZAI_API_KEY")
	if key == "" {
		return nil, errors.New("zai: ZAI_API_KEY is not set")
	}
	return New(key), nil
}

// Option customises the adapter (tests inject a fake base URL).
type Option func(*Provider)

// WithBaseURL overrides the API base URL.
func WithBaseURL(u string) Option { return func(p *Provider) { p.baseURL = strings.TrimRight(u, "/") } }

// WithModel overrides the default model.
func WithModel(m string) Option { return func(p *Provider) { p.model = m } }

// WithClient overrides the HTTP client.
func WithClient(c *http.Client) Option { return func(p *Provider) { p.client = c } }

// Name identifies the provider in session records and attribution.
func (p *Provider) Name() string { return "z.ai" }

// Models lists models offered by the current delivery route.
func (p *Provider) Models() []string { return []string{DefaultModel, "glm-4.5"} }

// request is the OpenAI-compatible request body.
type request struct {
	Model    string             `json:"model"`
	Messages []provider.Message `json:"messages"`
	Stream   bool               `json:"stream,omitempty"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// response is the OpenAI-compatible (non-streaming) response shape.
type response struct {
	Choices []struct {
		Message message `json:"message"`
	} `json:"choices"`
}

// Chat performs one non-streaming turn.
func (p *Provider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
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

// streamChunk is one SSE `data:` payload of the streaming response.
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

// Stream performs one streaming turn, emitting content deltas in
// order. The channel closes after the stream ends or a terminal
// error event is emitted; ctx cancellation stops the stream.
func (p *Provider) Stream(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
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
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "data:") {
					payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
					if payload == "[DONE]" {
						return
					}
					var chunk streamChunk
					if jerr := json.Unmarshal([]byte(payload), &chunk); jerr != nil {
						out <- provider.StreamEvent{Err: fmt.Errorf("zai: bad stream chunk: %w", jerr)}
						return
					}
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
	buf, err := json.Marshal(request{Model: model, Messages: req.Messages, Stream: stream})
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
