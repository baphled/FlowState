package chat_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/streaming"
	chat "github.com/baphled/flowstate/internal/tui/intents/chat"
)

// Phase 14b — consumer migration.
//
// With P14a landed, every stream chunk emitted by the engine carries a
// stable InternalToolCallID that survives failover. Plans/Tool Execute
// Bus Bridge — Engine to SSE (May 2026) lifted the projection from the
// chunk-driven path into the bus path: the chat intent subscribes to
// tool.execute.{before,result,error} and projects ToolEventData /
// ToolExecuteResultEventData / ToolExecuteErrorEventData payloads onto
// SwarmEvents whose ID is the InternalToolCallID so the coalesce state
// machine pairs a tool_call on provider A with its tool_result on
// provider B even though the two events carry disjoint native ids.
//
// These tests pin that behaviour on the bus subscriber's projection
// layer where the choice is now made.
var _ = Describe("P14b cross-provider correlation (bus-driven)", func() {
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

	Describe("SwarmEvent.ID prefers InternalToolCallID over native ToolCallID", func() {
		It("uses InternalToolCallID on tool_call events when both ids are present", func() {
			intent.HandleEventBusNotificationForTest(chat.EventBusNotificationMsg{
				ToolBefore: events.NewToolEvent(events.ToolEventData{
					SessionID:          "test-session",
					ToolName:           "bash",
					ToolCallID:         "toolu_01NATIVE",
					InternalToolCallID: "fs_internal_call",
				}),
			})

			swarmEvents := intent.SwarmStoreForTest().All()
			Expect(swarmEvents).To(HaveLen(1))
			Expect(swarmEvents[0].ID).To(Equal("fs_internal_call"),
				"the SwarmEvent ID must come from InternalToolCallID so the "+
					"coalesce state machine pairs events across provider failover")
		})

		It("falls back to ToolCallID when InternalToolCallID is empty (pre-P14 events)", func() {
			intent.HandleEventBusNotificationForTest(chat.EventBusNotificationMsg{
				ToolBefore: events.NewToolEvent(events.ToolEventData{
					SessionID:  "test-session",
					ToolName:   "bash",
					ToolCallID: "toolu_01NATIVE",
				}),
			})

			swarmEvents := intent.SwarmStoreForTest().All()
			Expect(swarmEvents).To(HaveLen(1))
			Expect(swarmEvents[0].ID).To(Equal("toolu_01NATIVE"),
				"events without InternalToolCallID must keep the native "+
					"ToolCallID as the SwarmEvent ID")
		})

		It("uses InternalToolCallID on tool_result events when both ids are present", func() {
			intent.HandleEventBusNotificationForTest(chat.EventBusNotificationMsg{
				ToolResult: events.NewToolExecuteResultEvent(events.ToolExecuteResultEventData{
					SessionID:          "test-session",
					ToolName:           "bash",
					Result:             "output",
					ToolCallID:         "call_NATIVE_B",
					InternalToolCallID: "fs_internal_call",
				}),
			})

			swarmEvents := intent.SwarmStoreForTest().All()
			Expect(swarmEvents).To(HaveLen(1))
			Expect(swarmEvents[0].Type).To(Equal(streaming.EventToolResult))
			Expect(swarmEvents[0].ID).To(Equal("fs_internal_call"),
				"the SwarmEvent ID on tool_result must come from InternalToolCallID")
		})

		It("pairs tool_call on provider A with tool_result on provider B via InternalToolCallID", func() {
			// Real failover: provider A mints toolu_01A, provider B mints
			// call_XYZ for the same logical call (different native ids).
			// The engine stamps the same InternalToolCallID on both bus
			// events; the SwarmEvent IDs must match so coalesce pairs them.
			intent.HandleEventBusNotificationForTest(chat.EventBusNotificationMsg{
				ToolBefore: events.NewToolEvent(events.ToolEventData{
					SessionID:          "test-session",
					ToolName:           "bash",
					ToolCallID:         "toolu_01A",
					InternalToolCallID: "fs_logical_call",
				}),
			})
			intent.HandleEventBusNotificationForTest(chat.EventBusNotificationMsg{
				ToolResult: events.NewToolExecuteResultEvent(events.ToolExecuteResultEventData{
					SessionID:          "test-session",
					ToolName:           "bash",
					Result:             "output",
					ToolCallID:         "call_XYZ",
					InternalToolCallID: "fs_logical_call",
				}),
			})

			swarmEvents := intent.SwarmStoreForTest().All()
			Expect(swarmEvents).To(HaveLen(2))
			Expect(swarmEvents[0].ID).To(Equal(swarmEvents[1].ID),
				"tool_call on provider A and tool_result on provider B with "+
					"the same InternalToolCallID must produce matching SwarmEvent IDs "+
					"so the activity pane coalesces them into a single line "+
					"(the P14b contract). The native ToolCallIDs remain "+
					"disjoint — that's the audit trail.")
		})
	})
})
