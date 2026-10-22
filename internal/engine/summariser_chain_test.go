package engine_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
)

// chainStubProvider doubles a provider for the SummariserChain unit
// tests: it records the last request and returns either the scripted
// response or the scripted error.
type chainStubProvider struct {
	name        string
	response    string
	err         error
	calls       int
	lastRequest provider.ChatRequest
}

func (s *chainStubProvider) Name() string { return s.name }
func (s *chainStubProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}
func (s *chainStubProvider) Chat(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	s.calls++
	s.lastRequest = req
	if s.err != nil {
		return provider.ChatResponse{}, s.err
	}
	return provider.ChatResponse{Message: provider.Message{Role: "assistant", Content: s.response}}, nil
}
func (s *chainStubProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}
func (s *chainStubProvider) Models() ([]provider.Model, error) { return nil, nil }

// SummariserChain tests cover the Phase 1 multi-provider summariser:
// ordered fallback, empty-summary-as-failure, typed terminal error,
// attempted-provider tracking, and status classification.
var _ = Describe("SummariserChain", func() {
	const validSummary = `{"intent":"x","next_steps":["y"]}`

	buildRegistry := func(stubs ...*chainStubProvider) (*provider.Registry, map[string]*chainStubProvider) {
		reg := provider.NewRegistry()
		byName := map[string]*chainStubProvider{}
		for _, stub := range stubs {
			reg.Register(stub)
			byName[stub.name] = stub
		}
		return reg, byName
	}

	defaultEntries := func() []engine.SummariserChainEntry {
		return []engine.SummariserChainEntry{
			{Provider: "zai", Model: "glm-4.7"},
			{Provider: "anthropic", Model: "claude-sonnet-4-20250514"},
			{Provider: "ollama", Model: "llama3.2"},
		}
	}

	It("succeeds on the first provider without calling the fallbacks", func() {
		zai := &chainStubProvider{name: "zai", response: validSummary}
		anthropic := &chainStubProvider{name: "anthropic", response: validSummary}
		reg, _ := buildRegistry(zai, anthropic)
		chain := engine.NewSummariserChain(reg, defaultEntries())

		out, err := chain.Summarise(context.Background(), "sys", "user", nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(validSummary))
		Expect(zai.calls).To(Equal(1))
		Expect(anthropic.calls).To(Equal(0))
		Expect(chain.AttemptedProviders()).To(Equal([]string{"zai"}))
	})

	It("falls back to Anthropic when Z.AI errors", func() {
		zai := &chainStubProvider{name: "zai", err: errors.New("connection refused")}
		anthropic := &chainStubProvider{name: "anthropic", response: validSummary}
		reg, _ := buildRegistry(zai, anthropic)
		chain := engine.NewSummariserChain(reg, defaultEntries())

		out, err := chain.Summarise(context.Background(), "sys", "user", nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(validSummary))
		Expect(chain.AttemptedProviders()).To(Equal([]string{"zai", "anthropic"}))
	})

	It("treats an empty response as a failure and walks the chain", func() {
		zai := &chainStubProvider{name: "zai", response: "   "}
		anthropic := &chainStubProvider{name: "anthropic", response: validSummary}
		reg, _ := buildRegistry(zai, anthropic)
		chain := engine.NewSummariserChain(reg, defaultEntries())

		out, err := chain.Summarise(context.Background(), "sys", "user", nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(validSummary))
		Expect(anthropic.calls).To(Equal(1))
	})

	It("returns a typed error when every provider fails", func() {
		zai := &chainStubProvider{name: "zai", err: errors.New("down")}
		anthropic := &chainStubProvider{name: "anthropic", err: errors.New("down")}
		ollama := &chainStubProvider{name: "ollama", err: errors.New("model llama3.2 does not exist")}
		reg, _ := buildRegistry(zai, anthropic, ollama)
		chain := engine.NewSummariserChain(reg, defaultEntries())

		out, err := chain.Summarise(context.Background(), "sys", "user", nil)

		Expect(out).To(BeEmpty())
		Expect(errors.Is(err, engine.ErrSummariserUnavailable)).To(BeTrue(), "err = %v", err)
		Expect(chain.AttemptedProviders()).To(Equal([]string{"zai", "anthropic", "ollama"}))
		Expect(chain.Status(err)).To(Equal(engine.SummariserStatusUnavailable))
	})

	It("dispatches the configured model name to the Ollama last resort", func() {
		zai := &chainStubProvider{name: "zai", err: errors.New("down")}
		anthropic := &chainStubProvider{name: "anthropic", err: errors.New("down")}
		ollama := &chainStubProvider{name: "ollama", response: validSummary}
		reg, _ := buildRegistry(zai, anthropic, ollama)
		entries := defaultEntries()
		entries[2].Model = "qwen3:14b"
		chain := engine.NewSummariserChain(reg, entries)

		_, err := chain.Summarise(context.Background(), "sys", "user", nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(ollama.lastRequest.Model).To(Equal("qwen3:14b"))
	})

	It("classifies success as the ok status", func() {
		zai := &chainStubProvider{name: "zai", response: validSummary}
		reg, _ := buildRegistry(zai)
		chain := engine.NewSummariserChain(reg, defaultEntries())

		Expect(chain.Status(nil)).To(Equal(engine.SummariserStatusOK))
	})

	It("fails loudly when the registry lacks a configured provider", func() {
		chain := engine.NewSummariserChain(provider.NewRegistry(), defaultEntries())

		_, err := chain.Summarise(context.Background(), "sys", "user", nil)

		Expect(errors.Is(err, engine.ErrSummariserUnavailable)).To(BeTrue())
	})
})
