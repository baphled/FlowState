package zai

// anthropic.go implements the Z.ai coding-plan delivery route:
// GLM Coding Plan subscriptions are served via the dedicated
// Anthropic-compatible endpoint (https://api.z.ai/api/anthropic),
// NOT the OpenAI-compatible paas/v4 endpoint. Evidence 2026-11-03:
// paas/v4 returns 429 code=1113 ("Insufficient balance or no
// resource package") for glm-4.6/glm-5.x with a coding-plan key,
// while the anthropic endpoint serves glm-4.5/4.5-air/4.6/5.3.

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

// CodingBaseURL is the Z.ai Anthropic-compatible endpoint used by
// GLM Coding Plan subscriptions.
const CodingBaseURL = "https://api.z.ai/api/anthropic"

// anthropicRequest is the Messages API request body.
type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int64              `json:"max_tokens"`
	Stream    bool               `json:"stream,omitempty"`
	Messages  []provider.Message `json:"messages"`
}

// anthropicResponse is a non-streaming Messages API response.
type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// anthropicEvent is one SSE event from a streaming Messages response.
type anthropicEvent struct {
	Type  string `json:"type"`
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
}

// codingChat performs one non-streaming turn via the coding plan route.
func (p *Provider) codingChat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
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

// codingStream performs one streaming turn via the coding plan route,
// emitting text deltas from content_block_delta events.
func (p *Provider) codingStream(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
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

// codingPost sends one request to the Anthropic-compatible endpoint.
func (p *Provider) codingPost(ctx context.Context, req *provider.ChatRequest, stream bool) (io.ReadCloser, error) {
	if p.apiKey == "" {
		return nil, errors.New("zai: empty API key")
	}
	model := req.Model
	if model == "" {
		model = p.model
	}
	buf, err := json.Marshal(anthropicRequest{Model: model, MaxTokens: req.MaxTokens, Stream: stream, Messages: req.Messages})
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/messages", bytes.NewReader(buf))
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
