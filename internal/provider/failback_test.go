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

func TestFailbackChainImmediateAsyncErrorFailover(t *testing.T) {
	registry := NewRegistry()
	registry.Register(&asyncErrorProvider{})
	registry.Register(&successfulStreamProvider{chunks: []StreamChunk{
		{Content: "hello from B"},
		{Done: true},
	}})

	chain := NewFailbackChain(registry, []ModelPreference{
		{Provider: "async-error", Model: "bad-model"},
		{Provider: "successful", Model: "good-model"},
	}, 10*time.Second)

	ch, err := chain.Stream(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "test"}},
	})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	var chunks []StreamChunk
	for chunk := range ch {
		chunks = append(chunks, chunk)
	}

	if len(chunks) < 1 {
		t.Fatalf("expected at least 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Content != "hello from B" {
		t.Fatalf("expected content from provider B, got: %q", chunks[0].Content)
	}
	if chain.LastProvider() != "successful" {
		t.Fatalf("expected lastProvider 'successful', got: %q", chain.LastProvider())
	}
	if chain.LastModel() != "good-model" {
		t.Fatalf("expected lastModel 'good-model', got: %q", chain.LastModel())
	}
}

func TestFailbackChainNormalStreamingWithReplay(t *testing.T) {
	registry := NewRegistry()
	registry.Register(&successfulStreamProvider{chunks: []StreamChunk{
		{Content: "chunk1"},
		{Content: "chunk2"},
		{Content: "chunk3"},
		{Done: true},
	}})

	chain := NewFailbackChain(registry, []ModelPreference{
		{Provider: "successful", Model: "good-model"},
	}, 10*time.Second)

	ch, err := chain.Stream(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "test"}},
	})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	var contents []string
	for chunk := range ch {
		if chunk.Content != "" {
			contents = append(contents, chunk.Content)
		}
	}

	if len(contents) != 3 {
		t.Fatalf("expected 3 content chunks, got %d: %v", len(contents), contents)
	}
	if contents[0] != "chunk1" || contents[1] != "chunk2" || contents[2] != "chunk3" {
		t.Fatalf("unexpected content order: %v", contents)
	}
}

func TestFailbackChainMidStreamErrorPassthrough(t *testing.T) {
	midStreamErr := errors.New("connection lost mid-stream")
	registry := NewRegistry()
	registry.Register(&successfulStreamProvider{chunks: []StreamChunk{
		{Content: "partial content"},
		{Error: midStreamErr, Done: true},
	}})

	chain := NewFailbackChain(registry, []ModelPreference{
		{Provider: "successful", Model: "good-model"},
	}, 10*time.Second)

	ch, err := chain.Stream(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "test"}},
	})
	if err != nil {
		t.Fatalf("expected no error from Stream(), got: %v", err)
	}

	var chunks []StreamChunk
	for chunk := range ch {
		chunks = append(chunks, chunk)
	}

	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if chunks[0].Content != "partial content" {
		t.Fatalf("expected partial content, got: %q", chunks[0].Content)
	}
	if chunks[1].Error == nil {
		t.Fatalf("expected error in second chunk, got nil")
	}
	if chunks[1].Error.Error() != "connection lost mid-stream" {
		t.Fatalf("expected mid-stream error, got: %v", chunks[1].Error)
	}
}

func TestFailbackChainAllProvidersFailAsync(t *testing.T) {
	registry := NewRegistry()
	registry.Register(&asyncErrorProvider{})

	chain := NewFailbackChain(registry, []ModelPreference{
		{Provider: "async-error", Model: "model-a"},
	}, 10*time.Second)

	_, err := chain.Stream(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "test"}},
	})
	if err == nil {
		t.Fatalf("expected error when all providers fail, got nil")
	}
	if !strings.Contains(err.Error(), "all providers failed") {
		t.Fatalf("expected 'all providers failed' error, got: %v", err)
	}
}

