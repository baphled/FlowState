package chat_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/streaming"
	chat "github.com/baphled/flowstate/internal/tui/intents/chat"
)

// Phase 14b — consumer migration.
//
// With P14a landed, every stream chunk emitted by the engine carries a
// stable InternalToolCallID that survives failover. The chat intent
// layer must prefer that internal id over the native ToolCallID when
// deriving SwarmEvent.ID so the activity pane's coalesce state machine
// pairs a tool_call on provider A with its tool_result on provider B
// even though the two chunks carry disjoint native ids.
//
// These tests pin that behaviour on the SwarmEvent mapping layer
// (where the choice is made) and on the coalesce layer (where the
// pairing happens).
var _ = Describe("P14b cross-provider correlation", func() {
	Describe("SwarmEvent.ID prefers InternalToolCallID over native ToolCallID", func() {
		It("uses InternalToolCallID on tool_call events when both ids are present", func() {
			ev, ok := chat.SwarmEventFromChunkForTest(chat.StreamChunkMsg{
				ToolCallID:         "toolu_01NATIVE",
				InternalToolCallID: "fs_internal_call",
				ToolCallName:       "bash",
				ToolStatus:         "started",
			}, "chat-agent")

			Expect(ok).To(BeTrue())
			Expect(ev.ID).To(Equal("fs_internal_call"),
				"the SwarmEvent ID must come from InternalToolCallID so the "+
					"coalesce state machine pairs chunks across provider failover")
		})

		It("falls back to ToolCallID when InternalToolCallID is empty (pre-P14 chunks)", func() {
			ev, ok := chat.SwarmEventFromChunkForTest(chat.StreamChunkMsg{
				ToolCallID:   "toolu_01NATIVE",
				ToolCallName: "bash",
				ToolStatus:   "started",
			}, "chat-agent")

			Expect(ok).To(BeTrue())
			Expect(ev.ID).To(Equal("toolu_01NATIVE"),
				"chunks without InternalToolCallID (pre-P14, test fixtures) "+
					"must keep the native ToolCallID as the SwarmEvent ID")
		})

		It("uses InternalToolCallID on tool_result events when both ids are present", func() {
			ev, ok := chat.SwarmEventFromChunkForTest(chat.StreamChunkMsg{
				EventType:          "tool_result",
				ToolCallID:         "call_NATIVE_B",
				InternalToolCallID: "fs_internal_call",
				ToolResult:         "output",
			}, "chat-agent")

			Expect(ok).To(BeTrue())
			Expect(ev.Type).To(Equal(streaming.EventToolResult))
			Expect(ev.ID).To(Equal("fs_internal_call"),
				"the SwarmEvent ID on tool_result must come from InternalToolCallID")
		})

		It("pairs tool_call on provider A with tool_result on provider B via InternalToolCallID", func() {
			// Real failover: provider A mints toolu_01A, provider B mints
			// call_XYZ for the same logical call (different native ids).
			// The engine stamps the same InternalToolCallID on both; the
			// SwarmEvent IDs must match so coalesce pairs them.
			call, okCall := chat.SwarmEventFromChunkForTest(chat.StreamChunkMsg{
				ToolCallID:         "toolu_01A",
				InternalToolCallID: "fs_logical_call",
				ToolCallName:       "bash",
				ToolStatus:         "started",
			}, "chat-agent")
			result, okResult := chat.SwarmEventFromChunkForTest(chat.StreamChunkMsg{
				EventType:          "tool_result",
				ToolCallID:         "call_XYZ",
				InternalToolCallID: "fs_logical_call",
				ToolResult:         "output",
			}, "chat-agent")

			Expect(okCall).To(BeTrue())
			Expect(okResult).To(BeTrue())
			Expect(call.ID).To(Equal(result.ID),
				"tool_call on provider A and tool_result on provider B with "+
					"the same InternalToolCallID must produce matching SwarmEvent IDs "+
					"so the activity pane coalesces them into a single line "+
					"(the P14b contract). The native ToolCallIDs remain "+
					"disjoint — that's the audit trail.")
		})
	})
})
