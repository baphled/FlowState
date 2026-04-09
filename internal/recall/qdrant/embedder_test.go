package qdrant_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/recall/qdrant"
)

type mockProviderEmbedder struct {
	calledWith string
	vector     []float64
	err        error
}

func (m *mockProviderEmbedder) Embed(_ context.Context, text string) ([]float64, error) {
	m.calledWith = text
	return m.vector, m.err
}

var _ = Describe("Embedder", func() {
	Describe("OllamaEmbedder", func() {
		It("delegates to the provider and returns its vector", func() {
			provider := &mockProviderEmbedder{vector: []float64{0.1, 0.2, 0.3}}
			embedder := qdrant.NewOllamaEmbedder(provider)

			vector, err := embedder.Embed(context.Background(), "hello world")

			Expect(err).NotTo(HaveOccurred())
			Expect(vector).To(Equal([]float64{0.1, 0.2, 0.3}))
			Expect(provider.calledWith).To(Equal("hello world"))
		})

		It("returns the provider error", func() {
			provider := &mockProviderEmbedder{err: errors.New("embed failed")}
			embedder := qdrant.NewOllamaEmbedder(provider)

			vector, err := embedder.Embed(context.Background(), "hello world")

			Expect(vector).To(BeNil())
			Expect(err).To(MatchError("embed failed"))
		})
	})

	Describe("MockEmbedder", func() {
		It("returns the configured vector", func() {
			embedder := &qdrant.MockEmbedder{Vector: []float64{1, 2, 3}}

			vector, err := embedder.Embed(context.Background(), "hello world")

			Expect(err).NotTo(HaveOccurred())
			Expect(vector).To(Equal([]float64{1, 2, 3}))
		})

		It("returns the configured error", func() {
			embedder := &qdrant.MockEmbedder{Err: errors.New("boom")}

			vector, err := embedder.Embed(context.Background(), "hello world")

			Expect(vector).To(BeNil())
			Expect(err).To(MatchError("boom"))
		})
	})
})
