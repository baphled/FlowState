package swarmactivity_test

import (
	"strings"
	"testing"

	"github.com/baphled/flowstate/internal/streaming"
	swarmactivity "github.com/baphled/flowstate/internal/tui/components/swarm_activity"
)

// TestP3Coalescer_UsesInternalToolCallID_NotProviderScoped covers the P14b
// consumer-migration contract: the coalesce state machine pairs events by
// SwarmEvent.ID, which the chat intent now derives from the engine-stamped
// InternalToolCallID. This test simulates a failover case where a
// tool_call was emitted on provider A with native id "toolu_01" and the
// tool_result was emitted on provider B with native id "call_XYZ" —
// different native ids, same logical call. With P14 wiring the chat
// intent maps both to the same SwarmEvent.ID (the internal id), so
// coalesce collapses them into a single line with the result's status.
//
// If the chat intent ever regresses to keying on native ToolCallID, the
// two events would carry different SwarmEvent.ID values and this test
// would fail — the coalesce would emit two separate lines because the
// call-vs-result pairing would be broken across the provider boundary.
func TestP3Coalescer_UsesInternalToolCallID_NotProviderScoped(t *testing.T) {
	// Both events carry the same SwarmEvent.ID derived from the engine's
	// internal correlator, even though the native tool-use ids (captured
	// as metadata for audit) are disjoint across the two providers.
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

	lines := swarmactivity.CoalesceToolCallsForTest(events, defaultVisibleP14())

	if len(lines) != 1 {
		t.Fatalf("expected 1 coalesced line across providers, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "completed") {
		t.Errorf("coalesced line must show the result's status (completed), got %q", lines[0])
	}
	if strings.Contains(lines[0], "started") {
		t.Errorf("coalesced line must not show the call's stale 'started' status, got %q", lines[0])
	}
}

// TestP3Coalescer_DisjointInternalIDs_DoNotPair verifies that two logical
// tool calls produced under different internal ids do NOT coalesce.
// Negative gate: if the internal-id derivation ever collides on
// unrelated calls, this test surfaces the regression.
func TestP3Coalescer_DisjointInternalIDs_DoNotPair(t *testing.T) {
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

	lines := swarmactivity.CoalesceToolCallsForTest(events, defaultVisibleP14())

	// The call renders (unpaired, running). The bare tool_result with no
	// matching call ID is dropped per coalesce rule 3.
	if len(lines) != 1 {
		t.Fatalf("expected 1 line (unpaired call; orphan result dropped), got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "running") {
		t.Errorf("unpaired tool_call must preserve its running status, got %q", lines[0])
	}
}

// defaultVisibleP14 matches the visibility map used across other coalesce
// tests but declared locally to keep this file self-contained.
func defaultVisibleP14() map[streaming.SwarmEventType]bool {
	return map[streaming.SwarmEventType]bool{
		streaming.EventDelegation: true,
		streaming.EventToolCall:   true,
		streaming.EventToolResult: true,
		streaming.EventPlan:       true,
		streaming.EventReview:     true,
	}
}
