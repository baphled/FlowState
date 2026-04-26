package engine_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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

// ProviderSummariser tests cover the engine's adapter that wraps a
// provider.Provider's Chat method behind the ctxstore.Summariser interface.
// Coverage:
//   - nil provider returns a typed sentinel (engine.ErrNilProvider).
//   - no resolver wired: every Summarise call uses the fallback model.
//   - resolver + manifest: requests are routed to the category-mapped model
//     and provider.
//   - unknown summary tier: the adapter degrades to the fallback model
//     rather than surfacing the lookup miss.
//   - provider errors are wrapped (errors.Is finds the sentinel).
var _ = Describe("ProviderSummariser.Summarise", func() {
	It("returns ErrNilProvider when constructed with a nil provider", func() {
		adapter := engine.NewProviderSummariser(nil, nil, "fallback-model")
		_, err := adapter.Summarise(context.Background(), "sys", "user", nil)
		Expect(errors.Is(err, engine.ErrNilProvider)).To(BeTrue(),
			"err = %v; want ErrNilProvider", err)
	})

	It("uses the fallback model and forwards system+user messages when no resolver is wired", func() {
		chat := &stubChatProvider{response: `{"intent":"x","next_steps":["y"]}`}
		adapter := engine.NewProviderSummariser(chat, nil, "fallback-llm")

		out, err := adapter.Summarise(context.Background(), "sys prompt", "user prompt", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(`{"intent":"x","next_steps":["y"]}`))
		Expect(chat.received.Model).To(Equal("fallback-llm"))
		Expect(chat.received.Messages).To(HaveLen(2))
		Expect(chat.received.Messages[0].Role).To(Equal("system"))
		Expect(chat.received.Messages[0].Content).To(Equal("sys prompt"))
		Expect(chat.received.Messages[1].Role).To(Equal("user"))
		Expect(chat.received.Messages[1].Content).To(Equal("user prompt"))
	})

	It("routes to the resolver-mapped model and provider when a manifest is bound", func() {
		categoryResolver := engine.NewCategoryResolver(map[string]engine.CategoryConfig{
			"quick": {Model: "route-target", Provider: "category-provider"},
		})
		resolver := engine.NewSummariserResolver(categoryResolver)

		chat := &stubChatProvider{response: `{"intent":"i","next_steps":["n"]}`}
		manifest := &agent.Manifest{ID: "agent-a"}
		adapter := engine.NewProviderSummariser(chat, resolver, "fallback").WithManifest(manifest)

		_, err := adapter.Summarise(context.Background(), "sys", "user", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(chat.received.Model).To(Equal("route-target"))
		Expect(chat.received.Provider).To(Equal("category-provider"))
	})

	It("falls back to the fallback model when the resolver reports an unknown tier", func() {
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
		Expect(err).NotTo(HaveOccurred())
		Expect(chat.received.Model).To(Equal("safety-net"))
	})

	It("wraps the provider's Chat error so errors.Is recovers the sentinel", func() {
		sentinel := errors.New("simulated provider outage")
		chat := &stubChatProvider{err: sentinel}
		adapter := engine.NewProviderSummariser(chat, nil, "m")

		_, err := adapter.Summarise(context.Background(), "sys", "user", nil)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, sentinel)).To(BeTrue(),
			"err = %v; want wrapped sentinel", err)
	})
})
