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

		It("forwards gate.failed events for the matching session as a sanitised payload", func() {
			// Plans/Gate Bus Bridge — Engine to SSE and TUI (May 2026):
			// the engine publishes gate.failed when runSwarmGates /
			// dispatchMemberGates halts. The session bus bridge must
			// forward a sanitised payload onto the out channel so the
			// SSE writer can render the gate-failed banner. Web subscribes
			// ONLY to gate.failed — gate.evaluating and gate.passed are
			// suppressed to keep the failure-signal:noise ratio sane.
			bus.Publish(events.EventGateFailed, events.NewGateFailedEvent(events.GateEventData{
				SwarmID:        "a-team",
				SessionID:      "sess-1",
				Lifecycle:      "post-member",
				MemberID:       "researcher",
				GateName:       "post-member-researcher-relevance-gate",
				GateKind:       "ext:relevance-gate",
				Reason:         "off-topic",
				Cause:          "score below threshold",
				CoordStoreKeys: []string{"chain/researcher/output", "chain/topic/spec"},
			}))

			var msg api.WSChunkMsg
			Eventually(out, 2*time.Second).Should(Receive(&msg))
			Expect(msg.EventType).To(Equal("gate.failed"))
			data, ok := msg.EventData.(map[string]any)
			Expect(ok).To(BeTrue(), "gate.failed EventData must be map[string]any for SSE marshalling")
			Expect(data["event_type"]).To(Equal("gate.failed"))
			Expect(data["swarm_id"]).To(Equal("a-team"))
			Expect(data["lifecycle"]).To(Equal("post-member"))
			Expect(data["member_id"]).To(Equal("researcher"))
			Expect(data["gate_name"]).To(Equal("post-member-researcher-relevance-gate"))
			Expect(data["gate_kind"]).To(Equal("ext:relevance-gate"))
			Expect(data["reason"]).To(Equal("off-topic"))
			Expect(data["cause"]).To(Equal("score below threshold"))
			Expect(data["coord_store_keys"]).To(Equal([]string{"chain/researcher/output", "chain/topic/spec"}))
		})

		It("drops gate.failed events for other sessions", func() {
			bus.Publish(events.EventGateFailed, events.NewGateFailedEvent(events.GateEventData{
				SwarmID:   "a-team",
				SessionID: "sess-other",
				Lifecycle: "pre",
				GateName:  "envelope-check",
				GateKind:  "builtin:result-schema",
				Reason:    "schema validation failed",
			}))

			Consistently(func() bool {
				select {
				case msg := <-out:
					return msg.EventType == "gate.failed"
				default:
					return false
				}
			}, 200*time.Millisecond).Should(BeFalse(),
				"gate.failed events for other sessions must not reach this subscriber")
		})

		It("does NOT subscribe to gate.evaluating (web pass-event policy: failures only)", func() {
			// Plans/Gate Bus Bridge — Engine to SSE and TUI (May 2026):
			// the web SSE bridge subscribes ONLY to gate.failed. A
			// gate.evaluating event must NOT show up on the bridge's
			// out channel — the chat surface is request-reply and an
			// extra evaluating-marker risks UX noise.
			bus.Publish(events.EventGateEvaluating, events.NewGateEvaluatingEvent(events.GateEventData{
				SwarmID:   "a-team",
				SessionID: "sess-1",
				Lifecycle: "pre",
				GateCount: 3,
			}))

			Consistently(func() bool {
				select {
				case msg := <-out:
					return msg.EventType == "gate.evaluating"
				default:
					return false
				}
			}, 200*time.Millisecond).Should(BeFalse(),
				"web SSE bridge must NOT forward gate.evaluating events; pass-event policy is gate.failed-only")
		})

		It("does NOT subscribe to gate.passed (web pass-event policy: failures only)", func() {
			bus.Publish(events.EventGatePassed, events.NewGatePassedEvent(events.GateEventData{
				SwarmID:   "a-team",
				SessionID: "sess-1",
				Lifecycle: "post",
				GateCount: 3,
			}))

			Consistently(func() bool {
				select {
				case msg := <-out:
					return msg.EventType == "gate.passed"
				default:
					return false
				}
			}, 200*time.Millisecond).Should(BeFalse(),
				"web SSE bridge must NOT forward gate.passed events; per-batch passes are TUI-only affordances")
		})

		It("forwards context.compacted events for the matching session", func() {
			// Slice 6a — the engine's L2 auto-compactor publishes
			// EventContextCompacted on success. The session bus
			// bridge must forward the sanitised payload onto the
			// out channel so SSE/WS subscribers can render the
			// compaction affordance (Slice 6b consumes this on the
			// Vue chip).
			//
			// Phase-5 Slice δ added the trigger discriminant to the
			// payload so the chip tooltip can attribute the cause
			// (ratio | gate_proximity | model_switch | tool_result_wave).
			bus.Publish(events.EventContextCompacted, events.NewContextCompactedEvent(events.ContextCompactedEventData{
				SessionID:      "sess-1",
				AgentID:        "Tech-Lead",
				OriginalTokens: 50_000,
				SummaryTokens:  5_000,
				LatencyMS:      420,
				Trigger:        "gate_proximity",
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
			Expect(data["trigger"]).To(Equal("gate_proximity"),
				"Phase-5 Slice δ: bridge handler must surface the Trigger discriminant under the snake_case key the SSE writer + Vue parser expect")
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

		It("forwards streaming.heartbeat events for the matching session", func() {
			// Streaming Coherence Slice F follow-up (Bug Fix #62, May 2026):
			// the engine's runStreamingHeartbeat ticker + the Anthropic
			// ping forwarder publish EventStreamingHeartbeat on the bus.
			// Prior to this fix the typed event had zero subscribers; the
			// frontend's adaptive stall watchdog (chatStore.ts /
			// useSessionStream.ts) consumes the wire frame but had no path
			// from the bus to the SSE/WS pipe. The session bus bridge MUST
			// forward the sanitised payload onto the out channel so both
			// transports project the heartbeat as `streaming.heartbeat`
			// frames.
			bus.Publish(events.EventStreamingHeartbeat, events.NewStreamingHeartbeatEvent(events.StreamingHeartbeatEventData{
				SessionID: "sess-1",
				AgentID:   "Tech-Lead",
				Phase:     "thinking",
			}))

			var msg api.WSChunkMsg
			Eventually(out, 2*time.Second).Should(Receive(&msg))
			Expect(msg.EventType).To(Equal("streaming.heartbeat"))
			data, ok := msg.EventData.(map[string]any)
			Expect(ok).To(BeTrue(), "streaming.heartbeat EventData must be map[string]any for SSE marshalling")
			Expect(data["event_type"]).To(Equal("streaming.heartbeat"))
			Expect(data["session_id"]).To(Equal("sess-1"))
			Expect(data["agent_id"]).To(Equal("Tech-Lead"))
			Expect(data["phase"]).To(Equal("thinking"),
				"phase discriminant lets the frontend's adaptive watchdog pick a per-phase stall threshold")
		})

		// UI Parity PR5 — Live token counter (May 2026).
		//
		// The bus payload's TokenCount must flow through the bridge
		// onto the sanitised SSE map under the snake_case `token_count`
		// key so the SSE writer can emit it on the wire and the Vue
		// parser can read it back. Pre-fix the bridge dropped the
		// field because the handler only copied {event_type,
		// session_id, agent_id, phase} — the heartbeat reached the
		// frontend stripped of its progress dimension.
		It("forwards streaming.heartbeat TokenCount onto the SSE payload as token_count", func() {
			bus.Publish(events.EventStreamingHeartbeat, events.NewStreamingHeartbeatEvent(events.StreamingHeartbeatEventData{
				SessionID:  "sess-1",
				AgentID:    "Tech-Lead",
				Phase:      "generating",
				TokenCount: 1247,
			}))

			var msg api.WSChunkMsg
			Eventually(out, 2*time.Second).Should(Receive(&msg))
			data, ok := msg.EventData.(map[string]any)
			Expect(ok).To(BeTrue())
			Expect(data["token_count"]).To(Equal(int64(1247)),
				"the bridge MUST surface TokenCount under the snake_case wire key so the SSE writer and Vue parser can thread it onto the chat UI's live counter")
		})

		It("drops streaming.heartbeat events for other sessions", func() {
			bus.Publish(events.EventStreamingHeartbeat, events.NewStreamingHeartbeatEvent(events.StreamingHeartbeatEventData{
				SessionID: "sess-other",
				AgentID:   "Tech-Lead",
				Phase:     "thinking",
			}))

			Consistently(func() bool {
				select {
				case msg := <-out:
					return msg.EventType == "streaming.heartbeat"
				default:
					return false
				}
			}, 200*time.Millisecond).Should(BeFalse(),
				"streaming.heartbeat events for other sessions must not reach this subscriber")
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

		It("stops forwarding streaming.heartbeat events after unsubscribe is called", func() {
			bus := eventbus.NewEventBus()
			srv := api.NewServer(nil, nil, nil, nil, api.WithEventBus(bus))
			out := make(chan api.WSChunkMsg, 16)
			stop := srv.SubscribeSessionBus("sess-1", out)

			bus.Publish(events.EventStreamingHeartbeat, events.NewStreamingHeartbeatEvent(events.StreamingHeartbeatEventData{
				SessionID: "sess-1",
				AgentID:   "Tech-Lead",
				Phase:     "thinking",
			}))

			Eventually(out, 2*time.Second).Should(Receive())

			stop()

			bus.Publish(events.EventStreamingHeartbeat, events.NewStreamingHeartbeatEvent(events.StreamingHeartbeatEventData{
				SessionID: "sess-1",
				AgentID:   "Tech-Lead",
				Phase:     "thinking",
			}))

			Consistently(out, 200*time.Millisecond).ShouldNot(Receive())
		})
	})
})

type someError string

func (e someError) Error() string { return string(e) }

// Bug C2 — handleSessionWebSocket panics on bus event after channel close.
//
// internal/api/websocket.go pre-fix did `close(out)` then `stopBus()`. Bus
// handlers (event_bridge.go) perform a non-blocking send via
// `select { case out <- msg: default: }`. On a CLOSED channel `select` does
// NOT take the default branch — it panics with "send on closed channel".
// Between `close(out)` and Unsubscribe any of nine bus topics firing
// (tool.before/result/error, rate_limited, three background_task variants,
// context_compacted, gate.failed) crashes the publisher's goroutine.
//
// The WS-handler fix avoids closing `out` at all (it now signals the
// writer goroutine via a quit channel, mirroring the SSE handler's busCh
// pattern). The specs below pin the bridge-level lifecycle invariants any
// future caller of subscribeSessionBus must respect:
//
//  1. close(out) before stop() panics on a subsequent bus publish — pinning
//     the bug class. Any future caller that closes the channel before
//     unsubscribing repeats the C2 panic; this spec is the contract guard.
//  2. stop() before close(out) is panic-safe — Unsubscribe removes the
//     handler from the bus's snapshot map before the channel is closed,
//     so a subsequent publish finds no handler and never sends.
var _ = Describe("subscribeSessionBus — channel-close lifecycle (Bug C2)", func() {
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

	It("close(out) before stop() panics on a subsequent bus publish — pins the bug class", func() {
		// BUGGY ORDER: close before stop. This is the pre-fix
		// websocket.go cleanup shape. The handler closure (still in
		// the bus's handler list) runs `select { case out <- msg:
		// default: }` which panics on a closed channel. Pins the bug
		// class so any future caller that closes the channel before
		// unsubscribing fails this invariant explicitly.
		close(out)

		Expect(func() {
			bus.Publish(events.EventToolExecuteBefore, events.NewToolEvent(events.ToolEventData{
				SessionID: "sess-1",
				ToolName:  "read",
			}))
		}).To(Panic(),
			"close(out) before stop() must panic on subsequent publish — this is the C2 bug class")

		stop()
	})

	It("stop() before close(out) is panic-safe under publish — pins the fix invariant", func() {
		// SAFE ORDER: stop before close. Unsubscribe removes our
		// handlers from the bus's handler map before the channel
		// closes, so a publish after stop() finds no handlers and
		// never sends to the closed channel. The WS handler post-C2
		// fix avoids close(out) entirely (signalling the writer via a
		// quit channel instead) — this spec stays as a guard for any
		// future caller that does need to close the bridge channel.
		stop()
		close(out)

		Expect(func() {
			bus.Publish(events.EventToolExecuteBefore, events.NewToolEvent(events.ToolEventData{
				SessionID: "sess-1",
				ToolName:  "read",
			}))
		}).NotTo(Panic(),
			"stop() before close(out) must be panic-safe — this is the C2 fix invariant")
	})
})
