package provider

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestFailbackChainWithEmptyPreferences(t *testing.T) {
	t.Run("stream with empty preferences", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register(&FailingProvider{})
		chain := NewFailbackChain(registry, []ModelPreference{}, 10*time.Second)
		ctx := context.Background()
		req := ChatRequest{
			Model:    "test",
			Messages: []Message{{Role: "user", Content: "test"}},
		}
		_, err := chain.Stream(ctx, req)
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "no model preferences configured") {
			t.Fatalf("expected error, got: %v", err)
		}
	})

	t.Run("chat with empty preferences", func(t *testing.T) {
		registry := NewRegistry()
		registry.Register(&FailingProvider{})
		chain := NewFailbackChain(registry, []ModelPreference{}, 10*time.Second)
		ctx := context.Background()
		req := ChatRequest{
			Model:    "test",
			Messages: []Message{{Role: "user", Content: "test"}},
		}
		_, err := chain.Chat(ctx, req)
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "no model preferences configured") {
			t.Fatalf("expected error, got: %v", err)
		}
	})
}

func TestFailbackChainWithNilErrorDoesNotProduceNilError(t *testing.T) {
	registry := NewRegistry()
	registry.Register(&FailingProvider{})

	chain := NewFailbackChain(registry, []ModelPreference{
		{Provider: "failing", Model: "test"},
	}, 10*time.Second)

	ctx := context.Background()
	req := ChatRequest{
		Model:    "test",
		Messages: []Message{{Role: "user", Content: "test"}},
	}

	_, err := chain.Chat(ctx, req)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	errStr := err.Error()
	if strings.Contains(errStr, "%!") {
		t.Fatalf("error message contains unformatted verb: %q", errStr)
	}
}

type FailingProvider struct{}

func (p *FailingProvider) Name() string { return "failing" }

func (p *FailingProvider) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	return ChatResponse{}, errors.New("chat failed")
}

func (p *FailingProvider) Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	return nil, errors.New("stream failed")
}

func (p *FailingProvider) Embed(ctx context.Context, req EmbedRequest) ([]float64, error) {
	return nil, errors.New("embeddings failed")
}

func (p *FailingProvider) Models() ([]Model, error) {
	return nil, errors.New("list models failed")
}
