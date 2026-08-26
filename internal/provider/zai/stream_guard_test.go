package zai

import (
	"context"
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/openai/openai-go/option"

	"github.com/baphled/flowstate/internal/testutils"
)

// TestStreamGuardWiredIntoConstructor proves the stream-guard client is wired
// into the production constructor. Z.AI is served through the openai-go SDK
// (no per-attempt timeout), so WITHOUT the guard this flap hangs unbounded;
// termination within the cap proves the constructor attached the guard. The
// constructor passes WithBaseURL(defaultBaseURL); the test's WithBaseURL(srv)
// comes later in the opts slice and overrides it.
func TestStreamGuardWiredIntoConstructor(t *testing.T) {
	restore := SetStreamGuardHeaderTimeoutForTest(700 * time.Millisecond)
	defer restore()

	srv := testutils.BlackholeServer(8 * time.Second)
	defer srv.Close()

	p, err := NewWithOptions(
		"test-key",
		option.WithBaseURL(srv.URL),
		option.WithMaxRetries(0),
	)
	if err != nil {
		t.Fatalf("NewWithOptions: %v", err)
	}

	testutils.AssertStreamTerminatesWithin(t, func() (<-chan provider.StreamChunk, error) {
		return p.Stream(context.Background(), provider.ChatRequest{
			Model:    "glm-4.6",
			Messages: []provider.Message{{Role: "user", Content: "hello"}},
		})
	}, 3*time.Second)
}
