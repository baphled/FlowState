package swarmactivity_test

import (
	"strings"
	"testing"

	"github.com/baphled/flowstate/internal/streaming"
	swarmactivity "github.com/baphled/flowstate/internal/tui/components/swarm_activity"
)

// TestCoalesceToolCalls_PairsStartedAndCompleted verifies that a tool_call
// with a matching tool_result (same ID) collapses into a single line whose
// status is derived from the result (completed).
func TestCoalesceToolCalls_PairsStartedAndCompleted(t *testing.T) {
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

	if len(lines) != 1 {
		t.Fatalf("expected 1 line (call + result collapsed), got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "tool_call") {
		t.Errorf("expected line to start with tool_call, got %q", lines[0])
	}
	if !strings.Contains(lines[0], "completed") {
		t.Errorf("expected status derived from tool_result (completed), got %q", lines[0])
	}
	if strings.Contains(lines[0], "started") {
		t.Errorf("coalesced line must not show the started status once the result is in, got %q", lines[0])
	}
}

// TestCoalesceToolCalls_UnpairedShowsRunning verifies that a tool_call with
// no matching tool_result still renders, showing the current running status.
func TestCoalesceToolCalls_UnpairedShowsRunning(t *testing.T) {
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

	if len(lines) != 1 {
		t.Fatalf("expected 1 line for unpaired tool_call, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "running") {
		t.Errorf("expected unpaired tool_call to show running status, got %q", lines[0])
	}
}

// TestCoalesceToolCalls_ErrorResult verifies that a tool_result carrying an
// error status is reflected on the coalesced line.
func TestCoalesceToolCalls_ErrorResult(t *testing.T) {
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

	if len(lines) != 1 {
		t.Fatalf("expected 1 line for call + error result, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "error") && !strings.Contains(lines[0], "failed") {
		t.Errorf("expected coalesced error line to show error/failed, got %q", lines[0])
	}
}

// TestCoalesceToolCalls_DropsOrphanToolResult verifies that a bare tool_result
// event with no matching tool_call is not rendered as its own line. The
// drill-down modal still shows it via the event store; the pane only
// coalesces on tool_call.
func TestCoalesceToolCalls_DropsOrphanToolResult(t *testing.T) {
	events := []streaming.SwarmEvent{
		{
			ID:      "toolu_01ORPHAN",
			Type:    streaming.EventToolResult,
			Status:  "completed",
			AgentID: "tool-agent",
		},
	}

	lines := swarmactivity.CoalesceToolCallsForTest(events, defaultVisible())

	if len(lines) != 0 {
		t.Fatalf("orphan tool_result must not render its own line, got %d lines: %v", len(lines), lines)
	}
}

// TestCoalesceToolCalls_PreservesNonToolEvents verifies that non-tool events
// (delegation, plan, review) pass through the coalesce layer unchanged.
func TestCoalesceToolCalls_PreservesNonToolEvents(t *testing.T) {
	events := []streaming.SwarmEvent{
		{ID: "d1", Type: streaming.EventDelegation, Status: "started", AgentID: "qa"},
		{ID: "p1", Type: streaming.EventPlan, Status: "completed", AgentID: "planner"},
		{ID: "r1", Type: streaming.EventReview, Status: "completed", AgentID: "reviewer"},
	}

	lines := swarmactivity.CoalesceToolCallsForTest(events, defaultVisible())

	if len(lines) != 3 {
		t.Fatalf("expected 3 lines for 3 non-tool events, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "delegation") {
		t.Errorf("expected first line to be delegation, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "plan") {
		t.Errorf("expected second line to be plan, got %q", lines[1])
	}
	if !strings.Contains(lines[2], "review") {
		t.Errorf("expected third line to be review, got %q", lines[2])
	}
}

// TestCoalesceToolCalls_MultipleParallelCalls verifies that two concurrent
// tool calls with distinct IDs coalesce independently.
func TestCoalesceToolCalls_MultipleParallelCalls(t *testing.T) {
	events := []streaming.SwarmEvent{
		{ID: "a", Type: streaming.EventToolCall, Status: "started", AgentID: "ag"},
		{ID: "b", Type: streaming.EventToolCall, Status: "started", AgentID: "ag"},
		{ID: "b", Type: streaming.EventToolResult, Status: "completed", AgentID: "ag"},
		{ID: "a", Type: streaming.EventToolResult, Status: "error", AgentID: "ag"},
	}

	lines := swarmactivity.CoalesceToolCallsForTest(events, defaultVisible())

	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (one per tool_call ID), got %d: %v", len(lines), lines)
	}
	// Order preserved by the tool_call ordering: a first, then b.
	if !strings.Contains(lines[0], "error") {
		t.Errorf("expected line 0 to carry a's error status, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "completed") {
		t.Errorf("expected line 1 to carry b's completed status, got %q", lines[1])
	}
}

// TestCoalesceToolCalls_RespectsVisibilityFilter verifies that events whose
// type is hidden via the visible-types map are dropped by coalesce.
func TestCoalesceToolCalls_RespectsVisibilityFilter(t *testing.T) {
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

	if len(lines) != 1 {
		t.Fatalf("expected only the visible tool_call line, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "tool_call") {
		t.Errorf("expected remaining line to be tool_call, got %q", lines[0])
	}
}

// defaultVisible returns a visibility map with all types enabled.
func defaultVisible() map[streaming.SwarmEventType]bool {
	return map[streaming.SwarmEventType]bool{
		streaming.EventDelegation: true,
		streaming.EventToolCall:   true,
		streaming.EventToolResult: true,
		streaming.EventPlan:       true,
		streaming.EventReview:     true,
	}
}
