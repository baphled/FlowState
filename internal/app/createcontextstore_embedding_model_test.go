package app

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
)

// createContextStore embedding-model routing.
//
// Smoke discovery on 2026-04-27 (session
// 97306299-bb99-4686-83b7-089e4b0244d6): persisted session JSON recorded
// `embedding_model: "llama3.2"` — the **chat** model, 3072d. The Qdrant
// collection in use was 768d Cosine (`opencode_memory`, 4393 points).
// Querying a 3072d vector against a 768d index yields zero matches with
// no error. Recall reported zero hits and the session proceeded with no
// recall context. The contract was broken silently.
//
// Root cause: createContextStore at internal/app/app.go:3741 passed
// cfg.Providers.Ollama.Model (the chat model) into
// recall.NewEmptyContextStore as the embedding-model identifier. The
// session-store Save path then surfaces store.GetModel() as the
// `embedding_model` field on disk — the wrong identifier was leaking
// into the persisted session metadata, and (more importantly) any
// caller that resolves the embedding model via the store was getting
// the chat model.
//
// These specs pin the contract: the embedding model used for recall is
// cfg.Recall configuration, NOT the chat model. cfg.ResolvedEmbeddingModel()
// is the single source of truth.
var _ = Describe("createContextStore embedding-model routing", func() {
	It("uses cfg.ResolvedEmbeddingModel(), not the chat model identifier", func() {
		cfg := &config.AppConfig{}
		cfg.Providers.Ollama.Model = "llama3.2" // chat model — 3072d, would corrupt 768d collection.
		cfg.EmbeddingModel = "nomic-embed-text" // embedding model — 768d, matches collection.

		store := createContextStore(cfg)
		Expect(store).NotTo(BeNil())
		Expect(store.GetModel()).To(Equal("nomic-embed-text"),
			"createContextStore must seed the FileContextStore with the configured embedding model — using the chat model produces a dimension mismatch against the Qdrant collection and silent zero-hit recall")
	})

	It("falls back to config.DefaultEmbeddingModel when EmbeddingModel is unset", func() {
		cfg := &config.AppConfig{}
		cfg.Providers.Ollama.Model = "llama3.2"

		store := createContextStore(cfg)
		Expect(store.GetModel()).To(Equal(config.DefaultEmbeddingModel),
			"with no explicit embedding model the store must inherit the historical 768d default, never the chat model")
	})

	It("never emits the chat-model identifier as the embedding model — regression for Bug #4", func() {
		// This is the exact reproduction case from the smoke: chat
		// model llama3.2 set, embedding model unset.
		cfg := &config.AppConfig{}
		cfg.Providers.Ollama.Model = "llama3.2"

		store := createContextStore(cfg)
		Expect(store.GetModel()).NotTo(Equal("llama3.2"),
			"the chat model identifier must never reach the FileContextStore as the embedding model — that path silently corrupts Qdrant queries")
	})
})
