package agent_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/recall"
)

type fakeQueryDiscoveryStore struct {
	queries  []recall.DiscoveryQuery
	results  []*recall.Discovery
	queryErr error
}

func (f *fakeQueryDiscoveryStore) Query(q any) ([]any, error) {
	if query, ok := q.(recall.DiscoveryQuery); ok {
		f.queries = append(f.queries, query)
	}
	// Convert results to []any for interface compatibility
	results := make([]any, len(f.results))
	for i, result := range f.results {
		results[i] = result
	}
	return results, f.queryErr
}

var _ = Describe("QueryDiscoveriesTool", func() {
	var (
		store *fakeQueryDiscoveryStore
		tool  *agent.QueryDiscoveriesTool
	)

	BeforeEach(func() {
		store = &fakeQueryDiscoveryStore{}
		tool = &agent.QueryDiscoveriesTool{Store: store}
	})

	It("returns discoveries matching the filter", func() {
		store.results = []*recall.Discovery{
			{Kind: "bug", Summary: "Bug found", Details: "Details...", Affects: "module X", Priority: "high"},
			{Kind: "feature", Summary: "Feature idea", Details: "Details...", Affects: "module Y", Priority: "low"},
		}
		tool.Kind = "bug"
		results, err := tool.Run()
		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(ContainSubstring("Bug found"))
		Expect(results).NotTo(ContainSubstring("Feature idea"))
	})

	It("returns all discoveries if no filter is set", func() {
		store.results = []*recall.Discovery{
			{Kind: "bug", Summary: "Bug found"},
			{Kind: "feature", Summary: "Feature idea"},
		}
		results, err := tool.Run()
		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(ContainSubstring("Bug found"))
		Expect(results).To(ContainSubstring("Feature idea"))
	})

	It("returns error if store.Query fails", func() {
		store.queryErr = recall.ErrDiscoveryNotFound
		_, err := tool.Run()
		Expect(err).To(HaveOccurred())
	})
})
