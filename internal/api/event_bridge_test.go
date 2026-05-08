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

		It("forwards tool.execute.result events with ok status", func() {
			bus.Publish("tool.execute.result", events.NewToolExecuteResultEvent(events.ToolExecuteResultEventData{
				SessionID: "sess-1",
				ToolName:  "read",
			}))

			var msg api.WSChunkMsg
			Eventually(out, 2*time.Second).Should(Receive(&msg))
			Expect(msg.EventType).To(Equal("tool.execute.result"))
			data := msg.EventData.(map[string]any)
			Expect(data["tool_name"]).To(Equal("read"))
			Expect(data["ok"]).To(BeTrue())
		})

		It("forwards tool.execute.error events with error status", func() {
			bus.Publish("tool.execute.error", events.NewToolExecuteErrorEvent(events.ToolExecuteErrorEventData{
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

		It("forwards context.compacted events for the matching session", func() {
			// Slice 6a — the engine's L2 auto-compactor publishes
			// EventContextCompacted on success. The session bus
			// bridge must forward the sanitised payload onto the
			// out channel so SSE/WS subscribers can render the
			// compaction affordance (Slice 6b consumes this on the
			// Vue chip).
			bus.Publish(events.EventContextCompacted, events.NewContextCompactedEvent(events.ContextCompactedEventData{
				SessionID:      "sess-1",
				AgentID:        "Tech-Lead",
				OriginalTokens: 50_000,
				SummaryTokens:  5_000,
				LatencyMS:      420,
			}))

			var msg api.WSChunkMsg
			Eventually(out, 2*time.Second).Should(Receive(&msg))
			Expect(msg.EventType).To(Equal("context.compacted"))
			data, ok := msg.EventData.(map[string]any)
			Expect(ok).To(BeTrue(), "context.compacted EventData must be map[string]any for SSE marshalling")
			Expect(data["event_type"]).To(Equal("context.compacted"))
			Expect(data["session_id"]).To(Equal("sess-1"))
			Expect(data["agent_id"]).To(Equal("Tech-Lead"))
			Expect(data["original_tokens"]).To(Equal(50_000))
			Expect(data["summary_tokens"]).To(Equal(5_000))
			Expect(data["latency_ms"]).To(Equal(int64(420)))
		})

		It("drops context.compacted events for other sessions", func() {
			bus.Publish(events.EventContextCompacted, events.NewContextCompactedEvent(events.ContextCompactedEventData{
				SessionID:      "sess-other",
				AgentID:        "Tech-Lead",
				OriginalTokens: 50_000,
				SummaryTokens:  5_000,
				LatencyMS:      420,
			}))

			Consistently(func() bool {
				select {
				case msg := <-out:
					return msg.EventType == "context.compacted"
				default:
					return false
				}
			}, 200*time.Millisecond).Should(BeFalse(),
				"context.compacted events for other sessions must not reach this subscriber")
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

		It("stops forwarding context.compacted events after unsubscribe is called", func() {
			bus := eventbus.NewEventBus()
			srv := api.NewServer(nil, nil, nil, nil, api.WithEventBus(bus))
			out := make(chan api.WSChunkMsg, 16)
			stop := srv.SubscribeSessionBus("sess-1", out)

			bus.Publish(events.EventContextCompacted, events.NewContextCompactedEvent(events.ContextCompactedEventData{
				SessionID:      "sess-1",
				AgentID:        "Tech-Lead",
				OriginalTokens: 50_000,
				SummaryTokens:  5_000,
				LatencyMS:      420,
			}))

			Eventually(out, 2*time.Second).Should(Receive())

			stop()

			bus.Publish(events.EventContextCompacted, events.NewContextCompactedEvent(events.ContextCompactedEventData{
				SessionID:      "sess-1",
				AgentID:        "Tech-Lead",
				OriginalTokens: 50_000,
				SummaryTokens:  5_000,
				LatencyMS:      420,
			}))

			Consistently(out, 200*time.Millisecond).ShouldNot(Receive())
		})
	})
})

type someError string

func (e someError) Error() string { return string(e) }
