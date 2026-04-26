package swarmactivity_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/streaming"
	swarmactivity "github.com/baphled/flowstate/internal/tui/components/swarm_activity"
)

// defaultVisible returns a visibility map with all event types enabled.
// It is also reused by sibling test files (p14_internal_id_test.go) to keep
// the "all visible" case in a single place.
func defaultVisible() map[streaming.SwarmEventType]bool {
	return map[streaming.SwarmEventType]bool{
		streaming.EventDelegation: true,
		streaming.EventToolCall:   true,
		streaming.EventToolResult: true,
		streaming.EventPlan:       true,
		streaming.EventReview:     true,
	}
}

// CoalesceToolCalls tests verify the state machine that takes a flat
// SwarmEvent slice and produces the human-readable lines rendered in the
// activity pane. Behaviours covered:
//   - tool_call + matching tool_result collapse to one line whose status
//     reflects the result (completed, error, etc.) — never the stale
//     "started" status.
//   - an unpaired tool_call still renders, showing its in-flight status.
//   - an orphan tool_result (no matching call) is dropped — never rendered.
//   - non-tool events (delegation, plan, review) pass through unchanged.
//   - parallel tool calls coalesce independently by ID.
//   - the visibility filter hides events whose type is set to false.
//   - eviction-edge cases: surviving call w/ evicted result shows in-flight,
//     surviving result w/ evicted call is suppressed (no panic, no ghost
//     line).
var _ = Describe("CoalesceToolCalls", func() {
	It("pairs a started tool_call with its completed tool_result into a single line", func() {
		events := []streaming.SwarmEvent{
			{
				ID:      "toolu_01PAIR",
				Type:    streaming.EventToolCall,
				Status:  "started",
				AgentID: "tool-agent",
				Metadata: map[string]interface{}{
					"tool_name": "read_file",
				},
			},
			{
				ID:      "toolu_01PAIR",
				Type:    streaming.EventToolResult,
				Status:  "completed",
				AgentID: "tool-agent",
				Metadata: map[string]interface{}{
					"content": "output",
				},
			},
		}

		lines := swarmactivity.CoalesceToolCallsForTest(events, defaultVisible())

		Expect(lines).To(HaveLen(1))
		Expect(lines[0]).To(ContainSubstring("Tool Call"))
		Expect(lines[0]).To(ContainSubstring("completed"))
		Expect(lines[0]).NotTo(ContainSubstring("started"),
			"coalesced line must not show the stale started status once the result is in")
	})

	It("renders an unpaired tool_call with its current running status", func() {
		events := []streaming.SwarmEvent{
			{
				ID:      "toolu_01LONE",
				Type:    streaming.EventToolCall,
				Status:  "running",
				AgentID: "tool-agent",
				Metadata: map[string]interface{}{
					"tool_name": "slow_tool",
				},
			},
		}

		lines := swarmactivity.CoalesceToolCallsForTest(events, defaultVisible())

		Expect(lines).To(HaveLen(1))
		Expect(lines[0]).To(ContainSubstring("running"))
	})

	It("reflects an error tool_result on the coalesced line", func() {
		events := []streaming.SwarmEvent{
			{
				ID:      "toolu_01ERR",
				Type:    streaming.EventToolCall,
				Status:  "started",
				AgentID: "tool-agent",
				Metadata: map[string]interface{}{
					"tool_name": "bash",
				},
			},
			{
				ID:      "toolu_01ERR",
				Type:    streaming.EventToolResult,
				Status:  "error",
				AgentID: "tool-agent",
				Metadata: map[string]interface{}{
					"content":  "boom",
					"is_error": true,
				},
			},
		}

		lines := swarmactivity.CoalesceToolCallsForTest(events, defaultVisible())

		Expect(lines).To(HaveLen(1))
		Expect(lines[0]).To(Or(ContainSubstring("error"), ContainSubstring("failed")))
	})

	It("drops an orphan tool_result with no matching tool_call", func() {
		events := []streaming.SwarmEvent{
			{
				ID:      "toolu_01ORPHAN",
				Type:    streaming.EventToolResult,
				Status:  "completed",
				AgentID: "tool-agent",
			},
		}

		lines := swarmactivity.CoalesceToolCallsForTest(events, defaultVisible())

		Expect(lines).To(BeEmpty(),
			"orphan tool_result must not render its own line")
	})

	It("preserves non-tool events (delegation, plan, review) unchanged", func() {
		events := []streaming.SwarmEvent{
			{ID: "d1", Type: streaming.EventDelegation, Status: "started", AgentID: "qa"},
			{ID: "p1", Type: streaming.EventPlan, Status: "completed", AgentID: "planner"},
			{ID: "r1", Type: streaming.EventReview, Status: "completed", AgentID: "reviewer"},
		}

		lines := swarmactivity.CoalesceToolCallsForTest(events, defaultVisible())

		Expect(lines).To(HaveLen(3))
		Expect(lines[0]).To(ContainSubstring("Delegation"))
		Expect(lines[1]).To(ContainSubstring("Plan"))
		Expect(lines[2]).To(ContainSubstring("Review"))
	})

	It("coalesces two parallel tool calls independently by ID", func() {
		events := []streaming.SwarmEvent{
			{ID: "a", Type: streaming.EventToolCall, Status: "started", AgentID: "ag"},
			{ID: "b", Type: streaming.EventToolCall, Status: "started", AgentID: "ag"},
			{ID: "b", Type: streaming.EventToolResult, Status: "completed", AgentID: "ag"},
			{ID: "a", Type: streaming.EventToolResult, Status: "error", AgentID: "ag"},
		}

		lines := swarmactivity.CoalesceToolCallsForTest(events, defaultVisible())

		Expect(lines).To(HaveLen(2))
		// Order preserved by the tool_call ordering: a first, then b.
		Expect(lines[0]).To(ContainSubstring("error"))
		Expect(lines[1]).To(ContainSubstring("completed"))
	})

	It("respects the visibility filter and drops hidden event types", func() {
		events := []streaming.SwarmEvent{
			{ID: "d1", Type: streaming.EventDelegation, Status: "started", AgentID: "a"},
			{ID: "t1", Type: streaming.EventToolCall, Status: "started", AgentID: "a"},
		}
		vis := map[streaming.SwarmEventType]bool{
			streaming.EventDelegation: false,
			streaming.EventToolCall:   true,
			streaming.EventToolResult: true,
			streaming.EventPlan:       true,
			streaming.EventReview:     true,
		}

		lines := swarmactivity.CoalesceToolCallsForTest(events, vis)

		Expect(lines).To(HaveLen(1))
		Expect(lines[0]).To(ContainSubstring("Tool Call"))
	})

	It("renders post-eviction surviving events under a visibility filter (no gaps, no stale lines)", func() {
		// Simulate a pane whose store has already evicted older events; the
		// caller now also hides EventPlan. The pane must render every
		// surviving non-plan event in order.
		surviving := []streaming.SwarmEvent{
			{ID: "d1", Type: streaming.EventDelegation, Status: "started", AgentID: "qa"},
			{ID: "p1", Type: streaming.EventPlan, Status: "done", AgentID: "planner"},
			{ID: "d2", Type: streaming.EventDelegation, Status: "completed", AgentID: "qa"},
		}
		vis := map[streaming.SwarmEventType]bool{
			streaming.EventDelegation: true,
			streaming.EventToolCall:   true,
			streaming.EventToolResult: true,
			streaming.EventPlan:       false, // hidden
			streaming.EventReview:     true,
		}

		lines := swarmactivity.CoalesceToolCallsForTest(surviving, vis)

		Expect(lines).To(HaveLen(2))
		Expect(lines[0]).To(ContainSubstring("Delegation"))
		Expect(lines[0]).To(ContainSubstring("started"))
		Expect(lines[1]).To(ContainSubstring("Delegation"))
		Expect(lines[1]).To(ContainSubstring("completed"))
		for _, line := range lines {
			Expect(line).NotTo(ContainSubstring("Plan"),
				"plan lines must be hidden by the visibility filter")
		}
	})

	It("suppresses an orphan tool_result when its tool_call was evicted (no panic)", func() {
		events := []streaming.SwarmEvent{
			{
				ID:      "toolu_01SURVIVED",
				Type:    streaming.EventToolResult,
				Status:  "completed",
				AgentID: "tool-agent",
				Metadata: map[string]interface{}{
					"content": "output body",
				},
			},
			// A normal delegation follows so we can confirm order is preserved.
			{ID: "d1", Type: streaming.EventDelegation, Status: "completed", AgentID: "qa"},
		}

		lines := swarmactivity.CoalesceToolCallsForTest(events, defaultVisible())

		Expect(lines).To(HaveLen(1))
		Expect(lines[0]).To(ContainSubstring("Delegation"))
	})

	It("shows a surviving tool_call with its in-flight status when its tool_result was evicted", func() {
		events := []streaming.SwarmEvent{
			{
				ID:      "toolu_01LONELY",
				Type:    streaming.EventToolCall,
				Status:  "running",
				AgentID: "tool-agent",
				Metadata: map[string]interface{}{
					"tool_name": "slow_tool",
				},
			},
		}

		lines := swarmactivity.CoalesceToolCallsForTest(events, defaultVisible())

		Expect(lines).To(HaveLen(1))
		Expect(lines[0]).To(ContainSubstring("running"))
		Expect(lines[0]).NotTo(ContainSubstring("completed"),
			"surviving call must not fabricate a completed status")
	})
})
