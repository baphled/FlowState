package swarmactivity_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/streaming"
	swarmactivity "github.com/baphled/flowstate/internal/tui/components/swarm_activity"
)

// P14b coalescer tests cover the consumer-migration contract: the coalesce
// state machine pairs events by SwarmEvent.ID, which the chat intent now
// derives from the engine-stamped InternalToolCallID. Two scenarios:
//
//  1. Cross-provider failover: a tool_call comes in on provider A with
//     native id "toolu_01" and the tool_result on provider B with native id
//     "call_XYZ". With P14 wiring the chat intent maps both to the same
//     internal SwarmEvent.ID, so coalesce collapses them. If the consumer
//     ever regresses to keying on the native id, this test fails.
//
//  2. Disjoint internal ids: two unrelated calls under different internal
//     ids must NOT pair — guards against any future internal-id collision
//     in the engine.
var _ = Describe("CoalesceToolCalls (P14 internal-id contract)", func() {
	It("pairs tool_call and tool_result across providers when SwarmEvent.ID matches", func() {
		// Both events carry the same SwarmEvent.ID derived from the engine's
		// internal correlator, even though the native tool-use ids
		// (captured as metadata for audit) are disjoint across the two
		// providers.
		events := []streaming.SwarmEvent{
			{
				ID:      "fs_internal_call",
				Type:    streaming.EventToolCall,
				Status:  "started",
				AgentID: "tool-agent",
				Metadata: map[string]interface{}{
					"tool_name":            "bash",
					"provider_tool_use_id": "toolu_01", // provider A's native id
				},
			},
			{
				ID:      "fs_internal_call",
				Type:    streaming.EventToolResult,
				Status:  "completed",
				AgentID: "tool-agent",
				Metadata: map[string]interface{}{
					"content":              "output",
					"provider_tool_use_id": "call_XYZ", // provider B's native id
				},
			},
		}

		lines := swarmactivity.CoalesceToolCallsForTest(events, defaultVisible())

		Expect(lines).To(HaveLen(1),
			"expected one coalesced line across providers when SwarmEvent.ID matches")
		Expect(lines[0]).To(ContainSubstring("completed"))
		Expect(lines[0]).NotTo(ContainSubstring("started"),
			"coalesced line must reflect the result, not the stale call status")
	})

	It("does not pair tool_call and tool_result when their internal ids differ", func() {
		events := []streaming.SwarmEvent{
			{
				ID:       "fs_call_A",
				Type:     streaming.EventToolCall,
				Status:   "running",
				AgentID:  "tool-agent",
				Metadata: map[string]interface{}{"tool_name": "bash"},
			},
			{
				ID:       "fs_call_B",
				Type:     streaming.EventToolResult,
				Status:   "completed",
				AgentID:  "tool-agent",
				Metadata: map[string]interface{}{"content": "unrelated output"},
			},
		}

		lines := swarmactivity.CoalesceToolCallsForTest(events, defaultVisible())

		// The call renders (unpaired, running). The bare tool_result with no
		// matching call ID is dropped per coalesce rule 3.
		Expect(lines).To(HaveLen(1))
		Expect(lines[0]).To(ContainSubstring("running"))
	})
})
