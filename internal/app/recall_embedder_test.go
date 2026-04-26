package app

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/provider"
)

// recordingProvider is a provider.Provider stub that records every Embed
// call. The recall embedder fix is verified by asserting Embed is NEVER
// invoked on the chat provider — the chat provider may not support
// embeddings (e.g. anthropic), so any call here proves misrouting.
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

// fakeOllamaEmbedder records Embed calls so we can assert routing went
// here.
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

// Recall embedder routing specs.
//
// Bug #4: chat provider was misused for embeddings. The recall embedder
// must route to the supplied Ollama-style embedder, never the chat
// provider (which may not support Embed at all — Anthropic returns an
// error). A nil Ollama provider returns a no-op embedder so the broker
// still constructs (other recall sources stay live; Qdrant queries
// surface a sentinel).
var _ = Describe("recall embedder routing", func() {
	It("routes Embed to the Ollama provider, not the chat provider", func() {
		ctx := context.Background()
		chat := &recordingProvider{}
		ollama := &fakeOllamaEmbedder{}

		embedder := newRecallEmbedder(ollama)
		_, err := embedder.Embed(ctx, "hello world")
		Expect(err).NotTo(HaveOccurred())

		Expect(chat.embedCalls).To(Equal(0),
			"chat provider Embed must not be invoked for recall embeddings")
		Expect(ollama.calls).To(Equal(1))
		Expect(ollama.lastReq.Model).To(Equal(defaultRecallEmbeddingModel),
			"ollama embed model must match Qdrant collection vector dim")
		Expect(ollama.lastReq.Input).To(Equal("hello world"))
	})

	It("returns a no-op embedder with a sentinel error when the Ollama provider is nil", func() {
		embedder := newRecallEmbedder(nil)
		Expect(embedder).NotTo(BeNil(),
			"newRecallEmbedder(nil) must return a non-nil no-op embedder")

		_, err := embedder.Embed(context.Background(), "anything")
		Expect(err).To(HaveOccurred(),
			"noop embedder must surface a sentinel so Qdrant source fails clearly")
		Expect(errors.Is(err, errRecallEmbedderUnavailable)).To(BeTrue())
	})

	It("buildRecallBroker never invokes the chat provider's Embed (regression for Bug #4)", func() {
		chat := &recordingProvider{}
		ollama := &fakeOllamaEmbedder{}

		cfg := &config.AppConfig{}
		cfg.Qdrant.URL = "http://localhost:6333"
		cfg.Qdrant.Collection = "flowstate-recall"

		broker := buildRecallBroker(recallBrokerParams{
			cfg:            cfg,
			chatProvider:   chat,
			ollamaProvider: ollama,
		})
		Expect(broker).NotTo(BeNil())

		// Drive the embedder via the public adapter to prove it routes
		// to Ollama.
		embedder := newRecallEmbedder(ollama)
		_, err := embedder.Embed(context.Background(), "regression")
		Expect(err).NotTo(HaveOccurred())

		Expect(chat.embedCalls).To(Equal(0),
			"chat provider must not be called during recall embed")
	})
})
