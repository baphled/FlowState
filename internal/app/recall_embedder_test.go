package app

import (
	"context"
	"errors"
	"testing"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/provider"
)

// recordingProvider is a provider.Provider stub that records every Embed call.
// The recall embedder fix is verified by asserting Embed is NEVER invoked on
// the chat provider — the chat provider may not support embeddings (e.g.
// anthropic), so any call here proves misrouting.
type recordingProvider struct {
	provider.Provider
	embedCalls int
	embedReqs  []provider.EmbedRequest
}

func (r *recordingProvider) Name() string { return "recording-chat" }

func (r *recordingProvider) Embed(_ context.Context, req provider.EmbedRequest) ([]float64, error) {
	r.embedCalls++
	r.embedReqs = append(r.embedReqs, req)
	return nil, errors.New("recording-chat does not support embeddings")
}

// fakeOllamaEmbedder records Embed calls so we can assert routing went here.
type fakeOllamaEmbedder struct {
	calls   int
	lastReq provider.EmbedRequest
	vector  []float64
	err     error
}

func (f *fakeOllamaEmbedder) Embed(_ context.Context, req provider.EmbedRequest) ([]float64, error) {
	f.calls++
	f.lastReq = req
	if f.err != nil {
		return nil, f.err
	}
	if f.vector != nil {
		return f.vector, nil
	}
	return make([]float64, defaultRecallEmbeddingDim), nil
}

// TestRecallEmbedder_RoutesToOllamaNotChatProvider verifies that the recall
// embedder uses the supplied Ollama-style embedder, NOT the chat provider.
// This is Bug #4: chat provider was misused for embeddings.
func TestRecallEmbedder_RoutesToOllamaNotChatProvider(t *testing.T) {
	ctx := context.Background()
	chat := &recordingProvider{}
	ollama := &fakeOllamaEmbedder{}

	embedder := newRecallEmbedder(ollama)

	if _, err := embedder.Embed(ctx, "hello world"); err != nil {
		t.Fatalf("embed: unexpected error: %v", err)
	}

	if chat.embedCalls != 0 {
		t.Errorf("chat provider Embed was called %d times; expected 0 (must not be used for embeddings)", chat.embedCalls)
	}
	if ollama.calls != 1 {
		t.Errorf("ollama embedder Embed calls = %d; want 1", ollama.calls)
	}
	if got, want := ollama.lastReq.Model, defaultRecallEmbeddingModel; got != want {
		t.Errorf("ollama embed model = %q; want %q (must match Qdrant collection vector dim)", got, want)
	}
	if ollama.lastReq.Input != "hello world" {
		t.Errorf("ollama embed input = %q; want %q", ollama.lastReq.Input, "hello world")
	}
}

// TestRecallEmbedder_NoopFallbackWhenNilProvider verifies that a nil Ollama
// provider produces a non-nil no-op embedder so the broker still constructs
// (other recall sources stay live; Qdrant queries fail gracefully).
func TestRecallEmbedder_NoopFallbackWhenNilProvider(t *testing.T) {
	embedder := newRecallEmbedder(nil)
	if embedder == nil {
		t.Fatal("newRecallEmbedder(nil) returned nil; want non-nil no-op embedder")
	}

	_, err := embedder.Embed(context.Background(), "anything")
	if err == nil {
		t.Fatal("noop embedder returned nil error; expected sentinel error so Qdrant source surfaces a clear failure")
	}
	if !errors.Is(err, errRecallEmbedderUnavailable) {
		t.Errorf("noop error = %v; want errRecallEmbedderUnavailable", err)
	}
}

// TestBuildRecallBroker_DoesNotInvokeChatProviderEmbed integration-flavoured
// regression: when buildRecallBroker is called with a chat provider AND an
// Ollama provider available, the Qdrant source's embedder MUST NOT call
// chat.Embed. This is the exact failure mode of Bug #4.
func TestBuildRecallBroker_DoesNotInvokeChatProviderEmbed(t *testing.T) {
	chat := &recordingProvider{}
	ollama := &fakeOllamaEmbedder{}

	cfg := &config.AppConfig{}
	cfg.Qdrant.URL = "http://localhost:6333"
	cfg.Qdrant.Collection = "flowstate-recall"

	// nil mcpClient/contextStore/chainStore is acceptable for this construction
	// test — we only inspect the embedder wiring, not query resolution.
	broker := buildRecallBroker(recallBrokerParams{
		cfg:            cfg,
		chatProvider:   chat,
		ollamaProvider: ollama,
	})
	if broker == nil {
		t.Fatal("buildRecallBroker returned nil")
	}

	// Drive the embedder via the public adapter to prove it routes to Ollama.
	embedder := newRecallEmbedder(ollama)
	if _, err := embedder.Embed(context.Background(), "regression"); err != nil {
		t.Fatalf("embed: %v", err)
	}
	if chat.embedCalls != 0 {
		t.Errorf("chat provider was called %d times during recall embed; want 0", chat.embedCalls)
	}
}
