package swarmactivity_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/streaming"
	swarmactivity "github.com/baphled/flowstate/internal/tui/components/swarm_activity"
)

// SwarmActivityPane state-tracking tests cover the "have I seen real events
// yet?" flag the pane uses to decide whether to draw placeholders or empty
// state. The contract is:
//   - fresh pane (no WithEvents call): placeholders.
//   - WithEvents(non-nil empty slice): loaded; show "No activity yet".
//   - WithEvents(nil): does NOT flip the flag — placeholders remain.
//   - The loaded flag persists across subsequent empty calls.
//
// They also verify that a tool_call + tool_result coalesce into a single line
// when piped through the pane's full Render path.
var _ = Describe("SwarmActivityPane state tracking", func() {
	It("renders placeholders before any caller has loaded events", func() {
		pane := swarmactivity.NewSwarmActivityPane()
		output := pane.Render(80, 10)
		Expect(output).To(Or(ContainSubstring("Delegation"), ContainSubstring("Plan: Wave 2")),
			"fresh pane (no WithEvents call) must render placeholders")
	})

	It("stops rendering placeholders after WithEvents(empty)", func() {
		pane := swarmactivity.NewSwarmActivityPane()
		pane.WithEvents([]streaming.SwarmEvent{})

		output := pane.Render(80, 10)
		Expect(output).NotTo(ContainSubstring("Plan: Wave 2"))
		Expect(output).NotTo(ContainSubstring("Delegation → senior-engineer"))
	})

	It("shows 'No activity yet' after WithEvents(empty)", func() {
		pane := swarmactivity.NewSwarmActivityPane()
		pane.WithEvents([]streaming.SwarmEvent{})

		Expect(pane.Render(80, 10)).To(ContainSubstring("No activity yet"))
	})

	It("keeps the loaded flag set after a real load followed by an empty call", func() {
		pane := swarmactivity.NewSwarmActivityPane()
		pane.WithEvents([]streaming.SwarmEvent{
			{ID: "1", Type: streaming.EventToolCall, Status: "started", AgentID: "a"},
		})
		_ = pane.Render(80, 10)

		pane.WithEvents([]streaming.SwarmEvent{})
		output := pane.Render(80, 10)
		Expect(output).NotTo(ContainSubstring("Plan: Wave 2"),
			"pane must not fall back to placeholders after first load")
		Expect(output).To(ContainSubstring("No activity yet"))
	})

	It("treats WithEvents(nil) as 'not yet loaded' and keeps placeholders", func() {
		pane := swarmactivity.NewSwarmActivityPane()
		pane.WithEvents(nil)

		output := pane.Render(80, 10)
		Expect(output).To(Or(ContainSubstring("Plan: Wave 2"), ContainSubstring("Delegation")),
			"WithEvents(nil) must preserve placeholder mode")
	})

	It("coalesces a tool_call + matching tool_result into a single rendered bullet", func() {
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

		// Count body bullet lines (▸ marks each rendered activity row).
		bulletCount := strings.Count(output, "▸")
		Expect(bulletCount).To(Equal(1),
			"expected exactly 1 coalesced body line, got %d. output:\n%s", bulletCount, output)
		Expect(output).To(ContainSubstring("completed"))
	})
})
