package swarmactivity_test

import (
	"strings"
	"testing"

	"github.com/baphled/flowstate/internal/streaming"
	swarmactivity "github.com/baphled/flowstate/internal/tui/components/swarm_activity"
)

// TestActivityPane_PlaceholdersBeforeFirstLoad verifies that a freshly-built
// pane shows placeholders while no caller has yet asserted a loaded state.
func TestActivityPane_PlaceholdersBeforeFirstLoad(t *testing.T) {
	pane := swarmactivity.NewSwarmActivityPane()

	output := pane.Render(80, 10)
	if !strings.Contains(output, "Delegation") && !strings.Contains(output, "Plan: Wave 2") {
		t.Errorf("fresh pane (no WithEvents call) must render placeholders, got:\n%s", output)
	}
}

// TestActivityPane_HasSeenRealEvents_StopsPlaceholders verifies that once a
// caller has supplied a non-nil events slice (even empty), the pane never
// reverts to placeholder items on subsequent renders.
func TestActivityPane_HasSeenRealEvents_StopsPlaceholders(t *testing.T) {
	pane := swarmactivity.NewSwarmActivityPane()

	// Explicitly assert "I've loaded state; it's genuinely empty" by passing
	// an empty but non-nil slice. The pane must never revert to placeholders.
	pane.WithEvents([]streaming.SwarmEvent{})

	output := pane.Render(80, 10)

	if strings.Contains(output, "Plan: Wave 2") || strings.Contains(output, "Delegation → senior-engineer") {
		t.Errorf("after WithEvents(empty), placeholder items must not appear, got:\n%s", output)
	}
}

// TestActivityPane_HasSeenRealEvents_ShowsEmptyStateText verifies that the
// post-load empty state surfaces an explicit "No activity yet" message
// instead of placeholders.
func TestActivityPane_HasSeenRealEvents_ShowsEmptyStateText(t *testing.T) {
	pane := swarmactivity.NewSwarmActivityPane()

	pane.WithEvents([]streaming.SwarmEvent{})
	output := pane.Render(80, 10)

	if !strings.Contains(output, "No activity yet") {
		t.Errorf("post-load empty pane must show \"No activity yet\", got:\n%s", output)
	}
}

// TestActivityPane_HasSeenRealEvents_PersistsAcrossEmptyCalls verifies that a
// subsequent WithEvents(empty) call keeps the loaded flag set — it does not
// revert to placeholders after the first load was observed.
func TestActivityPane_HasSeenRealEvents_PersistsAcrossEmptyCalls(t *testing.T) {
	pane := swarmactivity.NewSwarmActivityPane()

	// First load — a real event.
	pane.WithEvents([]streaming.SwarmEvent{
		{ID: "1", Type: streaming.EventToolCall, Status: "started", AgentID: "a"},
	})
	_ = pane.Render(80, 10)

	// Subsequent empty call (e.g. a Clear() then a snapshot).
	pane.WithEvents([]streaming.SwarmEvent{})
	output := pane.Render(80, 10)

	if strings.Contains(output, "Plan: Wave 2") {
		t.Errorf("pane must not fall back to placeholders after first load, got:\n%s", output)
	}
	if !strings.Contains(output, "No activity yet") {
		t.Errorf("pane must show empty-state text, got:\n%s", output)
	}
}

// TestActivityPane_NilEventsDoesNotFlipLoadedFlag verifies that the pane
// distinguishes nil (not yet loaded) from empty-but-non-nil (loaded with zero
// events). A nil call preserves placeholder-mode semantics.
func TestActivityPane_NilEventsDoesNotFlipLoadedFlag(t *testing.T) {
	pane := swarmactivity.NewSwarmActivityPane()

	pane.WithEvents(nil)
	output := pane.Render(80, 10)

	// nil is treated as "no caller has asserted a loaded state yet", so
	// placeholders remain visible.
	if !strings.Contains(output, "Plan: Wave 2") && !strings.Contains(output, "Delegation") {
		t.Errorf("WithEvents(nil) must preserve placeholder mode, got:\n%s", output)
	}
}

// TestActivityPane_CoalescesToolCallInFullRender is an integration test that
// verifies the Render pipeline applies the coalesce step: a tool_call + a
// matching tool_result produce a single line whose status reflects the
// result.
func TestActivityPane_CoalescesToolCallInFullRender(t *testing.T) {
	pane := swarmactivity.NewSwarmActivityPane()
	events := []streaming.SwarmEvent{
		{
			ID:      "toolu_01RENDER",
			Type:    streaming.EventToolCall,
			Status:  "started",
			AgentID: "tool-agent",
			Metadata: map[string]interface{}{
				"tool_name": "read_file",
			},
		},
		{
			ID:      "toolu_01RENDER",
			Type:    streaming.EventToolResult,
			Status:  "completed",
			AgentID: "tool-agent",
		},
	}

	output := pane.WithEvents(events).Render(80, 20)

	// Count body bullet lines.
	lines := strings.Split(output, "\n")
	bulletCount := 0
	for _, line := range lines {
		if strings.Contains(line, "▸") {
			bulletCount++
		}
	}
	if bulletCount != 1 {
		t.Errorf("expected exactly 1 coalesced body line, got %d. output:\n%s", bulletCount, output)
	}
	if !strings.Contains(output, "completed") {
		t.Errorf("coalesced line must show completed status, got:\n%s", output)
	}
}
