package chat_test

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

var _ = Describe("swarm activity pane wiring", func() {
	var intent *chat.Intent

	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
		intent = chat.NewIntent(chat.IntentConfig{
			AgentID:      "test-agent",
			SessionID:    "test-session",
			ProviderName: "openai",
			ModelName:    "gpt-4o",
			TokenBudget:  4096,
		})
	})

	AfterEach(func() {
		chat.SetRunningInTestsForTest(false)
	})

	Describe("View renders secondary pane when terminal is wide enough", func() {
		It("includes the Activity Timeline header at 100x24", func() {
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

			view := intent.View()
			Expect(view).To(ContainSubstring("Activity Timeline"))
		})
	})

	Describe("View falls back to single-pane below dual-pane threshold", func() {
		It("omits the Activity Timeline header at 79x24", func() {
			intent.Update(tea.WindowSizeMsg{Width: 79, Height: 24})

			view := intent.View()
			Expect(view).NotTo(ContainSubstring("Activity Timeline"))
		})
	})

	Describe("Intent constructor seeds the swarm event store", func() {
		It("initialises an empty MemorySwarmStore with the default capacity", func() {
			store := intent.SwarmStoreForTest()
			Expect(store).NotTo(BeNil())
			Expect(store.All()).To(BeEmpty())

			memStore, ok := store.(*streaming.MemorySwarmStore)
			Expect(ok).To(BeTrue(), "chat intent must use MemorySwarmStore")
			Expect(memStore.Capacity()).To(Equal(streaming.DefaultSwarmStoreCapacity))
		})
	})

	Describe("Delegation lifecycle events feed the swarm activity store via the bus", func() {
		It("appends a delegation event when delegation.started fires on the engine bus", func() {
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

			// Plans/Delegation Bus Bridge — Engine to SSE (May 2026):
			// delegation events no longer flow through the chunk
			// pipeline; the chat intent subscribes on the bus and
			// projects DelegationEventData to a SwarmEvent.
			intent.HandleEventBusNotificationForTest(chat.EventBusNotificationMsg{
				DelegationStarted: events.NewDelegationStartedEvent(events.DelegationEventData{
					ChainID:         "chain-99",
					ParentSessionID: "test-session",
					ChildSessionID:  "child-session-99",
					TargetAgent:     "qa-agent",
					Status:          "started",
				}),
			})

			swarmEvents := intent.SwarmStoreForTest().All()
			Expect(swarmEvents).To(HaveLen(1))
			Expect(swarmEvents[0].Type).To(Equal(streaming.EventDelegation))
			Expect(swarmEvents[0].AgentID).To(Equal("qa-agent"))
			Expect(swarmEvents[0].Status).To(Equal("started"))
			Expect(swarmEvents[0].Metadata["child_session_id"]).To(Equal("child-session-99"),
				"child_session_id is the load-bearing field for click-through navigation")

			view := intent.View()
			// The activity row renders the human label "Delegation"
			// (not the wire identifier "delegation"). See the
			// swarm_activity_human_labels_test.go contract.
			Expect(view).To(ContainSubstring("Delegation"))
			Expect(view).To(ContainSubstring("qa-agent"))
		})

		It("ignores delegation chunks (chunk-side path is now consumer-agnostic noise)", func() {
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

			intent.Update(chat.StreamChunkMsg{
				DelegationInfo: &provider.DelegationInfo{
					ChainID:     "chain-chunk",
					TargetAgent: "qa-agent",
					Status:      "started",
				},
			})

			Expect(intent.SwarmStoreForTest().All()).To(BeEmpty(),
				"chunk-side DelegationInfo no longer populates the activity store; "+
					"the engine publishes on the bus and the bus subscriber owns the projection")
		})

		It("appends a tool_call event when a tool.execute.before bus event fires", func() {
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

			// Plans/Tool Execute Bus Bridge — Engine to SSE (May 2026):
			// tool-call events no longer flow through the chunk pipeline;
			// the chat intent subscribes to tool.execute.before on the
			// engine's event bus and projects ToolEventData onto a
			// SwarmEvent via appendToolCallSwarmEvent. The TUI ends as a
			// bus consumer indistinguishable from the web SSE handler.
			intent.HandleEventBusNotificationForTest(chat.EventBusNotificationMsg{
				ToolBefore: events.NewToolEvent(events.ToolEventData{
					SessionID:          "test-session",
					ToolName:           "ReadFile",
					ToolCallID:         "toolu_01READ",
					InternalToolCallID: "fs_internal_read",
				}),
			})

			swarmEvents := intent.SwarmStoreForTest().All()
			Expect(swarmEvents).To(HaveLen(1))
			Expect(swarmEvents[0].Type).To(Equal(streaming.EventToolCall))
			Expect(swarmEvents[0].Status).To(Equal("started"))
			Expect(swarmEvents[0].ID).To(Equal("fs_internal_read"),
				"the SwarmEvent.ID is the FlowState-internal correlation id for failover-stable coalesce")
			Expect(swarmEvents[0].Metadata["provider_tool_use_id"]).To(Equal("toolu_01READ"))

			view := intent.View()
			Expect(view).To(ContainSubstring("Tool Call"))
		})

		It("ignores tool-call chunks (chunk-side path is now consumer-agnostic noise)", func() {
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

			intent.Update(chat.StreamChunkMsg{
				ToolCallName: "ReadFile",
				ToolStatus:   "started",
			})

			Expect(intent.SwarmStoreForTest().All()).To(BeEmpty(),
				"chunk-side ToolCallName/ToolStatus no longer populates the activity store; "+
					"the engine publishes tool.execute.before on the bus and the bus subscriber "+
					"owns the projection")
		})

		It("appends a tool_result event when tool.execute.result fires on the bus", func() {
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

			intent.HandleEventBusNotificationForTest(chat.EventBusNotificationMsg{
				ToolResult: events.NewToolExecuteResultEvent(events.ToolExecuteResultEventData{
					SessionID:          "test-session",
					ToolName:           "ReadFile",
					Result:             "file contents",
					ToolCallID:         "toolu_01READ",
					InternalToolCallID: "fs_internal_read",
				}),
			})

			swarmEvents := intent.SwarmStoreForTest().All()
			Expect(swarmEvents).To(HaveLen(1))
			Expect(swarmEvents[0].Type).To(Equal(streaming.EventToolResult))
			Expect(swarmEvents[0].Status).To(Equal("completed"))
			Expect(swarmEvents[0].ID).To(Equal("fs_internal_read"),
				"call/result share the InternalToolCallID so coalesce pairs them")
			Expect(swarmEvents[0].Metadata["content"]).To(Equal("file contents"))
		})

		It("appends an error tool_result event when tool.execute.error fires on the bus", func() {
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

			intent.HandleEventBusNotificationForTest(chat.EventBusNotificationMsg{
				ToolError: events.NewToolExecuteErrorEvent(events.ToolExecuteErrorEventData{
					SessionID:          "test-session",
					ToolName:           "ReadFile",
					Error:              fmt.Errorf("permission denied"),
					ToolCallID:         "toolu_01ERR",
					InternalToolCallID: "fs_internal_err",
				}),
			})

			swarmEvents := intent.SwarmStoreForTest().All()
			Expect(swarmEvents).To(HaveLen(1))
			Expect(swarmEvents[0].Type).To(Equal(streaming.EventToolResult))
			Expect(swarmEvents[0].Status).To(Equal("error"))
			Expect(swarmEvents[0].ID).To(Equal("fs_internal_err"))
			Expect(swarmEvents[0].Metadata["error"]).To(Equal("permission denied"))
		})

		It("ignores tool-result chunks (chunk-side path is now consumer-agnostic noise)", func() {
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

			intent.Update(chat.StreamChunkMsg{
				EventType:  "tool_result",
				ToolCallID: "toolu_01R",
				ToolResult: "ok",
			})

			Expect(intent.SwarmStoreForTest().All()).To(BeEmpty(),
				"chunk-side tool_result no longer populates the activity store; "+
					"the engine publishes tool.execute.result on the bus and the bus subscriber "+
					"owns the projection")
		})

		It("appends a plan event when EventType is plan_artifact", func() {
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

			intent.Update(chat.StreamChunkMsg{
				EventType: streaming.EventTypePlanArtifact,
				Content:   "plan content",
			})

			events := intent.SwarmStoreForTest().All()
			Expect(events).To(HaveLen(1))
			Expect(events[0].Type).To(Equal(streaming.EventPlan))
		})

		It("appends a review event when EventType is review_verdict", func() {
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

			intent.Update(chat.StreamChunkMsg{
				EventType: streaming.EventTypeReviewVerdict,
				Content:   "PASS",
			})

			events := intent.SwarmStoreForTest().All()
			Expect(events).To(HaveLen(1))
			Expect(events[0].Type).To(Equal(streaming.EventReview))
		})

		It("ignores chunks that carry no activity metadata", func() {
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

			intent.Update(chat.StreamChunkMsg{Content: "hello"})

			Expect(intent.SwarmStoreForTest().All()).To(BeEmpty())
		})
	})

	Describe("Gate lifecycle events feed the notification manager via the bus", func() {
		// Plans/Gate Bus Bridge — Engine to SSE and TUI (May 2026):
		// the engine publishes gate.failed when runSwarmGates /
		// dispatchMemberGates halts; the chat intent extends
		// EventBusNotificationMsg with GateFailed and routes through
		// the previously dead surfaceSwarmGateFailure helper. The
		// notification manager renders a Warning notification — the
		// existing renderer is preserved verbatim per the plan's
		// non-replacement constraint.

		It("surfaces a swarm-gate-failure notification on a halt-class gate.failed bus event", func() {
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

			intent.HandleEventBusNotificationForTest(chat.EventBusNotificationMsg{
				GateFailed: events.NewGateFailedEvent(events.GateEventData{
					SwarmID:   "a-team",
					SessionID: "test-session",
					Lifecycle: "post-member",
					MemberID:  "researcher",
					GateName:  "post-member-researcher-relevance-gate",
					GateKind:  "ext:relevance-gate",
					Reason:    "off-topic",
				}),
			})

			mgr := intent.NotificationManagerForTest()
			Expect(mgr).NotTo(BeNil())
			var titles, messages []string
			for _, n := range mgr.Active() {
				titles = append(titles, n.Title)
				messages = append(messages, n.Message)
			}
			Expect(titles).To(ContainElement(ContainSubstring("Swarm gate failure")),
				"the previously dead surfaceSwarmGateFailure helper must fire on a gate.failed bus event")
			Expect(messages).To(ContainElement(ContainSubstring("post-member-researcher-relevance-gate")),
				"notification body must name the failing gate so the operator knows what halted")
			Expect(messages).To(ContainElement(ContainSubstring("off-topic")),
				"notification body must surface the typed Reason from the *swarm.GateError")
		})

		It("does not surface a notification when GateFailed is not set on the message", func() {
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

			intent.HandleEventBusNotificationForTest(chat.EventBusNotificationMsg{
				ToolBefore: events.NewToolEvent(events.ToolEventData{
					SessionID:          "test-session",
					ToolName:           "ReadFile",
					InternalToolCallID: "fs_internal_read",
				}),
			})

			mgr := intent.NotificationManagerForTest()
			Expect(mgr).NotTo(BeNil())
			for _, n := range mgr.Active() {
				Expect(n.Title).NotTo(ContainSubstring("Swarm gate failure"),
					"a non-gate-failed bus event must not produce a swarm-gate-failure notification")
			}
		})
	})

	Describe("recordSwarmEvent with nil store", func() {
		It("is a no-op when the swarm store has been cleared to nil", func() {
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
			intent.SetSwarmStoreForTest(nil)

			// Must not panic on a delegation chunk.
			intent.Update(chat.StreamChunkMsg{
				DelegationInfo: &provider.DelegationInfo{
					ChainID:     "chain-nil",
					TargetAgent: "x",
					Status:      "started",
				},
			})

			// View must still render without panic; the pane gets nil events.
			Expect(func() { _ = intent.View() }).NotTo(Panic())
		})
	})

	Describe("SwarmEventAppendedMsg handling", func() {
		It("returns nil from Update and does not mutate the store", func() {
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

			cmd := intent.Update(chat.SwarmEventAppendedMsg{})

			Expect(cmd).To(BeNil())
			Expect(intent.SwarmStoreForTest().All()).To(BeEmpty())
		})
	})

	Describe("swarmEventFromChunk converter", func() {
		It("returns false for a chunk with no activity metadata", func() {
			_, ok := chat.SwarmEventFromChunkForTest(chat.StreamChunkMsg{Content: "x"}, "agent-1")
			Expect(ok).To(BeFalse())
		})

		It("returns false for a chunk carrying only DelegationInfo (delegation events flow via the bus now)", func() {
			_, ok := chat.SwarmEventFromChunkForTest(chat.StreamChunkMsg{
				DelegationInfo: &provider.DelegationInfo{
					ChainID: "chain-x",
					Status:  "started",
				},
			}, "agent-1")
			Expect(ok).To(BeFalse(),
				"Plans/Delegation Bus Bridge — Engine to SSE (May 2026) lifted "+
					"delegation event production into the engine; the chunk-side "+
					"DelegationInfo branch in swarmEventFromChunk is intentionally "+
					"absent, so a chunk with only DelegationInfo no longer maps")
		})

		It("returns false for a chunk carrying only ToolCallName (tool-call events flow via the bus now)", func() {
			_, ok := chat.SwarmEventFromChunkForTest(chat.StreamChunkMsg{
				ToolCallName: "WriteFile",
				ToolStatus:   "started",
			}, "agent-1")
			Expect(ok).To(BeFalse(),
				"Plans/Tool Execute Bus Bridge — Engine to SSE (May 2026) lifted "+
					"tool-call event production into the engine; the chunk-side "+
					"branch in swarmEventFromChunk is intentionally absent")
		})

		It("returns false for a chunk carrying tool_result metadata (tool-result events flow via the bus now)", func() {
			_, ok := chat.SwarmEventFromChunkForTest(chat.StreamChunkMsg{
				EventType:  "tool_result",
				ToolCallID: "toolu_01R",
				ToolResult: "ok",
			}, "agent-1")
			Expect(ok).To(BeFalse(),
				"chunk-side tool_result no longer maps; the bus path owns the projection")
		})

		It("maps plan_artifact event type to EventPlan", func() {
			ev, ok := chat.SwarmEventFromChunkForTest(chat.StreamChunkMsg{
				EventType: streaming.EventTypePlanArtifact,
			}, "chat-agent")
			Expect(ok).To(BeTrue())
			Expect(ev.Type).To(Equal(streaming.EventPlan))
			Expect(ev.AgentID).To(Equal("chat-agent"),
				"plan events must fall back to the chat agent ID")
		})

		It("maps review_verdict event type to EventReview", func() {
			ev, ok := chat.SwarmEventFromChunkForTest(chat.StreamChunkMsg{
				EventType: streaming.EventTypeReviewVerdict,
			}, "chat-agent")
			Expect(ok).To(BeTrue())
			Expect(ev.Type).To(Equal(streaming.EventReview))
		})

		It("does not record a swarm event for a pure-content status_transition chunk", func() {
			_, ok := chat.SwarmEventFromChunkForTest(chat.StreamChunkMsg{
				EventType: streaming.EventTypeStatusTransition,
				Content:   "any",
			}, "chat-agent")
			Expect(ok).To(BeFalse(),
				"status_transition chunks must not produce SwarmEvents")
		})
	})

	Describe("P2 T3: SwarmEvent.ID is populated for every event kind", func() {
		// Plans/Delegation Bus Bridge — Engine to SSE (May 2026): the
		// chunk-side delegation branch is removed; the bus path owns
		// the projection and uses ChainID as the SwarmEvent.ID via
		// appendDelegationSwarmEvent.
		// Plans/Tool Execute Bus Bridge — Engine to SSE (May 2026): the
		// chunk-side tool-call/tool-result branches are also removed;
		// the bus path owns the projection and uses InternalToolCallID
		// as the SwarmEvent.ID via appendToolCallSwarmEvent /
		// appendToolResultSwarmEvent. Both contracts are pinned in the
		// bus-driven specs above and in swarm_event_invariants_test.go.

		It("generates a non-empty ID for plan events", func() {
			ev, ok := chat.SwarmEventFromChunkForTest(chat.StreamChunkMsg{
				EventType: streaming.EventTypePlanArtifact,
				Content:   "plan body",
			}, "chat-agent")
			Expect(ok).To(BeTrue())
			Expect(ev.ID).NotTo(BeEmpty(),
				"plan events must have a generated non-empty ID for persistence "+
					"and event-details lookup")
		})

		It("generates a non-empty ID for review events", func() {
			ev, ok := chat.SwarmEventFromChunkForTest(chat.StreamChunkMsg{
				EventType: streaming.EventTypeReviewVerdict,
				Content:   "PASS",
			}, "chat-agent")
			Expect(ok).To(BeTrue())
			Expect(ev.ID).NotTo(BeEmpty(),
				"review events must have a generated non-empty ID for persistence "+
					"and event-details lookup")
		})

		It("produces distinct IDs for successive plan chunks (UUID uniqueness)", func() {
			ev1, ok1 := chat.SwarmEventFromChunkForTest(chat.StreamChunkMsg{
				EventType: streaming.EventTypePlanArtifact,
				Content:   "plan 1",
			}, "chat-agent")
			ev2, ok2 := chat.SwarmEventFromChunkForTest(chat.StreamChunkMsg{
				EventType: streaming.EventTypePlanArtifact,
				Content:   "plan 2",
			}, "chat-agent")
			Expect(ok1).To(BeTrue())
			Expect(ok2).To(BeTrue())
			Expect(ev1.ID).NotTo(Equal(ev2.ID),
				"distinct plan events must produce distinct IDs")
		})
	})

	Describe("handleInputKey defensive fallthrough", func() {
		It("returns nil for key types that match no branch (e.g. tea.KeyF1)", func() {
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

			// tea.KeyF1 does not match any branch in handleInputKey or
			// handleTextInputKey — the function must return nil without
			// mutating the swarm store.
			before := intent.SwarmStoreForTest().All()
			cmd := intent.Update(tea.KeyMsg{Type: tea.KeyF1})
			Expect(cmd).To(BeNil())
			Expect(intent.SwarmStoreForTest().All()).To(HaveLen(len(before)),
				"non-matching key types must not touch the swarm store")
		})
	})
})
