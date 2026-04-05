package tui_test

import (
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/tui"
)

var _ = Describe("PublishResumedEvent", func() {
	var bus *eventbus.EventBus

	BeforeEach(func() {
		bus = eventbus.NewEventBus()
	})

	Context("when sessionID is non-empty", func() {
		It("publishes session.resumed with the correct SessionID", func() {
			var mu sync.Mutex
			var received []*events.SessionResumedEvent

			bus.Subscribe(events.EventSessionResumed, func(event any) {
				if e, ok := event.(*events.SessionResumedEvent); ok {
					mu.Lock()
					received = append(received, e)
					mu.Unlock()
				}
			})

			tui.PublishResumedEvent(bus, "session-abc")

			mu.Lock()
			defer mu.Unlock()
			Expect(received).To(HaveLen(1))
			Expect(received[0].EventType()).To(Equal("session.resumed"))
			Expect(received[0].Data.SessionID).To(Equal("session-abc"))
		})
	})

	Context("when sessionID is empty", func() {
		It("does not publish session.resumed", func() {
			var mu sync.Mutex
			var received []*events.SessionResumedEvent

			bus.Subscribe(events.EventSessionResumed, func(event any) {
				if e, ok := event.(*events.SessionResumedEvent); ok {
					mu.Lock()
					received = append(received, e)
					mu.Unlock()
				}
			})

			tui.PublishResumedEvent(bus, "")

			mu.Lock()
			defer mu.Unlock()
			Expect(received).To(BeEmpty())
		})
	})

	Context("when bus is nil", func() {
		It("does not panic", func() {
			Expect(func() {
				tui.PublishResumedEvent(nil, "session-xyz")
			}).NotTo(Panic())
		})
	})
})
