package learning_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	"github.com/baphled/flowstate/internal/learning"
	"github.com/baphled/flowstate/internal/recall/qdrant"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type recordingStore struct {
	upsertCalls int32
	upsertErrs  []error
}

func (r *recordingStore) Upsert(_ context.Context, _ string, _ []learning.VectorPoint, _ bool) error {
	idx := atomic.AddInt32(&r.upsertCalls, 1) - 1
	if int(idx) >= len(r.upsertErrs) {
		return nil
	}
	return r.upsertErrs[idx]
}

func (r *recordingStore) Search(_ context.Context, _ string, _ []float64, _ int) ([]learning.ScoredVectorPoint, error) {
	return nil, nil
}

type recordingEnsurer struct {
	calls       int32
	wantErr     error
	gotSize     int
	collections []string
}

func (e *recordingEnsurer) EnsureCollection(_ context.Context, name string, vectorSize int, _ string) error {
	atomic.AddInt32(&e.calls, 1)
	e.gotSize = vectorSize
	e.collections = append(e.collections, name)
	return e.wantErr
}

var _ = Describe("EnsuringVectorStore", func() {
	var (
		store   *recordingStore
		ensurer *recordingEnsurer
		points  []learning.VectorPoint
	)

	BeforeEach(func() {
		store = &recordingStore{}
		ensurer = &recordingEnsurer{}
		points = []learning.VectorPoint{
			{ID: "p1", Vector: []float64{0.1, 0.2, 0.3, 0.4}, Payload: map[string]any{}},
		}
	})

	Context("when the underlying upsert succeeds", func() {
		It("does not invoke the ensurer", func() {
			es := learning.NewEnsuringVectorStore(store, ensurer, qdrant.IsCollectionNotFound, "Cosine")
			Expect(es.Upsert(context.Background(), "col", points, false)).To(Succeed())
			Expect(ensurer.calls).To(BeNumerically("==", 0))
			Expect(store.upsertCalls).To(BeNumerically("==", 1))
		})
	})

	Context("when the first upsert returns a 404", func() {
		BeforeEach(func() {
			store.upsertErrs = []error{
				&qdrant.Error{StatusCode: http.StatusNotFound, Message: "Collection `c` doesn't exist"},
			}
		})

		It("auto-creates the collection and retries the upsert", func() {
			es := learning.NewEnsuringVectorStore(store, ensurer, qdrant.IsCollectionNotFound, "Cosine")
			Expect(es.Upsert(context.Background(), "col", points, false)).To(Succeed())
			Expect(ensurer.calls).To(BeNumerically("==", 1))
			Expect(ensurer.gotSize).To(Equal(len(points[0].Vector)))
			Expect(ensurer.collections).To(ContainElement("col"))
			Expect(store.upsertCalls).To(BeNumerically("==", 2))
		})

		It("propagates the create failure joined with the original 404", func() {
			ensurer.wantErr = errors.New("permission denied")
			es := learning.NewEnsuringVectorStore(store, ensurer, qdrant.IsCollectionNotFound, "Cosine")
			err := es.Upsert(context.Background(), "col", points, false)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("permission denied"))
			Expect(err.Error()).To(ContainSubstring("Collection"))
		})

		It("does not re-create the collection on a second 404 for the same name", func() {
			store.upsertErrs = []error{
				&qdrant.Error{StatusCode: http.StatusNotFound, Message: "missing"},
				nil,
				&qdrant.Error{StatusCode: http.StatusNotFound, Message: "missing again"},
				&qdrant.Error{StatusCode: http.StatusNotFound, Message: "still missing"},
			}
			es := learning.NewEnsuringVectorStore(store, ensurer, qdrant.IsCollectionNotFound, "Cosine")
			Expect(es.Upsert(context.Background(), "col", points, false)).To(Succeed())
			Expect(ensurer.calls).To(BeNumerically("==", 1))

			err := es.Upsert(context.Background(), "col", points, false)
			Expect(err).To(HaveOccurred())
			Expect(ensurer.calls).To(BeNumerically("==", 1))
		})
	})

	Context("when the upsert fails with a non-404 error", func() {
		BeforeEach(func() {
			store.upsertErrs = []error{errors.New("connection refused")}
		})

		It("does not invoke the ensurer", func() {
			es := learning.NewEnsuringVectorStore(store, ensurer, qdrant.IsCollectionNotFound, "Cosine")
			err := es.Upsert(context.Background(), "col", points, false)
			Expect(err).To(MatchError(ContainSubstring("connection refused")))
			Expect(ensurer.calls).To(BeNumerically("==", 0))
		})
	})

	Context("when the upsert fails with a 404 but the points slice is empty", func() {
		BeforeEach(func() {
			store.upsertErrs = []error{
				&qdrant.Error{StatusCode: http.StatusNotFound, Message: "missing"},
			}
		})

		It("does not invoke the ensurer (no resolvable vector size)", func() {
			es := learning.NewEnsuringVectorStore(store, ensurer, qdrant.IsCollectionNotFound, "Cosine")
			err := es.Upsert(context.Background(), "col", nil, false)
			Expect(err).To(HaveOccurred())
			Expect(ensurer.calls).To(BeNumerically("==", 0))
		})
	})
})

