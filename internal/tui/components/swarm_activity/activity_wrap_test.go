package swarmactivity_test

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/streaming"
	swarmactivity "github.com/baphled/flowstate/internal/tui/components/swarm_activity"
)

// These specs pin the wrap-on-narrow-pane contract drawn from the
// Multi-Agent Chat UX Implementation Plan (T5 Gotcha: "Component must
// respect width/height constraints (text wrapping for narrow panes)").
//
// Prior behaviour truncated body rows with an ellipsis (e.g.
// "▸ Tool Call · test-agent · st…"), destroying the user-facing status
// and agent context. The plan is clear that narrow panes wrap — this
// spec asserts that contract at the component seam.
var _ = Describe("SwarmActivityPane narrow-pane wrapping", func() {
	var pane *swarmactivity.SwarmActivityPane

	BeforeEach(func() {
		pane = swarmactivity.NewSwarmActivityPane()
	})

	Context("at a width that cannot fit a full event line", func() {
		It("wraps body rows onto continuation lines instead of truncating them", func() {
			// A single event whose formatted line is longer than the pane
			// width. With truncation, the status would be replaced by an
			// ellipsis; with wrapping, every rune is preserved across one
			// or more continuation lines.
			events := []streaming.SwarmEvent{
				{
					Type:    streaming.EventToolCall,
					Status:  "started",
					AgentID: "test-agent",
				},
			}

			// Width 20 is tight enough to force wrapping of
			// "▸ Tool Call · test-agent · started" (34 cells).
			output := pane.WithEvents(events).Render(20, 10)

			// No body line contains the ellipsis glyph — content is
			// preserved across continuation lines, not truncated away.
			Expect(output).NotTo(ContainSubstring("…"),
				"narrow-pane body lines must wrap, not truncate with an ellipsis")

			// Collect just the body (lines after the header) so the
			// assertion is not polluted by header text. Concatenating the
			// body lines must reproduce every rune of the original event
			// row — wrapping may split mid-word, but never drops runes.
			bodyLines := strings.Split(output, "\n")[1:]
			joinedBody := strings.Join(bodyLines, "")
			Expect(joinedBody).To(ContainSubstring("test-agent"),
				"agent id runes must survive wrapping in full when rejoined")
			Expect(joinedBody).To(ContainSubstring("started"),
				"status runes must survive wrapping in full when rejoined")

			// Every rendered line still fits the declared width — wrapping
			// does not leak past the pane.
			for _, line := range strings.Split(output, "\n") {
				Expect(lipgloss.Width(line)).To(BeNumerically("<=", 20),
					"wrapped continuation lines must still fit the pane width")
			}

			// At least one continuation line must exist — otherwise the
			// pane silently dropped content rather than wrapping.
			Expect(len(bodyLines)).To(BeNumerically(">=", 2),
				"overflowing row must produce at least one continuation line")
		})

		It("does not truncate event body lines with an ellipsis", func() {
			// Regression guard for the original symptom reported by
			// operators: "▸ Tool Call · test-agent · st…". The ellipsis
			// is the bug tell; it must never appear on a body row.
			events := []streaming.SwarmEvent{
				{
					Type:    streaming.EventToolCall,
					Status:  "started",
					AgentID: "test-agent",
				},
			}

			output := pane.WithEvents(events).Render(20, 10)

			// Pull out only body lines (those carrying the ▸ bullet or
			// their wrap continuations). Header and filter lines are out
			// of scope for this wrap contract.
			var bodyText strings.Builder
			bulletSeen := false
			for _, line := range strings.Split(output, "\n") {
				if strings.Contains(line, "▸") {
					bulletSeen = true
				}
				if bulletSeen {
					bodyText.WriteString(line)
					bodyText.WriteString("\n")
				}
			}

			Expect(bodyText.String()).NotTo(ContainSubstring("…"),
				"no body line may end in an ellipsis — narrow panes wrap")
		})
	})
})
