package openzen

import (
	"context"
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/openai/openai-go/option"

	"github.com/baphled/flowstate/internal/testutils"
)

// TestStreamGuardWiredIntoConstructor proves the stream-guard client is wired
// into OpenZen's constructor. The openai-go SDK passes no per-attempt timeout,
// so without the guard this no-response flap hangs unbounded.
func TestStreamGuardWiredIntoConstructor(t *testing.T) {
	restore := SetStreamGuardHeaderTimeoutForTest(700 * time.Millisecond)
	defer restore()

	srv := testutils.BlackholeServer(8 * time.Second)
	defer srv.Close()

	p, err := NewWithOptions("test-key", option.WithBaseURL(srv.URL), option.WithMaxRetries(0))
	if err != nil {
		t.Fatalf("NewWithOptions: %v", err)
	}

	testutils.AssertStreamTerminatesWithin(t, func() (<-chan provider.StreamChunk, error) {
		return p.Stream(context.Background(), provider.ChatRequest{
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "hello"}},
		})
	}, 3*time.Second)
}
