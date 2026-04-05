package adapter_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/adapter"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
)

var _ = Describe("PluginEventAdapter", func() {
	var (
		bus      *eventbus.EventBus
		adapt    *adapter.PluginEventAdapter
		received []adapter.PublicEvent
		handler  func(adapter.PublicEvent)
	)

	BeforeEach(func() {
		bus = eventbus.NewEventBus()
		adapt = adapter.NewPluginEventAdapter(bus)
		received = nil
		handler = func(e adapter.PublicEvent) {
			received = append(received, e)
		}
	})

	Describe("RegisterPluginSubscription", func() {
		Context("with an exact topic pattern", func() {
			It("translates an internal event to a PublicEvent JSON payload", func() {
				err := adapt.RegisterPluginSubscription("my-plugin", []string{events.EventSessionCreated}, handler)
				Expect(err).NotTo(HaveOccurred())

				evt := events.NewSessionEvent(events.SessionEventData{
					SessionID: "sess-123",
					Action:    "created",
				})
				bus.Publish(events.EventSessionCreated, evt)

				Expect(received).To(HaveLen(1))
				pub := received[0]
				Expect(pub.Type).To(Equal(events.EventSessionCreated))
				Expect(pub.Version).To(Equal("1"))
				Expect(pub.Timestamp).NotTo(BeZero())

				var data map[string]any
				Expect(json.Unmarshal(pub.Data, &data)).To(Succeed())
			})
		})

		Context("with a namespace wildcard pattern", func() {
			It("resolves provider.* to all provider.* catalog topics", func() {
				var topicsReceived []string
				wildcardHandler := func(e adapter.PublicEvent) {
					topicsReceived = append(topicsReceived, e.Type)
				}

				err := adapt.RegisterPluginSubscription("my-plugin", []string{"provider.*"}, wildcardHandler)
				Expect(err).NotTo(HaveOccurred())

				bus.Publish(events.EventProviderError, events.NewProviderErrorEvent(events.ProviderErrorEventData{
					ProviderName: "anthropic",
				}))
				bus.Publish(events.EventProviderRequest, events.NewProviderRequestEvent(events.ProviderRequestEventData{
					ProviderName: "anthropic",
					ModelName:    "claude-3",
				}))

				Expect(topicsReceived).To(ContainElement(events.EventProviderError))
				Expect(topicsReceived).To(ContainElement(events.EventProviderRequest))
			})

			It("only forwards events to plugins subscribed to matching topics", func() {
				var pluginAReceived, pluginBReceived []string

				err := adapt.RegisterPluginSubscription("plugin-a", []string{events.EventSessionCreated}, func(e adapter.PublicEvent) {
					pluginAReceived = append(pluginAReceived, e.Type)
				})
				Expect(err).NotTo(HaveOccurred())

				err = adapt.RegisterPluginSubscription("plugin-b", []string{events.EventProviderError}, func(e adapter.PublicEvent) {
					pluginBReceived = append(pluginBReceived, e.Type)
				})
				Expect(err).NotTo(HaveOccurred())

				bus.Publish(events.EventSessionCreated, events.NewSessionEvent(events.SessionEventData{Action: "created"}))
				bus.Publish(events.EventProviderError, events.NewProviderErrorEvent(events.ProviderErrorEventData{ProviderName: "anthropic"}))

				Expect(pluginAReceived).To(ConsistOf(events.EventSessionCreated))
				Expect(pluginBReceived).To(ConsistOf(events.EventProviderError))
			})
		})

		Context("with an unknown pattern", func() {
			It("returns an error when no catalog entries match", func() {
				err := adapt.RegisterPluginSubscription("bad-plugin", []string{"nonexistent.topic"}, handler)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("nonexistent.topic"))
			})

			It("returns an error for an unknown namespace wildcard", func() {
				err := adapt.RegisterPluginSubscription("bad-plugin", []string{"unknown.*"}, handler)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("unknown.*"))
			})
		})
	})
})
