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
	if !strings.Contains(lines[0], "Tool Call") {
		t.Errorf("expected line to start with Tool Call (human label), got %q", lines[0])
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
	if !strings.Contains(lines[0], "Delegation") {
		t.Errorf("expected first line to be Delegation (human label), got %q", lines[0])
	}
	if !strings.Contains(lines[1], "Plan") {
		t.Errorf("expected second line to be Plan (human label), got %q", lines[1])
	}
	if !strings.Contains(lines[2], "Review") {
		t.Errorf("expected third line to be Review (human label), got %q", lines[2])
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
	if !strings.Contains(lines[0], "Tool Call") {
		t.Errorf("expected remaining line to be Tool Call (human label), got %q", lines[0])
	}
}

// TestCoalesceToolCalls_EvictionWithFilter_ShowsRecentVisibleEvents verifies
// that when the caller hands the pane a post-eviction slice (the store has
// already trimmed the oldest events) and a visibility filter hides a type,
// the pane renders the most-recent-N-visible events rather than producing
// gaps or stale lines. This closes the F16 QA gap: ring-buffer eviction and
// filtering were each tested in isolation, but not their interaction.
func TestCoalesceToolCalls_EvictionWithFilter_ShowsRecentVisibleEvents(t *testing.T) {
	// Simulate a pane that has a small capacity (imagine the store's ring
	// buffer has already evicted the oldest). Surviving events are mixed
	// types; caller then hides EventPlan. The pane must render every
	// surviving non-plan event, in order, with no duplicates or gaps.
	surviving := []streaming.SwarmEvent{
		// These three are what remained after eviction of older events.
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

	if len(lines) != 2 {
		t.Fatalf("expected 2 surviving, visible lines (d1, d2), got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "Delegation") || !strings.Contains(lines[0], "started") {
		t.Errorf("expected first line to be d1 Delegation/started, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "Delegation") || !strings.Contains(lines[1], "completed") {
		t.Errorf("expected second line to be d2 Delegation/completed, got %q", lines[1])
	}
	// Plan event must be dropped.
	for _, line := range lines {
		if strings.Contains(line, "Plan") {
			t.Errorf("expected plan lines to be hidden, got %q", line)
		}
	}
}

// TestCoalesceToolCalls_EvictedCall_OrphanResultStillSuppressed verifies that
// when the tool_call was evicted by the ring buffer but its tool_result
// survived, the pane still behaves gracefully — the lone tool_result is
// suppressed (not rendered as a ghost "result" line), and nothing panics.
func TestCoalesceToolCalls_EvictedCall_OrphanResultStillSuppressed(t *testing.T) {
	// Only the result survived; the call was evicted.
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

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("coalesceToolCalls must not panic on an evicted tool_call; got: %v", r)
		}
	}()

	lines := swarmactivity.CoalesceToolCallsForTest(events, defaultVisible())

	if len(lines) != 1 {
		t.Fatalf("expected 1 line (orphan result dropped, delegation kept), got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "Delegation") {
		t.Errorf("expected surviving line to be the Delegation, got %q", lines[0])
	}
}

// TestCoalesceToolCalls_SurvivingCall_EvictedResult_ShowsInflightStatus
// verifies the mirror-image case: the tool_call survived but its
// tool_result was evicted. The pane must show the call with its own
// (inflight) status — no ghost "completed" line, no panic.
func TestCoalesceToolCalls_SurvivingCall_EvictedResult_ShowsInflightStatus(t *testing.T) {
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

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("coalesceToolCalls must not panic when a tool_result is evicted; got: %v", r)
		}
	}()

	lines := swarmactivity.CoalesceToolCallsForTest(events, defaultVisible())

	if len(lines) != 1 {
		t.Fatalf("expected 1 line (surviving call only), got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "running") {
		t.Errorf("expected surviving call to show its in-flight status 'running', got %q", lines[0])
	}
	// Must not fabricate a "completed" suffix.
	if strings.Contains(lines[0], "completed") {
		t.Errorf("expected surviving call not to claim completion, got %q", lines[0])
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
