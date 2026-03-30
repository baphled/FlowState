package api_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
)

var _ = Describe("subscribeSessionBus", func() {
	Describe("with nil EventBus", func() {
		It("returns a no-op unsubscribe without panicking", func() {
			srv := api.NewServer(nil, nil, nil, nil)
			out := make(chan api.WSChunkMsg, 16)
			unsub := srv.SubscribeSessionBus("sess-1", out)
			Expect(unsub).NotTo(Panic())
			unsub()
		})
	})

	Describe("with matching session ID", func() {
		var (
			bus  *eventbus.EventBus
			srv  *api.Server
			out  chan api.WSChunkMsg
			stop func()
		)

		BeforeEach(func() {
			bus = eventbus.NewEventBus()
			srv = api.NewServer(nil, nil, nil, nil, api.WithEventBus(bus))
			out = make(chan api.WSChunkMsg, 16)
			stop = srv.SubscribeSessionBus("sess-1", out)
		})

		AfterEach(func() {
			stop()
		})

		It("forwards tool.execute.before events", func() {
			bus.Publish("tool.execute.before", events.NewToolEvent(events.ToolEventData{
				SessionID: "sess-1",
				ToolName:  "read",
			}))

			var msg api.WSChunkMsg
			Eventually(out, 2*time.Second).Should(Receive(&msg))
			Expect(msg.EventType).To(Equal("tool.execute.before"))
			data := msg.EventData.(map[string]string)
			Expect(data["tool_name"]).To(Equal("read"))
		})

		It("forwards tool.execute.after events with ok status", func() {
			bus.Publish("tool.execute.after", events.NewToolEvent(events.ToolEventData{
				SessionID: "sess-1",
				ToolName:  "read",
			}))

			var msg api.WSChunkMsg
			Eventually(out, 2*time.Second).Should(Receive(&msg))
			Expect(msg.EventType).To(Equal("tool.execute.after"))
			data := msg.EventData.(map[string]any)
			Expect(data["tool_name"]).To(Equal("read"))
			Expect(data["ok"]).To(BeTrue())
		})

		It("forwards tool.execute.after events with error status", func() {
			bus.Publish("tool.execute.after", events.NewToolEvent(events.ToolEventData{
				SessionID: "sess-1",
				ToolName:  "write",
				Error:     someError("permission denied"),
			}))

			var msg api.WSChunkMsg
			Eventually(out, 2*time.Second).Should(Receive(&msg))
			data := msg.EventData.(map[string]any)
			Expect(data["ok"]).To(BeFalse())
		})

		It("forwards provider.rate_limited events regardless of session ID", func() {
			bus.Publish("provider.rate_limited", events.NewProviderEvent(events.ProviderEventData{
				SessionID:    "sess-1",
				ProviderName: "anthropic",
			}))

			var msg api.WSChunkMsg
			Eventually(out, 2*time.Second).Should(Receive(&msg))
			Expect(msg.EventType).To(Equal("provider.rate_limited"))
			data := msg.EventData.(map[string]string)
			Expect(data["provider"]).To(Equal("anthropic"))
		})

		It("forwards provider.rate_limited events from other sessions", func() {
			bus.Publish("provider.rate_limited", events.NewProviderEvent(events.ProviderEventData{
				SessionID:    "sess-other",
				ProviderName: "openai",
			}))

			var msg api.WSChunkMsg
			Eventually(out, 2*time.Second).Should(Receive(&msg))
			Expect(msg.EventType).To(Equal("provider.rate_limited"))
			data := msg.EventData.(map[string]string)
			Expect(data["provider"]).To(Equal("openai"))
		})
	})

	Describe("with non-matching session ID", func() {
		It("drops events for other sessions", func() {
			bus := eventbus.NewEventBus()
			srv := api.NewServer(nil, nil, nil, nil, api.WithEventBus(bus))
			out := make(chan api.WSChunkMsg, 16)
			stop := srv.SubscribeSessionBus("sess-1", out)
			defer stop()

			bus.Publish("tool.execute.before", events.NewToolEvent(events.ToolEventData{
				SessionID: "sess-other",
				ToolName:  "read",
			}))

			Consistently(out, 200*time.Millisecond).ShouldNot(Receive())
		})
	})

	Describe("unsubscribe", func() {
		It("stops forwarding events after unsubscribe is called", func() {
			bus := eventbus.NewEventBus()
			srv := api.NewServer(nil, nil, nil, nil, api.WithEventBus(bus))
			out := make(chan api.WSChunkMsg, 16)
			stop := srv.SubscribeSessionBus("sess-1", out)

			bus.Publish("tool.execute.before", events.NewToolEvent(events.ToolEventData{
				SessionID: "sess-1",
				ToolName:  "bash",
			}))

			Eventually(out, 2*time.Second).Should(Receive())

			stop()

			bus.Publish("tool.execute.before", events.NewToolEvent(events.ToolEventData{
				SessionID: "sess-1",
				ToolName:  "read",
			}))

			Consistently(out, 200*time.Millisecond).ShouldNot(Receive())
		})
	})
})

type someError string

func (e someError) Error() string { return string(e) }
