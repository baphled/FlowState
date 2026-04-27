package vaultindex_test

import (
	"context"
	"encoding/json"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/recall/qdrant"
	"github.com/baphled/flowstate/internal/vaultindex"
)

type fakeSearcher struct {
	limit  int
	col    string
	points []qdrant.ScoredPoint
	err    error
}

func (f *fakeSearcher) Search(_ context.Context, collection string, _ []float64, limit int) ([]qdrant.ScoredPoint, error) {
	f.col = collection
	f.limit = limit
	return f.points, f.err
}

var _ = Describe("QueryHandler", func() {
	var (
		embedder *fakeEmbedder
		searcher *fakeSearcher
		handler  *vaultindex.QueryHandler
	)

	BeforeEach(func() {
		embedder = &fakeEmbedder{vector: []float64{1, 2, 3}}
		searcher = &fakeSearcher{}
		handler = vaultindex.NewQueryHandler(embedder, searcher, "vault-test")
	})

	It("returns an empty response without calling dependencies for empty questions", func() {
		resp, err := handler.Handle(context.Background(), vaultindex.QueryArgs{})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Chunks).To(BeEmpty())
		Expect(embedder.calls).To(Equal(0))
	})

	It("uses DefaultTopK when TopK is zero", func() {
		_, err := handler.Handle(context.Background(), vaultindex.QueryArgs{Question: "hello"})
		Expect(err).ToNot(HaveOccurred())
		Expect(searcher.limit).To(Equal(vaultindex.DefaultTopK))
	})

	It("forwards the configured top_k to the searcher", func() {
		_, err := handler.Handle(context.Background(), vaultindex.QueryArgs{Question: "hello", TopK: 3})
		Expect(err).ToNot(HaveOccurred())
		Expect(searcher.limit).To(Equal(3))
		Expect(searcher.col).To(Equal("vault-test"))
	})

	It("maps Qdrant points to the FlowState chunk shape", func() {
		searcher.points = []qdrant.ScoredPoint{
			{
				ID: "id-1",
				Payload: map[string]any{
					"content":     "first chunk body",
					"source_file": "notes/a.md",
					"chunk_index": float64(2),
				},
			},
			{
				ID: "id-2",
				Payload: map[string]any{
					"content":     "second chunk body",
					"source_file": "notes/b.md",
					"chunk_index": 5,
				},
			},
		}
		resp, err := handler.Handle(context.Background(), vaultindex.QueryArgs{Question: "q"})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Chunks).To(HaveLen(2))
		Expect(resp.Chunks[0]).To(Equal(vaultindex.Chunk{
			Content: "first chunk body", SourceFile: "notes/a.md", ChunkIndex: 2,
		}))
		Expect(resp.Chunks[1].ChunkIndex).To(Equal(5))
	})

	It("emits the contract shape exactly when serialised", func() {
		searcher.points = []qdrant.ScoredPoint{
			{
				Payload: map[string]any{
					"content":     "hello world",
					"source_file": "/docs/note.md",
					"chunk_index": 0,
				},
			},
		}
		resp, err := handler.Handle(context.Background(), vaultindex.QueryArgs{Question: "x"})
		Expect(err).ToNot(HaveOccurred())

		raw, err := json.Marshal(resp)
		Expect(err).ToNot(HaveOccurred())

		var roundTrip struct {
			Chunks []struct {
				Content    string `json:"content"`
				SourceFile string `json:"source_file"`
				ChunkIndex int    `json:"chunk_index"`
			} `json:"chunks"`
		}
		Expect(json.Unmarshal(raw, &roundTrip)).To(Succeed())
		Expect(roundTrip.Chunks).To(HaveLen(1))
		Expect(roundTrip.Chunks[0].Content).To(Equal("hello world"))
		Expect(roundTrip.Chunks[0].SourceFile).To(Equal("/docs/note.md"))
	})

	It("propagates embedder errors", func() {
		embedder.err = errors.New("embed-down")
		_, err := handler.Handle(context.Background(), vaultindex.QueryArgs{Question: "q"})
		Expect(err).To(MatchError(ContainSubstring("embed-down")))
	})

	It("propagates searcher errors", func() {
		searcher.err = errors.New("search-down")
		_, err := handler.Handle(context.Background(), vaultindex.QueryArgs{Question: "q"})
		Expect(err).To(MatchError(ContainSubstring("search-down")))
	})

	It("falls back to zero values for missing payload fields", func() {
		searcher.points = []qdrant.ScoredPoint{{Payload: map[string]any{}}}
		resp, err := handler.Handle(context.Background(), vaultindex.QueryArgs{Question: "q"})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Chunks).To(HaveLen(1))
		Expect(resp.Chunks[0]).To(Equal(vaultindex.Chunk{}))
	})
})
