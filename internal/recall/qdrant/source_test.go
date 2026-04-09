package qdrant_test

import (
	"context"
	"errors"
	"time"

	"github.com/baphled/flowstate/internal/recall/qdrant"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type mockVectorStore struct {
	searchResult []qdrant.ScoredPoint
	searchErr    error
}

func (m *mockVectorStore) CreateCollection(_ context.Context, _ string, _ qdrant.CollectionConfig) error {
	return nil
}

func (m *mockVectorStore) Upsert(_ context.Context, _ string, _ []qdrant.Point, _ bool) error {
	return nil
}

func (m *mockVectorStore) Search(_ context.Context, _ string, _ []float64, _ int) ([]qdrant.ScoredPoint, error) {
	return m.searchResult, m.searchErr
}

func (m *mockVectorStore) DeleteCollection(_ context.Context, _ string) error { return nil }

func (m *mockVectorStore) CollectionExists(_ context.Context, _ string) (bool, error) {
	return true, nil
}

var _ = Describe("Source", func() {
	var (
		store  *mockVectorStore
		embed  *qdrant.MockEmbedder
		source *qdrant.Source
	)

	BeforeEach(func() {
		store = &mockVectorStore{}
		embed = &qdrant.MockEmbedder{Vector: []float64{0.1, 0.2, 0.3}}
		source = qdrant.NewSource(store, embed, "test-collection")
	})

	It("returns observations from Qdrant search results", func() {
		ts := time.Now().UTC().Truncate(time.Second)
		store.searchResult = []qdrant.ScoredPoint{
			{
				ID:    "obs-001",
				Score: 0.95,
				Payload: map[string]any{
					"content":   "important recall content",
					"agent_id":  "agent-1",
					"timestamp": ts.Format(time.RFC3339),
				},
			},
		}

		obs, err := source.Query(context.Background(), "what happened", 5)
		Expect(err).NotTo(HaveOccurred())
		Expect(obs).To(HaveLen(1))
		Expect(obs[0].ID).To(Equal("obs-001"))
		Expect(obs[0].Source).To(Equal("qdrant:test-collection"))
		Expect(obs[0].Content).To(Equal("important recall content"))
		Expect(obs[0].AgentID).To(Equal("agent-1"))
		Expect(obs[0].Timestamp).To(BeTemporally("~", ts, time.Second))
	})

	It("returns empty slice when search returns no results", func() {
		store.searchResult = []qdrant.ScoredPoint{}

		obs, err := source.Query(context.Background(), "nothing here", 5)
		Expect(err).NotTo(HaveOccurred())
		Expect(obs).NotTo(BeNil())
		Expect(obs).To(BeEmpty())
	})

	It("returns an error when embedding fails", func() {
		embed.Err = errors.New("embed failure")

		_, err := source.Query(context.Background(), "query", 5)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("embed failure"))
	})

	It("returns an error when vector search fails", func() {
		store.searchErr = errors.New("search failure")

		_, err := source.Query(context.Background(), "query", 5)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("search failure"))
	})

	It("handles missing payload fields gracefully", func() {
		store.searchResult = []qdrant.ScoredPoint{
			{ID: "obs-002", Score: 0.8, Payload: map[string]any{}},
		}

		obs, err := source.Query(context.Background(), "query", 5)
		Expect(err).NotTo(HaveOccurred())
		Expect(obs).To(HaveLen(1))
		Expect(obs[0].ID).To(Equal("obs-002"))
		Expect(obs[0].Content).To(BeEmpty())
		Expect(obs[0].AgentID).To(BeEmpty())
	})
})
