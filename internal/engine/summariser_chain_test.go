package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/baphled/flowstate/internal/provider"
)

type stubProvider struct {
	name    string
	resp    string
	err     error
	calls   int
	lastReq *provider.ChatRequest
}

func (s *stubProvider) Name() string { return s.name }
func (s *stubProvider) Models() ([]provider.Model, error) {
	return nil, errors.New("not implemented")
}
func (s *stubProvider) Embed(ctx context.Context, req provider.EmbedRequest) ([]float64, error) {
	return nil, errors.New("not implemented")
}
func (s *stubProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return nil, errors.New("not implemented")
}
func (s *stubProvider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	s.calls++
	r := req
	s.lastReq = &r
	if s.err != nil {
		return provider.ChatResponse{}, s.err
	}
	return provider.ChatResponse{Message: provider.Message{Role: "assistant", Content: s.resp}}, nil
}

func newChainRegistry(ps ...provider.Provider) *provider.Registry {
	r := provider.NewRegistry()
	for _, p := range ps {
		r.Register(p)
	}
	return r
}

func TestSummariserChainWalksInOrder(t *testing.T) {
	first := &stubProvider{name: "zai", err: errors.New("rate limited")}
	second := &stubProvider{name: "ollama", resp: "summary"}
	chain := NewSummariserChain(newChainRegistry(first, second), []SummariserChainEntry{
		{Provider: "zai", Model: "glm-5.2"},
		{Provider: "ollama", Model: "llama3.2"},
	})
	out, err := chain.Summarise(context.Background(), "sys", "user", nil)
	if err != nil || out != "summary" {
		t.Fatalf("want summary via second hop, got %q err=%v", out, err)
	}
	if first.calls != 1 || second.calls != 1 {
		t.Fatalf("want one attempt per hop, got first=%d second=%d", first.calls, second.calls)
	}
	if chain.Status(nil) != SummariserStatusOK {
		t.Fatalf("want ok status, got %s", chain.Status(nil))
	}
}

func TestSummariserChainEmptySummaryIsFailure(t *testing.T) {
	p := &stubProvider{name: "zai", resp: "   "}
	chain := NewSummariserChain(newChainRegistry(p), []SummariserChainEntry{{Provider: "zai", Model: "glm-5.2"}})
	_, err := chain.Summarise(context.Background(), "sys", "user", nil)
	if !errors.Is(err, ErrSummariserUnavailable) {
		t.Fatalf("empty summary must fail the hop and the chain, got err=%v", err)
	}
}

func TestSummariserChainEmptyEntriesUnavailable(t *testing.T) {
	chain := NewSummariserChain(newChainRegistry(), nil)
	_, err := chain.Summarise(context.Background(), "sys", "user", nil)
	if !errors.Is(err, ErrSummariserUnavailable) {
		t.Fatalf("empty chain must yield ErrSummariserUnavailable, got %v", err)
	}
	if chain.Status(err) != SummariserStatusUnavailable {
		t.Fatalf("want unavailable status, got %s", chain.Status(nil))
	}
}

func TestSummariserChainNilRegistryFailsClosed(t *testing.T) {
	chain := NewSummariserChain(nil, []SummariserChainEntry{{Provider: "zai", Model: "glm-5.2"}})
	_, err := chain.Summarise(context.Background(), "sys", "user", nil)
	if !errors.Is(err, ErrSummariserUnavailable) {
		t.Fatalf("nil registry must fail closed, got %v", err)
	}
}
