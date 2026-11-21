package anthropic

// apikey.go implements the API-key route (providers.anthropic.auth_type:
// api_key). It is a thin adapter over the Anthropic Messages wire client that
// already lives in internal/provider/zai (anthropic coding route): that client
// sends x-api-key, anthropic-version and content headers only. This file adds
// what the Anthropic API needs on top: top-level system prompt, a finish event,
// default model, a finish event carrying the stop reason, redacted
// anthropic-named errors, and a client that never follows redirects.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/zai"
)

// NewAPIKey returns the API-key route for key. An empty baseURL selects
// DefaultAPIBaseURL. A nil client selects a client that does not follow
// redirects, so a 3xx is an API error and the key is never re-sent to another
// host; a client you pass keeps its own redirect policy.
func NewAPIKey(key, baseURL string, client *http.Client) *APIKey {
	if baseURL == "" {
		baseURL = DefaultAPIBaseURL
	}
	if client == nil {
		client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	}
	return &APIKey{key: key, client: zai.New(key,
		zai.WithCoding(), zai.WithCodingRoute("anthropic"), zai.WithBaseURL(baseURL),
		zai.WithModel(APIKeyModel), zai.WithAnthropicSystem(), zai.WithAnthropicFinish(),
		zai.WithClient(client))}
}

// Name identifies the provider in session records.
func (a *APIKey) Name() string { return "anthropic" }

// Models lists current Anthropic model ids. It is advisory: the request model
// is passed through unchanged and never validated against this list.
func (a *APIKey) Models() []string {
	return []string{"claude-sonnet-5-5", "claude-haiku-5-5", "claude-opus-5-5"}
}

// Error reports the status, type and Anthropic's message.
func (e *APIError) Error() string {
	return fmt.Sprintf("anthropic: api error %d %s: %s", e.Status, e.Type, e.Message)
}

// Unwrap exposes the underlying cause, for example zai.ErrUnauthorized on a
// 401, so errors.Is keeps working; the cause is never printed.
func (e *APIError) Unwrap() error { return e.cause }

// Error returns the redacted message.
func (e *routeError) Error() string { return e.msg }

// Unwrap exposes the sentinel cause, for example context.Canceled.
func (e *routeError) Unwrap() error { return e.cause }

// Chat performs one non-streaming turn.
func (a *APIKey) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	if err := refuseTools(req); err != nil {
		return nil, err
	}
	resp, err := a.client.Chat(ctx, req)
	if err != nil {
		return nil, a.wrapErr(err)
	}
	return resp, nil
}

// Stream performs one streaming turn: content events, then one finish event
// carrying the mapped stop reason when the stream ends cleanly, or one terminal
// error event. A truncated stream never yields a finish event.
func (a *APIKey) Stream(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	out := make(chan provider.StreamEvent)
	go func() {
		defer close(out)
		send := func(ev provider.StreamEvent) bool {
			select {
			case out <- ev:
				return true
			case <-ctx.Done():
				return false
			}
		}
		if err := refuseTools(req); err != nil {
			send(provider.StreamEvent{Kind: provider.StreamEventError, Err: err})
			return
		}
		for ev := range a.client.Stream(ctx, req) {
			if ev.Err != nil {
				send(provider.StreamEvent{Kind: provider.StreamEventError, Err: a.wrapErr(ev.Err)})
				return
			}
			if ev.Kind == provider.StreamEventFinish {
				send(ev)
				return
			}
			if ev.Content != "" && !send(provider.StreamEvent{Kind: provider.StreamEventText, Content: ev.Content}) {
				return
			}
		}
	}()
	return out
}

// refuseTools returns ErrUnsupportedTools, as the OAuth route does.
func refuseTools(req *provider.ChatRequest) error {
	if req == nil {
		return nil
	}
	return rejectTools(req)
}

// wrapErr renames client errors to the anthropic provider and redacts the key.
func (a *APIKey) wrapErr(err error) error {
	var apiErr *zai.APIError
	if errors.As(err, &apiErr) {
		return &APIError{
			Status: apiErr.Status, Type: a.redact(apiErr.Code), Message: a.redact(apiErr.Message),
			cause: errors.Unwrap(apiErr),
		}
	}
	msg := "anthropic: " + a.redact(strings.TrimPrefix(err.Error(), "zai: "))
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, zai.ErrStreamEnded} {
		if errors.Is(err, cause) {
			return &routeError{msg: msg, cause: cause}
		}
	}
	return &routeError{msg: msg}
}

// redact removes the API key from s.
func (a *APIKey) redact(s string) string {
	if a.key == "" {
		return s
	}
	return strings.ReplaceAll(s, a.key, "[redacted]")
}