func TestFailbackChainImmediateChannelClose(t *testing.T) {
	registry := NewRegistry()
	registry.Register(&immediateCloseProvider{})
	registry.Register(&successfulStreamProvider{chunks: []StreamChunk{
		{Content: "fallback works"},
		{Done: true},
	}})

	chain := NewFailbackChain(registry, []ModelPreference{
		{Provider: "immediate-close", Model: "dead-model"},
		{Provider: "successful", Model: "good-model"},
	}, 10*time.Second)

	ch, err := chain.Stream(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "test"}},
	})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	var chunks []StreamChunk
	for chunk := range ch {
		chunks = append(chunks, chunk)
	}

	if len(chunks) < 1 {
		t.Fatalf("expected at least 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Content != "fallback works" {
		t.Fatalf("expected fallback content, got: %q", chunks[0].Content)
	}
	if chain.LastProvider() != "successful" {
		t.Fatalf("expected lastProvider 'successful', got: %q", chain.LastProvider())
	}
}

type FailingProvider struct{}

func (p *FailingProvider) Name() string { return "failing" }

func (p *FailingProvider) Chat(_ context.Context, _ ChatRequest) (ChatResponse, error) {
	return ChatResponse{}, errors.New("chat failed")
}

func (p *FailingProvider) Stream(_ context.Context, _ ChatRequest) (<-chan StreamChunk, error) {
	return nil, errors.New("stream failed")
}

func (p *FailingProvider) Embed(_ context.Context, _ EmbedRequest) ([]float64, error) {
	return nil, errors.New("embeddings failed")
}

func (p *FailingProvider) Models() ([]Model, error) {
	return nil, errors.New("list models failed")
}

type asyncErrorProvider struct{}

func (p *asyncErrorProvider) Name() string { return "async-error" }

func (p *asyncErrorProvider) Chat(_ context.Context, _ ChatRequest) (ChatResponse, error) {
	return ChatResponse{}, errors.New("chat failed")
}

func (p *asyncErrorProvider) Stream(_ context.Context, _ ChatRequest) (<-chan StreamChunk, error) {
	ch := make(chan StreamChunk, 1)
	ch <- StreamChunk{Error: errors.New("async 404: model not found"), Done: true}
	close(ch)
	return ch, nil
}

func (p *asyncErrorProvider) Embed(_ context.Context, _ EmbedRequest) ([]float64, error) {
	return nil, errors.New("embeddings failed")
}

func (p *asyncErrorProvider) Models() ([]Model, error) {
	return nil, errors.New("list models failed")
}

type successfulStreamProvider struct {
	chunks []StreamChunk
}

func (p *successfulStreamProvider) Name() string { return "successful" }

func (p *successfulStreamProvider) Chat(_ context.Context, _ ChatRequest) (ChatResponse, error) {
	return ChatResponse{}, nil
}

func (p *successfulStreamProvider) Stream(_ context.Context, _ ChatRequest) (<-chan StreamChunk, error) {
	ch := make(chan StreamChunk, len(p.chunks))
	for _, chunk := range p.chunks {
		ch <- chunk
	}
	close(ch)
	return ch, nil
}

func (p *successfulStreamProvider) Embed(_ context.Context, _ EmbedRequest) ([]float64, error) {
	return nil, nil
}

func (p *successfulStreamProvider) Models() ([]Model, error) {
	return nil, nil
}

type immediateCloseProvider struct{}

func (p *immediateCloseProvider) Name() string { return "immediate-close" }

func (p *immediateCloseProvider) Chat(_ context.Context, _ ChatRequest) (ChatResponse, error) {
	return ChatResponse{}, errors.New("chat failed")
}

func (p *immediateCloseProvider) Stream(_ context.Context, _ ChatRequest) (<-chan StreamChunk, error) {
	ch := make(chan StreamChunk)
	close(ch)
	return ch, nil
}

func (p *immediateCloseProvider) Embed(_ context.Context, _ EmbedRequest) ([]float64, error) {
	return nil, errors.New("embeddings failed")
}

func (p *immediateCloseProvider) Models() ([]Model, error) {
	return nil, errors.New("list models failed")
}
