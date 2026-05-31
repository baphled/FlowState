package ollama

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

// TestStreamGuardWiredIntoConstructor proves the stream-guard client is wired
// into Ollama's New(): it mirrors ClientFromEnvironment but with the guard
// client. envconfig.Host() reads OLLAMA_HOST, so pointing that at a no-headers
// black-hole and asserting Stream terminates within the cap proves the guard
// is active (the SDK default http.DefaultClient would hang unbounded).
func TestStreamGuardWiredIntoConstructor(t *testing.T) {
	restore := SetStreamGuardHeaderTimeoutForTest(700 * time.Millisecond)
	defer restore()

	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(8 * time.Second):
		}
	}))
	defer srv.Close()

	// New() resolves the client base from OLLAMA_HOST via envconfig.Host().
	t.Setenv("OLLAMA_HOST", srv.URL)

	p, err := New(srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	done := make(chan struct{})
	go func() {
		ch, serr := p.Stream(context.Background(), provider.ChatRequest{
			Model:    "llama3",
			Messages: []provider.Message{{Role: "user", Content: "hello"}},
		})
		if serr == nil {
			for range ch { //nolint:revive // drain to terminal/close
			}
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not terminate within 3s — stream-guard not wired into ollama New()")
	}
}