var _ = Describe("EnsuringVectorStore against a fake Qdrant", func() {
	It("creates the collection on the first 404 and succeeds on retry", func() {
		var (
			collectionExistsCalls int32
			createCalls           int32
			upsertCalls           int32
		)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/collections/auto":
				atomic.AddInt32(&collectionExistsCalls, 1)
				if atomic.LoadInt32(&createCalls) == 0 {
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"status":"not found"}`))
					return
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{}`))
			case r.Method == http.MethodPut && r.URL.Path == "/collections/auto":
				atomic.AddInt32(&createCalls, 1)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{}`))
			case r.Method == http.MethodPut && r.URL.Path == "/collections/auto/points":
				if atomic.AddInt32(&upsertCalls, 1) == 1 {
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"status":"Collection auto doesn't exist"}`))
					return
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{}`))
			default:
				w.WriteHeader(http.StatusInternalServerError)
			}
		}))
		defer server.Close()

		client := qdrant.NewClient(server.URL, "", server.Client())
		adapter := &clientAdapter{client: client}
		es := learning.NewEnsuringVectorStore(adapter, client, qdrant.IsCollectionNotFound, "Cosine")
		points := []learning.VectorPoint{
			{ID: "p1", Vector: []float64{0.1, 0.2, 0.3}, Payload: map[string]any{}},
		}
		Expect(es.Upsert(context.Background(), "auto", points, false)).To(Succeed())
		Expect(atomic.LoadInt32(&createCalls)).To(BeNumerically("==", 1))
		Expect(atomic.LoadInt32(&upsertCalls)).To(BeNumerically("==", 2))
	})
})

type clientAdapter struct{ client *qdrant.Client }

func (a *clientAdapter) Upsert(ctx context.Context, collection string, points []learning.VectorPoint, wait bool) error {
	qp := make([]qdrant.Point, len(points))
	for i, p := range points {
		qp[i] = qdrant.Point{ID: p.ID, Vector: p.Vector, Payload: p.Payload}
	}
	return a.client.Upsert(ctx, collection, qp, wait)
}

func (a *clientAdapter) Search(ctx context.Context, collection string, vector []float64, limit int) ([]learning.ScoredVectorPoint, error) {
	results, err := a.client.Search(ctx, collection, vector, limit)
	if err != nil {
		return nil, err
	}
	out := make([]learning.ScoredVectorPoint, len(results))
	for i, r := range results {
		out[i] = learning.ScoredVectorPoint{ID: r.ID, Score: r.Score, Payload: r.Payload}
	}
	return out, nil
}
