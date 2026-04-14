package engine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
)

// stubChatProvider records the ChatRequest it receives and returns a
// preconfigured response. It satisfies provider.Provider but only
// exercises the Chat method; Stream, Embed, and Models are minimal stubs.
type stubChatProvider struct {
	received provider.ChatRequest
	response string
	err      error
}

func (s *stubChatProvider) Name() string { return "stub" }
func (s *stubChatProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}
func (s *stubChatProvider) Chat(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	s.received = req
	if s.err != nil {
		return provider.ChatResponse{}, s.err
	}
	return provider.ChatResponse{
		Message: provider.Message{Role: "assistant", Content: s.response},
	}, nil
}
func (s *stubChatProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}
func (s *stubChatProvider) Models() ([]provider.Model, error) { return nil, nil }

func TestProviderSummariser_Summarise_NilProvider_ReturnsSentinel(t *testing.T) {
	t.Parallel()

	adapter := engine.NewProviderSummariser(nil, nil, "fallback-model")
	_, err := adapter.Summarise(context.Background(), "sys", "user", nil)
	if !errors.Is(err, engine.ErrNilProvider) {
		t.Fatalf("err = %v; want ErrNilProvider", err)
	}
}

func TestProviderSummariser_Summarise_NoResolver_UsesFallbackModel(t *testing.T) {
	t.Parallel()

	chat := &stubChatProvider{response: `{"intent":"x","next_steps":["y"]}`}
	adapter := engine.NewProviderSummariser(chat, nil, "fallback-llm")

	out, err := adapter.Summarise(context.Background(), "sys prompt", "user prompt", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != `{"intent":"x","next_steps":["y"]}` {
		t.Fatalf("out = %q; want canned response", out)
	}
	if chat.received.Model != "fallback-llm" {
		t.Fatalf("chat request model = %q; want fallback-llm", chat.received.Model)
	}
	if len(chat.received.Messages) != 2 {
		t.Fatalf("chat request messages = %d; want 2", len(chat.received.Messages))
	}
	if chat.received.Messages[0].Role != "system" || chat.received.Messages[0].Content != "sys prompt" {
		t.Fatalf("system message mismatch: %+v", chat.received.Messages[0])
	}
	if chat.received.Messages[1].Role != "user" || chat.received.Messages[1].Content != "user prompt" {
		t.Fatalf("user message mismatch: %+v", chat.received.Messages[1])
	}
}

func TestProviderSummariser_Summarise_WithResolverAndManifest_RoutesToCategoryModel(t *testing.T) {
	t.Parallel()

	categoryResolver := engine.NewCategoryResolver(map[string]engine.CategoryConfig{
		"quick": {Model: "route-target", Provider: "category-provider"},
	})
	resolver := engine.NewSummariserResolver(categoryResolver)

	chat := &stubChatProvider{response: `{"intent":"i","next_steps":["n"]}`}
	manifest := &agent.Manifest{ID: "agent-a"}
	adapter := engine.NewProviderSummariser(chat, resolver, "fallback").WithManifest(manifest)

	_, err := adapter.Summarise(context.Background(), "sys", "user", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chat.received.Model != "route-target" {
		t.Fatalf("chat request model = %q; want route-target (from CategoryResolver)", chat.received.Model)
	}
	if chat.received.Provider != "category-provider" {
		t.Fatalf("chat request provider = %q; want category-provider", chat.received.Provider)
	}
}

func TestProviderSummariser_Summarise_ResolverError_FallsBackToFallbackModel(t *testing.T) {
	t.Parallel()

	// An unknown summary tier makes CategoryResolver.Resolve return
	// errUnknownCategory; the adapter must degrade to fallback rather
	// than propagate the lookup miss into the hot path.
	categoryResolver := engine.NewCategoryResolver(map[string]engine.CategoryConfig{})
	resolver := engine.NewSummariserResolver(categoryResolver)
	chat := &stubChatProvider{response: "ok"}
	manifest := &agent.Manifest{
		ID: "agent-a",
		ContextManagement: agent.ContextManagement{
			SummaryTier: "definitely-not-a-real-tier",
		},
	}

	adapter := engine.NewProviderSummariser(chat, resolver, "safety-net").WithManifest(manifest)

	_, err := adapter.Summarise(context.Background(), "sys", "user", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chat.received.Model != "safety-net" {
		t.Fatalf("chat request model = %q; want safety-net fallback", chat.received.Model)
	}
}

func TestProviderSummariser_Summarise_ProviderError_Wrapped(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("simulated provider outage")
	chat := &stubChatProvider{err: sentinel}
	adapter := engine.NewProviderSummariser(chat, nil, "m")

	_, err := adapter.Summarise(context.Background(), "sys", "user", nil)
	if err == nil {
		t.Fatal("expected error; got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v; want wrapped sentinel", err)
	}
}
