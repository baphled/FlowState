package learning_test

import (
	"context"
	"errors"
	"time"

	"github.com/baphled/flowstate/internal/learning"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type mockVectorStore struct {
	upsertErr    error
	searchResult []learning.ScoredVectorPoint
	searchErr    error
}

func (m *mockVectorStore) Upsert(_ context.Context, _ string, _ []learning.VectorPoint, _ bool) error {
	return m.upsertErr
}

func (m *mockVectorStore) Search(_ context.Context, _ string, _ []float64, _ int) ([]learning.ScoredVectorPoint, error) {
	return m.searchResult, m.searchErr
}

type mockEmbedder struct {
	vector []float64
	err    error
}

func (m *mockEmbedder) Embed(_ context.Context, _ string) ([]float64, error) {
	return m.vector, m.err
}

var _ = Describe("Mem0LearningStore", func() {
	var (
		vs    *mockVectorStore
		emb   *mockEmbedder
		store *learning.Mem0LearningStore
	)

	BeforeEach(func() {
		vs = &mockVectorStore{}
		emb = &mockEmbedder{vector: []float64{0.1, 0.2, 0.3}}
		store = learning.NewMem0LearningStore(vs, emb, "learning-col")
	})

	Describe("Capture", func() {
		It("stores an entry successfully", func() {
			entry := learning.Entry{
				Timestamp:   time.Now().UTC(),
				AgentID:     "agent-1",
				UserMessage: "hello",
				Response:    "world",
				Outcome:     "success",
			}
			Expect(store.Capture(entry)).To(Succeed())
		})

		Context("when embedding fails", func() {
			BeforeEach(func() {
				emb.err = errors.New("embed failure")
			})

			It("returns a wrapped error", func() {
				err := store.Capture(learning.Entry{UserMessage: "hi"})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("embed failure"))
			})
		})

		Context("when upsert fails", func() {
			BeforeEach(func() {
				vs.upsertErr = errors.New("upsert failure")
			})

			It("returns a wrapped error", func() {
				err := store.Capture(learning.Entry{UserMessage: "hi"})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("upsert failure"))
			})
		})
	})

	Describe("Query", func() {
		Context("when search returns results", func() {
			BeforeEach(func() {
				vs.searchResult = []learning.ScoredVectorPoint{
					{
						ID:    "123",
						Score: 0.9,
						Payload: map[string]any{
							"content":   "what is recall",
							"agent_id":  "agent-1",
							"response":  "it remembers things",
							"outcome":   "success",
							"timestamp": "2026-04-09T10:00:00Z",
						},
					},
				}
			})

			It("returns matching entries", func() {
				entries := store.Query("what is recall")
				Expect(entries).To(HaveLen(1))
				Expect(entries[0].UserMessage).To(Equal("what is recall"))
				Expect(entries[0].AgentID).To(Equal("agent-1"))
				Expect(entries[0].Response).To(Equal("it remembers things"))
				Expect(entries[0].Outcome).To(Equal("success"))
				Expect(entries[0].Timestamp).NotTo(BeZero())
			})
		})

		Context("when embedding fails", func() {
			BeforeEach(func() {
				emb.err = errors.New("embed failed")
			})

			It("returns an empty slice", func() {
				Expect(store.Query("anything")).To(BeEmpty())
			})
		})

		Context("when search fails", func() {
			BeforeEach(func() {
				vs.searchErr = errors.New("search failed")
			})

			It("returns an empty slice", func() {
				Expect(store.Query("anything")).To(BeEmpty())
			})
		})

		Context("when payload has missing fields", func() {
			BeforeEach(func() {
				vs.searchResult = []learning.ScoredVectorPoint{
					{ID: "456", Score: 0.5, Payload: map[string]any{}},
				}
			})

			It("returns an entry with zero values for missing fields", func() {
				entries := store.Query("partial")
				Expect(entries).To(HaveLen(1))
				Expect(entries[0].UserMessage).To(BeEmpty())
				Expect(entries[0].AgentID).To(BeEmpty())
			})
		})
	})
})
