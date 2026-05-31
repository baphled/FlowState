package anthropic

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/baphled/flowstate/internal/provider"
)

// blackholeServer accepts the request but never writes response headers,
// self-returning after a hard cap so httptest.Server.Close drains cleanly.
// This is the provider-flap signature reproduced live: the connection is
// accepted, but no first byte ever arrives.
func blackholeServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(8 * time.Second):
		}
	}))
}

func assertStreamTerminatesWithin(t *testing.T, start func() (<-chan provider.StreamChunk, error), within time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		ch, err := start()
		if err == nil {
			for range ch { //nolint:revive // drain to terminal/close
			}
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(within):
		t.Fatalf("stream did not terminate within %s — stream-guard not bounding the no-headers window", within)
	}
}

// TestStreamGuardWiredIntoConstructor proves the stream-guard client is wired
// into the production constructor: a default-built provider (NO explicit
// WithHTTPClient) bounds a no-response flap at the guard's header timeout.
// Anthropic would otherwise only stop at the 10-minute wall-clock, so
// termination within a sub-second guard window proves the guard is active.
func TestStreamGuardWiredIntoConstructor(t *testing.T) {
	restore := SetStreamGuardHeaderTimeoutForTest(700 * time.Millisecond)
	defer restore()

	srv := blackholeServer()
	defer srv.Close()

	p, err := NewWithOptions(
		"sk-ant-test-key",
		option.WithBaseURL(srv.URL),
		option.WithMaxRetries(0),
		// Deliberately NOT passing WithHTTPClient — the constructor's
		// guard client must be the one in effect. Wall-clock left at its
		// 10-minute default so only the guard can fire within the cap.
	)
	if err != nil {
		t.Fatalf("NewWithOptions: %v", err)
	}

	assertStreamTerminatesWithin(t, func() (<-chan provider.StreamChunk, error) {
		return p.Stream(context.Background(), provider.ChatRequest{
			Model:    "claude-3-5-sonnet-20241022",
			Messages: []provider.Message{{Role: "user", Content: "hello"}},
		})
	}, 3*time.Second)
}

// TestStreamGuardTimeoutClassifiedAsRetriableNetworkError pins that a
// header-timeout (the stream-guard firing on a dead/flapping provider) surfaces
// as a retriable *provider.Error{NetworkError}, NOT a raw *url.Error. That
// classification is what lets the failover HealthManager apply a cooldown so a
// dead Anthropic provider is SKIPPED on subsequent turns instead of re-paying
// the full ResponseHeaderTimeout every turn. Without it, in-turn failover still
// advances but the provider is never health-marked (the cross-turn gap). This
// mirrors openaicompat.ParseProviderError's *url.Error -> NetworkError branch.
func TestStreamGuardTimeoutClassifiedAsRetriableNetworkError(t *testing.T) {
	restore := SetStreamGuardHeaderTimeoutForTest(700 * time.Millisecond)
	defer restore()

	srv := blackholeServer()
	defer srv.Close()

	p, err := NewWithOptions("sk-ant-test-key", option.WithBaseURL(srv.URL), option.WithMaxRetries(0))
	if err != nil {
		t.Fatalf("NewWithOptions: %v", err)
	}

	ch, err := p.Stream(context.Background(), provider.ChatRequest{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []provider.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var streamErr error
	for c := range ch {
		if c.Error != nil {
			streamErr = c.Error
		}
	}
	if streamErr == nil {
		t.Fatal("expected a terminal error from the dead provider, got none")
	}

	var provErr *provider.Error
	if !errors.As(streamErr, &provErr) {
		t.Fatalf("header-timeout surfaced as %T (%v); want *provider.Error so the failover HealthManager can cool the provider down", streamErr, streamErr)
	}
	if provErr.ErrorType != provider.ErrorTypeNetworkError || !provErr.IsRetriable {
		t.Fatalf("got ErrorType=%v IsRetriable=%v; want network_error + retriable", provErr.ErrorType, provErr.IsRetriable)
	}
}

// TestChatTransportErrorClassifiedAsRetriableNetworkError pins that the
// non-stream Chat path classifies a transport/header-timeout error the same way
// the streaming path does — a retriable *provider.Error{NetworkError} — so any
// failover/health consumer of Chat sees the same typed signal (symmetry with
// parseAnthropicStreamError).
func TestChatTransportErrorClassifiedAsRetriableNetworkError(t *testing.T) {
	restore := SetStreamGuardHeaderTimeoutForTest(700 * time.Millisecond)
	defer restore()

	srv := blackholeServer()
	defer srv.Close()

	p, err := NewWithOptions("sk-ant-test-key", option.WithBaseURL(srv.URL), option.WithMaxRetries(0))
	if err != nil {
		t.Fatalf("NewWithOptions: %v", err)
	}

	_, chatErr := p.Chat(context.Background(), provider.ChatRequest{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []provider.Message{{Role: "user", Content: "hi"}},
	})
	if chatErr == nil {
		t.Fatal("expected an error from the dead provider, got nil")
	}
	var provErr *provider.Error
	if !errors.As(chatErr, &provErr) {
		t.Fatalf("Chat header-timeout surfaced as %T (%v); want *provider.Error", chatErr, chatErr)
	}
	if provErr.ErrorType != provider.ErrorTypeNetworkError || !provErr.IsRetriable {
		t.Fatalf("got ErrorType=%v IsRetriable=%v; want network_error + retriable", provErr.ErrorType, provErr.IsRetriable)
	}
}
