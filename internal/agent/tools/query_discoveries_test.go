package tools_test

import (
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent/tools"
)

type fakeQueryStore struct {
	queryCalled bool
	queryArgs   tools.QueryDiscoveriesInput
	queryIDs    []string
	queryErr    error
}

// Publish is a stub to satisfy the DiscoveryStore interface.
func (f *fakeQueryStore) Publish(event any) error {
	return nil
}

// Watch is a stub to satisfy the DiscoveryStore interface.
func (f *fakeQueryStore) Watch() (<-chan any, error) {
	ch := make(chan any)
	close(ch)
	return ch, nil
}

func (f *fakeQueryStore) Query(input any) ([]any, error) {
	f.queryCalled = true
	if q, ok := input.(tools.QueryDiscoveriesInput); ok {
		f.queryArgs = q
	}
	result := make([]any, len(f.queryIDs))
	for i, id := range f.queryIDs {
		result[i] = map[string]any{"ID": id}
	}
	return result, f.queryErr
}

var _ = Describe("QueryDiscoveriesTool", func() {
	var (
		discoveryStore *fakeQueryStore
		tool           *tools.QueryDiscoveriesTool
	)

	BeforeEach(func() {
		discoveryStore = &fakeQueryStore{}
		tool = tools.NewQueryDiscoveriesTool(discoveryStore)
	})

	Context("when no filters are provided", func() {
		It("returns all discovery IDs", func() {
			discoveryStore.queryIDs = []string{"id1", "id2"}
			input := tools.QueryDiscoveriesInput{}
			_, err := tool.Run(input)
			Expect(err).NotTo(HaveOccurred())
			Expect(discoveryStore.queryArgs).To(Equal(input))
		})
	})

	Context("when filters are provided", func() {
		It("filters by Kind", func() {
			input := tools.QueryDiscoveriesInput{Kind: "test"}
			discoveryStore.queryIDs = []string{"id3"}
			_, err := tool.Run(input)
			Expect(err).NotTo(HaveOccurred())
			Expect(discoveryStore.queryArgs.Kind).To(Equal("test"))
		})
		It("filters by AgentID", func() {
			input := tools.QueryDiscoveriesInput{AgentID: "agent42"}
			discoveryStore.queryIDs = []string{"id4"}
			_, err := tool.Run(input)
			Expect(err).NotTo(HaveOccurred())
			Expect(discoveryStore.queryArgs.AgentID).To(Equal("agent42"))
		})
		It("filters by MinPriority", func() {
			input := tools.QueryDiscoveriesInput{MinPriority: "medium"}
			discoveryStore.queryIDs = []string{"id5"}
			_, err := tool.Run(input)
			Expect(err).NotTo(HaveOccurred())
			Expect(discoveryStore.queryArgs.MinPriority).To(Equal("medium"))
		})
		It("filters by StartTime and EndTime", func() {
			start := time.Now().Add(-24 * time.Hour)
			end := time.Now()
			input := tools.QueryDiscoveriesInput{StartTime: start, EndTime: end}
			discoveryStore.queryIDs = []string{"id6"}
			_, err := tool.Run(input)
			Expect(err).NotTo(HaveOccurred())
			Expect(discoveryStore.queryArgs.StartTime).To(Equal(start))
			Expect(discoveryStore.queryArgs.EndTime).To(Equal(end))
		})
	})

	Context("when DiscoveryStore.Query returns an error", func() {
		It("propagates the error", func() {
			input := tools.QueryDiscoveriesInput{}
			discoveryStore.queryErr = errors.New("query failed")
			_, err := tool.Run(input)
			Expect(err).To(MatchError(ContainSubstring("query failed")))
		})
	})
})
