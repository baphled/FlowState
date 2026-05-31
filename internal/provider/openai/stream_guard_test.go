package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/openai/openai-go/option"
)

// blackholeServer accepts the request but never writes response headers — the
// provider-flap signature (connection accepted, no first byte). Self-returns
// after a hard cap so httptest.Server.Close drains cleanly.
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
// into the production constructor. The openai-go SDK passes NO per-attempt
// timeout, so WITHOUT the guard this flap hangs unbounded; termination within
// the cap proves the constructor attached the guard.
func TestStreamGuardWiredIntoConstructor(t *testing.T) {
	restore := SetStreamGuardHeaderTimeoutForTest(700 * time.Millisecond)
	defer restore()

	srv := blackholeServer()
	defer srv.Close()

	p, err := NewWithOptions(
		"test-key",
		option.WithBaseURL(srv.URL),
		option.WithMaxRetries(0),
	)
	if err != nil {
		t.Fatalf("NewWithOptions: %v", err)
	}

	assertStreamTerminatesWithin(t, func() (<-chan provider.StreamChunk, error) {
		return p.Stream(context.Background(), provider.ChatRequest{
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "hello"}},
		})
	}, 3*time.Second)
}
