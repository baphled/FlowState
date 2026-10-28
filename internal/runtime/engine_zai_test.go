package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/baphled/flowstate/internal/provider/registry"
	"github.com/baphled/flowstate/internal/provider/zai"
	"github.com/baphled/flowstate/internal/session"
)

func newSSEServer(t *testing.T, chunks ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, c := range chunks {
			_, _ = w.Write([]byte("data: " + c + "\n\n"))
			fl.Flush()
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		fl.Flush()
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGivenZaiOverHTTPWhenTurnThenPersistedEvents(t *testing.T) {
	srv := newSSEServer(t,
		`{"choices":[{"delta":{"content":"hel"}}]}`,
		`{"choices":[{"delta":{"content":"lo"}}]}`,
	)
	p := zai.New("test-key", zai.WithBaseURL(srv.URL))
	st := newStore(t)
	ctx := context.Background()
	sess, err := session.Create(ctx, st, "z.ai", "glm-4.6", session.DefaultHarness, "sha", "greet me")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	res, err := Turn(ctx, st, sess, p)
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if res.FinalContent != "hello" {
		t.Fatalf("FinalContent = %q, want hello", res.FinalContent)
	}
	evts, err := st.Replay(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	want := []string{"turn.start", "user.message", "assistant.delta", "assistant.delta", "assistant.message", "turn.end"}
	if len(evts) != len(want) {
		t.Fatalf("event types = %v, want %v", types(evts), want)
	}
	for i, e := range evts {
		if e.Type != want[i] {
			t.Fatalf("event types = %v, want %v", types(evts), want)
		}
	}
	after, _ := st.GetSession(ctx, sess.ID)
	if after.Status != "completed" {
		t.Errorf("status = %q, want completed", after.Status)
	}
}

func TestGivenZaiHTTPErrorWhenTurnThenProviderErrorEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"503","message":"upstream down"}}`))
	}))
	t.Cleanup(srv.Close)
	p := zai.New("test-key", zai.WithBaseURL(srv.URL))
	st := newStore(t)
	ctx := context.Background()
	sess, err := session.Create(ctx, st, "z.ai", "glm-4.6", session.DefaultHarness, "sha", "greet me")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := Turn(ctx, st, sess, p); err == nil {
		t.Fatal("Turn: want error for 503 upstream")
	}
	evts, err := st.Replay(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	last := evts[len(evts)-1]
	if last.Type != "provider.error" {
		t.Fatalf("last event = %s, want provider.error", last.Type)
	}
	after, _ := st.GetSession(ctx, sess.ID)
	if after.Status != "failed" {
		t.Errorf("status = %q, want failed", after.Status)
	}
}

func TestRegistryResolvesZai(t *testing.T) {
	t.Setenv("ZAI_API_KEY", "test-key")
	p, err := registry.Resolve("z.ai")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	zp, ok := p.(*zai.Provider)
	if !ok {
		t.Fatalf("Resolve(z.ai) = %T, want *zai.Provider", p)
	}
	if zp.Name() != "z.ai" {
		t.Errorf("Name = %q, want z.ai", zp.Name())
	}
}
