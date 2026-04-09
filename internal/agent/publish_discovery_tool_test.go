package agent_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/recall"
)

type fakeDiscoveryStore struct {
	published  *recall.Discovery
	publishErr error
}

func (f *fakeDiscoveryStore) Publish(d *recall.Discovery) (string, error) {
	f.published = d
	return "fake-id", f.publishErr
}

var _ = Describe("PublishDiscoveryTool", func() {
	var (
		store *fakeDiscoveryStore
		tool  *agent.PublishDiscoveryTool
	)

	BeforeEach(func() {
		store = &fakeDiscoveryStore{}
		tool = &agent.PublishDiscoveryTool{Store: store}
	})

	It("fails if required fields are missing", func() {
		tool.Kind = ""
		tool.Summary = ""
		tool.Details = ""
		result, err := tool.Run()
		Expect(err).To(HaveOccurred())
		Expect(result).To(BeEmpty())
	})

	It("publishes discovery when all required fields are present", func() {
		tool.Kind = "bug"
		tool.Summary = "Something is wrong"
		tool.Details = "Steps to reproduce..."
		tool.Affects = "module X"
		tool.Priority = "high"
		tool.Evidence = "screenshot.png"
		result, err := tool.Run()
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal("fake-id"))
		Expect(store.published).NotTo(BeNil())
		Expect(store.published.Kind).To(Equal("bug"))
		Expect(store.published.Summary).To(Equal("Something is wrong"))
		Expect(store.published.Details).To(Equal("Steps to reproduce..."))
		Expect(store.published.Affects).To(Equal("module X"))
		Expect(store.published.Priority).To(Equal("high"))
		Expect(store.published.Evidence).To(Equal("screenshot.png"))
	})

	It("returns error if store.Publish fails", func() {
		tool.Kind = "bug"
		tool.Summary = "Summary"
		tool.Details = "Details"
		store.publishErr = recall.ErrDiscoveryNotFound
		result, err := tool.Run()
		Expect(err).To(MatchError(recall.ErrDiscoveryNotFound))
		Expect(result).To(BeEmpty())
	})
})
