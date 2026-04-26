package swarmactivity_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/streaming"
	swarmactivity "github.com/baphled/flowstate/internal/tui/components/swarm_activity"
)

// SwarmActivityPane profile-label tests cover the P11 contract:
//   - WithProfileName(non-empty) renders the label as a dedicated line in
//     the pane output, even at narrow secondary-pane widths.
//   - WithProfileName("") suppresses the label entirely; the only allowed
//     bracketed line is the filter-indicator "[D]…" that may appear when
//     event types are hidden.
var _ = Describe("SwarmActivityPane profile label", func() {
	It("renders the label line when WithProfileName is non-empty", func() {
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

		Expect(pane.Render(36, 10)).To(ContainSubstring("Tool calls only"))
	})

	It("suppresses the label line when WithProfileName is empty", func() {
		pane := swarmactivity.NewSwarmActivityPane().
			WithEvents([]streaming.SwarmEvent{
				{ID: "d1", Type: streaming.EventDelegation, Status: "started", AgentID: "qa"},
			}).
			WithProfileName("")

		output := pane.Render(60, 10)
		// Filter indicator starts with "[D]" — we intentionally do not
		// suppress that. Any other bracketed line starting with "[" is
		// suspicious when no profile is set.
		for _, line := range strings.Split(output, "\n") {
			if strings.HasPrefix(line, "[") && !strings.HasPrefix(line, "[D]") {
				Fail("unexpected bracketed label line with empty profile name: " + line)
			}
		}
	})
})
