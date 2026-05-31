package copilot_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/copilot"
)

// TestStreamGuardWiredIntoBuildClient proves the stream-guard client is wired
// into Copilot's buildClient choke point. A direct token avoids any exchange
// network call, so Stream goes straight to buildClient → RunStream against the
// black-hole. Copilot's New() gives no way to disable the openai-go SDK's
// default retries, so the guarded first-byte window (300ms) is multiplied by
// ~3 retry attempts plus backoff — measured ~2.1s. The 5s cap clears that with
// margin while staying below the 6s black-hole self-return: a MISSING guard
// would instead hang each attempt to the 6s self-return (~18s across retries),
// blowing the cap. That ordering is what makes the regression valid.
func TestStreamGuardWiredIntoBuildClient(t *testing.T) {
	restore := copilot.SetStreamGuardHeaderTimeoutForTest(300 * time.Millisecond)
	defer restore()

	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(6 * time.Second):
		}
	}))
	defer srv.Close()

	p, err := copilot.New("direct-token")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p.SetBaseURLForTest(srv.URL)

	done := make(chan struct{})
	go func() {
		ch, serr := p.Stream(context.Background(), provider.ChatRequest{
			Model:    "gpt-4o",
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
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not terminate within 5s — stream-guard not wired into buildClient")
	}
}
