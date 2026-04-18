package swarmactivity_test

import (
	"strings"
	"testing"

	"github.com/baphled/flowstate/internal/streaming"
	swarmactivity "github.com/baphled/flowstate/internal/tui/components/swarm_activity"
)

// TestSwarmActivityPane_WithProfileName_RendersLabel asserts that setting a
// non-empty profile name via WithProfileName surfaces the label as a
// dedicated line in the rendered pane output (P11). A narrow secondary-pane
// width is used so the test catches any regression that tries to shove the
// label onto the header line.
func TestSwarmActivityPane_WithProfileName_RendersLabel(t *testing.T) {
	pane := swarmactivity.NewSwarmActivityPane().
		WithEvents([]streaming.SwarmEvent{
			{ID: "t1", Type: streaming.EventToolCall, Status: "started", AgentID: "t"},
		}).
		WithVisibleTypes(map[streaming.SwarmEventType]bool{
			streaming.EventDelegation: false,
			streaming.EventToolCall:   true,
			streaming.EventToolResult: true,
			streaming.EventPlan:       false,
			streaming.EventReview:     false,
		}).
		WithProfileName("Tool calls only")

	output := pane.Render(36, 10)
	if !strings.Contains(output, "Tool calls only") {
		t.Errorf("expected profile label in rendered pane, got:\n%s", output)
	}
}

// TestSwarmActivityPane_WithProfileName_EmptyOmitsLabel asserts that an
// empty profile name suppresses the label line entirely, preserving the
// pre-P11 rendering for the default profile.
func TestSwarmActivityPane_WithProfileName_EmptyOmitsLabel(t *testing.T) {
	pane := swarmactivity.NewSwarmActivityPane().
		WithEvents([]streaming.SwarmEvent{
			{ID: "d1", Type: streaming.EventDelegation, Status: "started", AgentID: "qa"},
		}).
		WithProfileName("")

	output := pane.Render(60, 10)
	// The pane should not contain bracketed-label syntax reserved for the
	// profile tag when no name is set. We look for the leading bracket in
	// a context that can't confuse with the filter indicator line, which
	// never renders for profileAll (no types hidden).
	for _, line := range strings.Split(output, "\n") {
		// Filter indicator starts with "[D]" — we intentionally do not
		// suppress that. Any other bracketed line starting with "[" is
		// suspicious when no profile is set.
		if strings.HasPrefix(line, "[") && !strings.HasPrefix(line, "[D]") {
			t.Errorf("unexpected bracketed label line with empty profile name: %q", line)
		}
	}
}
