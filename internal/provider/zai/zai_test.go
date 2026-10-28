package zai_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/zai"
)

func newTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *zai.Provider) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := zai.New("test-key", zai.WithBaseURL(srv.URL))
	return srv, p
}

func mustSatisfyPort(t *testing.T, p *zai.Provider) {
	t.Helper()
	var _ provider.Provider = p
}

func TestNewRequiresAPIKey(t *testing.T) {
	if _, err := zai.NewFromEnv(); err == nil {
		t.Fatal("expected error when ZAI_API_KEY unset")
	}
	t.Setenv("ZAI_API_KEY", "k")
	p, err := zai.NewFromEnv()
	if err != nil {
		t.Fatalf("NewFromEnv: %v", err)
	}
	mustSatisfyPort(t, p)
}

func TestProviderIdentity(t *testing.T) {
	_, p := newTestServer(t, func(http.ResponseWriter, *http.Request) {})
	mustSatisfyPort(t, p)
	if p.Name() != "z.ai" {
		t.Fatalf("Name = %q, want z.ai", p.Name())
	}
	if len(p.Models()) == 0 {
		t.Fatal("Models must not be empty")
	}
}

func TestChatSendsAuthAndParsesContent(t *testing.T) {
	var gotAuth, gotBody, gotPath string
	_, p := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "c1",
			"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": "hello there"}}},
		})
	})
	resp, err := p.Chat(context.Background(), &provider.ChatRequest{
		Model:    "glm-4.6",
		Messages: []provider.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "hello there" {
		t.Fatalf("Content = %q", resp.Content)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if !strings.HasSuffix(gotPath, "/chat/completions") {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotBody, `"model":"glm-4.6"`) {
		t.Fatalf("body missing model: %s", gotBody)
	}
}

func TestChatAPIErrorIsTyped(t *testing.T) {
	_, p := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"1002","message":"invalid api key"}}`))
	})
	_, err := p.Chat(context.Background(), &provider.ChatRequest{Model: "glm-4.6"})
	var apiErr *zai.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *zai.APIError, got %T: %v", err, err)
	}
	if apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("Status = %d", apiErr.Status)
	}
	if apiErr.Code != "1002" || apiErr.Message != "invalid api key" {
		t.Fatalf("unexpected error payload: %+v", apiErr)
	}
	if !errors.Is(err, zai.ErrUnauthorized) {
		t.Fatalf("ErrUnauthorized not matched: %v", err)
	}
}

func TestChatNoSilentRetry(t *testing.T) {
	calls := 0
	_, p := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"internal","message":"boom"}}`))
	})
	_, err := p.Chat(context.Background(), &provider.ChatRequest{Model: "glm-4.6"})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("server called %d times, want exactly 1 (no retries)", calls)
	}
}

func TestStreamYieldsDeltasInOrder(t *testing.T) {
	_, p := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, chunk := range []string{
			`data: {"choices":[{"delta":{"content":"he"}}]}`,
			`data: {"choices":[{"delta":{"content":"llo"}}]}`,
			`data: [DONE]`,
		} {
			_, _ = w.Write([]byte(chunk + "\n\n"))
			fl.Flush()
		}
	})
	var sb strings.Builder
	for ev := range p.Stream(context.Background(), &provider.ChatRequest{Model: "glm-4.6"}) {
		if ev.Err != nil {
			t.Fatalf("stream error: %v", ev.Err)
		}
		sb.WriteString(ev.Content)
	}
	if sb.String() != "hello" {
		t.Fatalf("streamed content = %q, want hello", sb.String())
	}
}

func TestStreamSurfacesTypedError(t *testing.T) {
	_, p := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"1302","message":"rate limited"}}`))
	})
	var gotErr error
	for ev := range p.Stream(context.Background(), &provider.ChatRequest{Model: "glm-4.6"}) {
		if ev.Err != nil {
			gotErr = ev.Err
		}
	}
	var apiErr *zai.APIError
	if !errors.As(gotErr, &apiErr) || apiErr.Status != http.StatusTooManyRequests {
		t.Fatalf("want typed 429 error, got %v", gotErr)
	}
}

func TestStreamContextCancelStops(t *testing.T) {
	release := make(chan struct{})
	_, p := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		<-release
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		for range p.Stream(ctx, &provider.ChatRequest{Model: "glm-4.6"}) {
		}
		close(done)
	}()
	cancel()
	<-done
	close(release)
}
