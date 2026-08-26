package recall_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/learning"
	"github.com/baphled/flowstate/internal/recall"
)

type stubLearningSource struct {
	result []any
	err    error
}

func (s *stubLearningSource) Query(ctx context.Context, query string) ([]any, error) {
	return s.result, s.err
}

func (s *stubLearningSource) Observe(ctx context.Context, observations []any) error {
	return nil
}

func (s *stubLearningSource) Synthesize(ctx context.Context, entity string, observations []string) error {
	return nil
}

var _ = Describe("MCPMemorySource", func() {
	var (
		ctx    context.Context
		stubLS *stubLearningSource
	)

	BeforeEach(func() {
		ctx = context.Background()
		stubLS = &stubLearningSource{}
	})

	It("returns empty slice when LearningSource.Query returns nil", func() {
		stubLS.result = nil
		source := recall.NewMCPMemorySource(stubLS)
		results, err := source.Query(ctx, "foo", 10)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(BeEmpty())
	})

	It("maps each learning.Entity to a recall.Observation", func() {
		stubLS.result = []any{
			learning.Entity{Name: "e1", EntityType: "fact", Observations: []string{"obs1", "obs2"}},
		}
		source := recall.NewMCPMemorySource(stubLS)
		results, err := source.Query(ctx, "foo", 10)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(1))
		Expect(results[0].ID).To(Equal("e1"))
		Expect(results[0].Source).To(Equal("mcp-memory"))
		Expect(results[0].Content).To(Equal("obs1\nobs2"))
	})

	It("skips non-Entity items in the result", func() {
		stubLS.result = []any{
			"not-an-entity",
			learning.Entity{Name: "e2", Observations: []string{"x"}},
			123,
		}
		source := recall.NewMCPMemorySource(stubLS)
		results, err := source.Query(ctx, "foo", 10)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(1))
		Expect(results[0].ID).To(Equal("e2"))
	})

	It("respects the limit parameter", func() {
		stubLS.result = []any{
			learning.Entity{Name: "a", Observations: []string{"1"}},
			learning.Entity{Name: "b", Observations: []string{"2"}},
			learning.Entity{Name: "c", Observations: []string{"3"}},
		}
		source := recall.NewMCPMemorySource(stubLS)
		results, err := source.Query(ctx, "foo", 2)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(2))
	})

	It("propagates errors from LearningSource.Query", func() {
		stubLS.err = errors.New("fail")
		source := recall.NewMCPMemorySource(stubLS)
		_, err := source.Query(ctx, "foo", 10)
		Expect(err).To(MatchError("fail"))
	})
})
