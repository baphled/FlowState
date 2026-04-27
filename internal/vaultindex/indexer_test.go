package vaultindex_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/recall/qdrant"
	"github.com/baphled/flowstate/internal/vaultindex"
)

type fakeStore struct {
	mu            sync.Mutex
	exists        bool
	createdName   string
	createdConfig qdrant.CollectionConfig
	upserts       [][]qdrant.Point
	upsertCol     string
	collectionErr error
	createErr     error
	upsertErr     error
}

func (f *fakeStore) CollectionExists(_ context.Context, _ string) (bool, error) {
	return f.exists, f.collectionErr
}

func (f *fakeStore) CreateCollection(_ context.Context, name string, cfg qdrant.CollectionConfig) error {
	f.createdName = name
	f.createdConfig = cfg
	return f.createErr
}

func (f *fakeStore) Upsert(_ context.Context, collection string, points []qdrant.Point, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.upsertCol = collection
	cp := make([]qdrant.Point, len(points))
	copy(cp, points)
	f.upserts = append(f.upserts, cp)
	return nil
}

type fakeEmbedder struct {
	mu     sync.Mutex
	calls  int
	vector []float64
	err    error
}

func (f *fakeEmbedder) Embed(_ context.Context, _ string) ([]float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return append([]float64(nil), f.vector...), nil
}

func newIndexerFixture(root string, reindex bool) (*vaultindex.Indexer, *fakeStore, *fakeEmbedder, *vaultindex.State) {
	GinkgoHelper()
	store := &fakeStore{}
	embedder := &fakeEmbedder{vector: []float64{0.1, 0.2, 0.3}}
	state, err := vaultindex.LoadState(vaultindex.SidecarPath(root))
	Expect(err).ToNot(HaveOccurred())
	indexer := vaultindex.NewIndexer(vaultindex.IndexerConfig{
		VaultRoot:    root,
		Collection:   "vault-test",
		BatchSize:    2,
		EmbeddingDim: 3,
		Reindex:      reindex,
		Chunker:      vaultindex.NewChunker(4, 1),
		Embedder:     embedder,
		Store:        store,
		State:        state,
	})
	return indexer, store, embedder, state
}

var _ = Describe("Indexer", func() {
	Describe("EnsureCollection", func() {
		It("creates the collection when it is missing", func() {
			root := GinkgoT().TempDir()
			indexer, store, _, _ := newIndexerFixture(root, false)
			Expect(indexer.EnsureCollection(context.Background())).To(Succeed())
			Expect(store.createdName).To(Equal("vault-test"))
			Expect(store.createdConfig.VectorSize).To(Equal(3))
			Expect(store.createdConfig.Distance).To(Equal(vaultindex.DefaultDistance))
		})

		It("does not recreate an existing collection", func() {
			root := GinkgoT().TempDir()
			indexer, store, _, _ := newIndexerFixture(root, false)
			store.exists = true
			Expect(indexer.EnsureCollection(context.Background())).To(Succeed())
			Expect(store.createdName).To(BeEmpty())
		})

		It("returns the existence-check error", func() {
			root := GinkgoT().TempDir()
			indexer, store, _, _ := newIndexerFixture(root, false)
			store.collectionErr = errors.New("boom")
			err := indexer.EnsureCollection(context.Background())
			Expect(err).To(MatchError(ContainSubstring("boom")))
		})
	})

	Describe("IndexAll", func() {
		It("embeds each chunk and upserts a point per chunk", func() {
			root := GinkgoT().TempDir()
			writeFile(filepath.Join(root, "note.md"), "alpha beta gamma delta epsilon zeta")

			indexer, store, embedder, _ := newIndexerFixture(root, false)
			summary, err := indexer.IndexAll(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(summary.Total).To(Equal(1))
			Expect(summary.Indexed).To(Equal(1))
			Expect(summary.Skipped).To(Equal(0))
			Expect(summary.Chunks).To(BeNumerically(">", 0))

			Expect(embedder.calls).To(Equal(summary.Chunks))
			Expect(store.upserts).To(HaveLen(1))
			Expect(store.upsertCol).To(Equal("vault-test"))

			point := store.upserts[0][0]
			Expect(point.Vector).To(HaveLen(3))
			Expect(point.Payload["source_file"]).To(Equal("note.md"))
			Expect(point.Payload["content"]).To(BeAssignableToTypeOf(""))
			Expect(point.Payload["chunk_index"]).To(Equal(0))
			Expect(point.ID).ToNot(BeEmpty())
		})

		It("skips files that have not changed since the last pass", func() {
			root := GinkgoT().TempDir()
			path := filepath.Join(root, "note.md")
			writeFile(path, "one two three four five")

			indexer, _, embedder, _ := newIndexerFixture(root, false)
			_, err := indexer.IndexAll(context.Background())
			Expect(err).ToNot(HaveOccurred())
			firstCalls := embedder.calls

			indexer2, _, embedder2, _ := newIndexerFixture(root, false)
			summary, err := indexer2.IndexAll(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(summary.Skipped).To(Equal(1))
			Expect(summary.Indexed).To(Equal(0))
			Expect(embedder2.calls).To(Equal(0))
			Expect(firstCalls).To(BeNumerically(">", 0))
		})

		It("re-embeds files whose mtime advanced", func() {
			root := GinkgoT().TempDir()
			path := filepath.Join(root, "note.md")
			writeFile(path, "one two three four five")

			indexer, _, _, _ := newIndexerFixture(root, false)
			_, err := indexer.IndexAll(context.Background())
			Expect(err).ToNot(HaveOccurred())

			future := time.Now().Add(2 * time.Second)
			Expect(os.Chtimes(path, future, future)).To(Succeed())

			indexer2, _, embedder2, _ := newIndexerFixture(root, false)
			summary, err := indexer2.IndexAll(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(summary.Indexed).To(Equal(1))
			Expect(embedder2.calls).To(BeNumerically(">", 0))
		})

		It("indexes new files added after the previous pass", func() {
			root := GinkgoT().TempDir()
			writeFile(filepath.Join(root, "first.md"), "a b c d")

			indexer, _, _, _ := newIndexerFixture(root, false)
			_, err := indexer.IndexAll(context.Background())
			Expect(err).ToNot(HaveOccurred())

			writeFile(filepath.Join(root, "second.md"), "e f g h")

			indexer2, _, _, _ := newIndexerFixture(root, false)
			summary, err := indexer2.IndexAll(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(summary.Indexed).To(Equal(1))
			Expect(summary.Skipped).To(Equal(1))
		})

		It("re-embeds everything when Reindex is true", func() {
			root := GinkgoT().TempDir()
			writeFile(filepath.Join(root, "note.md"), "alpha beta gamma delta")

			indexer, _, _, _ := newIndexerFixture(root, false)
			_, err := indexer.IndexAll(context.Background())
			Expect(err).ToNot(HaveOccurred())

			indexer2, _, embedder2, _ := newIndexerFixture(root, true)
			summary, err := indexer2.IndexAll(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(summary.Indexed).To(Equal(1))
			Expect(embedder2.calls).To(BeNumerically(">", 0))
		})

		It("propagates embedder failures", func() {
			root := GinkgoT().TempDir()
			writeFile(filepath.Join(root, "note.md"), "alpha beta gamma")

			indexer, _, embedder, _ := newIndexerFixture(root, false)
			embedder.err = errors.New("embed-broken")
			_, err := indexer.IndexAll(context.Background())
			Expect(err).To(MatchError(ContainSubstring("embed-broken")))
		})
	})
})
