package tools_test

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent/tools"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
)

type fakeDiscoveryStore struct {
	publishCalled bool
	publishArgs   tools.PublishDiscoveryInput
	publishErr    error
	publishID     string
}

func (f *fakeDiscoveryStore) Publish(input tools.PublishDiscoveryInput) (string, error) {
	f.publishCalled = true
	f.publishArgs = input
	return f.publishID, f.publishErr
}

var _ = Describe("PublishDiscoveryTool", func() {
	var (
		discoveryStore *fakeDiscoveryStore
		tool           *tools.PublishDiscoveryTool
	)

	BeforeEach(func() {
		discoveryStore = &fakeDiscoveryStore{}
		tool = tools.NewPublishDiscoveryTool(discoveryStore)
	})

	Context("when required fields are missing", func() {
		It("returns an error if Kind is missing", func() {
			input := tools.PublishDiscoveryInput{Summary: "foo", Priority: "high"}
			_, err := tool.Run(input)
			Expect(err).To(MatchError(ContainSubstring("kind is required")))
		})
		It("returns an error if Summary is missing", func() {
			input := tools.PublishDiscoveryInput{Kind: "test", Priority: "high"}
			_, err := tool.Run(input)
			Expect(err).To(MatchError(ContainSubstring("summary is required")))
		})
		It("returns an error if Priority is missing", func() {
			input := tools.PublishDiscoveryInput{Kind: "test", Summary: "foo"}
			_, err := tool.Run(input)
			Expect(err).To(MatchError(ContainSubstring("priority is required")))
		})
	})

	Context("when all required fields are present", func() {
		It("calls DiscoveryStore.Publish with correct arguments and returns the ID", func() {
			input := tools.PublishDiscoveryInput{Kind: "test", Summary: "foo", Priority: "high"}
			discoveryStore.publishID = "abc123"
			id, err := tool.Run(input)
			Expect(err).NotTo(HaveOccurred())
			Expect(id).To(Equal("abc123"))
			Expect(discoveryStore.publishCalled).To(BeTrue())
			Expect(discoveryStore.publishArgs).To(Equal(input))
		})
		It("returns an error if DiscoveryStore.Publish fails", func() {
			input := tools.PublishDiscoveryInput{Kind: "test", Summary: "foo", Priority: "high"}
			discoveryStore.publishErr = errors.New("store failure")
			_, err := tool.Run(input)
			Expect(err).To(MatchError(ContainSubstring("store failure")))
		})
	})

	Context("when EventBus is provided", func() {
		It("publishes DiscoveryPublishedEvent on successful publish", func() {
			bus := eventbus.NewEventBus()
			var publishedEvent events.Event
			bus.Subscribe(events.EventDiscoveryPublished, func(event any) {
				if e, ok := event.(events.Event); ok {
					publishedEvent = e
				}
			})

			input := tools.PublishDiscoveryInput{
				Kind:     "test",
				Summary:  "Test discovery",
				Priority: "high",
			}
			discoveryStore.publishID = "disc-123"
			tool = tools.NewPublishDiscoveryToolWithBus(discoveryStore, bus)

			id, err := tool.Run(input)
			Expect(err).NotTo(HaveOccurred())
			Expect(id).To(Equal("disc-123"))
			Expect(publishedEvent).NotTo(BeNil())
			Expect(publishedEvent.EventType()).To(Equal(events.EventDiscoveryPublished))

			// Verify event data
			if event, ok := publishedEvent.(*events.DiscoveryPublishedEvent); ok {
				Expect(event.Data.ID).To(Equal("disc-123"))
				Expect(event.Data.Summary).To(Equal("Test discovery"))
				Expect(event.Data.Kind).To(Equal("test"))
				Expect(event.Data.Priority).To(Equal("high"))
			}
		})
	})
})
