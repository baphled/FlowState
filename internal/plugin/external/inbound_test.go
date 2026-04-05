package external_test

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/adapter"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/external"
)

var _ = Describe("InboundHandler", func() {
	var (
		bus     *eventbus.EventBus
		adp     *adapter.PluginEventAdapter
		handler *external.InboundHandler
	)

	BeforeEach(func() {
		bus = eventbus.NewEventBus()
		adp = adapter.NewPluginEventAdapter(bus)
		handler = external.NewInboundHandler("my-plugin", bus, adp)
	})

	Describe("HandleNotification", func() {
		Context("when an external plugin pushes a named event", func() {
			It("publishes to the bus under ext.{plugin}.{name}", func() {
				var received any
				bus.Subscribe("ext.my-plugin.something-happened", func(event any) {
					received = event
				})

				data := json.RawMessage(`{"key":"value"}`)
				err := handler.HandleNotification("something-happened", data)

				Expect(err).NotTo(HaveOccurred())
				Expect(received).NotTo(BeNil())
			})
		})

		Context("when the event name starts with 'ext.'", func() {
			It("rejects the event to protect internal topic namespacing", func() {
				err := handler.HandleNotification("ext.internal.session.created", json.RawMessage(`{}`))

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("must not start with 'ext.'"))
			})
		})

		Context("when the plugin publishes at or below the rate limit", func() {
			It("accepts events within the 10 events/s limit", func() {
				for i := range 10 {
					_ = i
					err := handler.HandleNotification("ping", json.RawMessage(`{}`))
					Expect(err).NotTo(HaveOccurred())
				}
			})
		})

		Context("when the plugin exceeds the rate limit", func() {
			It("drops the event and returns a rate limit error", func() {
				var lastErr error
				for i := range 20 {
					_ = i
					lastErr = handler.HandleNotification("ping", json.RawMessage(`{}`))
				}

				Expect(lastErr).To(HaveOccurred())
				Expect(lastErr.Error()).To(ContainSubstring("rate limit exceeded"))
			})
		})
	})

	Describe("HandleSubscribe", func() {
		Context("when patterns resolve to known catalog topics", func() {
			It("registers the subscription via the adapter without error", func() {
				var received []adapter.PublicEvent
				subHandler := func(e adapter.PublicEvent) {
					received = append(received, e)
				}

				err := handler.HandleSubscribe([]string{"session.created"}, subHandler)

				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("when patterns do not match any catalog entry", func() {
			It("returns an error from the adapter", func() {
				err := handler.HandleSubscribe([]string{"nonexistent.topic"}, func(_ adapter.PublicEvent) {})

				Expect(err).To(HaveOccurred())
			})
		})
	})

	Describe("HandleUnsubscribe", func() {
		It("removes the plugin's subscriptions without panicking", func() {
			_ = handler.HandleSubscribe([]string{"session.created"}, func(_ adapter.PublicEvent) {})
			Expect(func() { handler.HandleUnsubscribe() }).NotTo(Panic())
		})
	})

	Describe("rate limit token refill", func() {
		It("allows new events after the token bucket refills over time", func() {
			for i := range 20 {
				_ = i
				_ = handler.HandleNotification("burst", json.RawMessage(`{}`))
			}

			time.Sleep(200 * time.Millisecond)

			err := handler.HandleNotification("after-refill", json.RawMessage(`{}`))
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
