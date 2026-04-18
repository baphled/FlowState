package chat_test

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
	"github.com/baphled/flowstate/internal/tui/uikit/layout"
)

// Covers P1/A2 — the activity pane must render at the 30% secondary-pane
// width, not the full terminal width. Regression was at intent.go:2031 where
// the intent passed i.width into swarmActivity.Render(i.width, contentHeight),
// so long event content would be rendered to its own full terminal width and
// then stamped into the 30% pane column, causing spill and truncation chaos.
var _ = Describe("swarm activity pane width (A2)", func() {
	var intent *chat.Intent

	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
		intent = chat.NewIntent(chat.IntentConfig{
			AgentID:      "test-agent",
			SessionID:    "test-session",
			ProviderName: "openai",
			ModelName:    "gpt-4o",
			TokenBudget:  4096,
		})
	})

	AfterEach(func() {
		chat.SetRunningInTestsForTest(false)
	})

	Describe("at a 120-column terminal with the secondary pane visible", func() {
		It("constrains rendered activity lines to the 30% secondary width", func() {
			intent.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

			// Seed a long-string event so any render using the terminal
			// width would produce wider-than-pane lines.
			longAgent := "agent-with-a-very-long-identifier-that-would-easily-exceed-the-secondary-pane"
			store := intent.SwarmStoreForTest()
			Expect(store).NotTo(BeNil())
			store.Append(streaming.SwarmEvent{
				Type:    streaming.EventDelegation,
				Status:  "started",
				AgentID: longAgent,
			})

			_, secondaryWidth := layout.SplitPaneWidths(120)
			Expect(secondaryWidth).To(BeNumerically("<=", 36),
				"precondition: 30% of (120-1) must be at most 36 columns")

			view := intent.View()

			// Find the rendered activity lines by scanning for the header
			// and checking widths of the surrounding content. The full view
			// is terminal-width (120), but the secondary-pane column must
			// render each activity line at <= secondaryWidth.
			Expect(view).To(ContainSubstring("Activity Timeline"),
				"secondary pane must be rendered when visible")

			// The activity component must not emit any line containing the
			// long agent name at a width greater than the secondary pane
			// width. If intent passed i.width (120) into Render, the long
			// agent would render unwrapped at up to 120 cols within the
			// pane's own internal line, which we forbid.
			for _, line := range splitLines(view) {
				if !containsAgent(line, longAgent) {
					continue
				}
				// Activity content lines must not span terminal width.
				Expect(lipgloss.Width(line)).To(BeNumerically("<=", secondaryWidth+8),
					"activity pane line %q wider than pane %d (with minor join tolerance)",
					line, secondaryWidth)
			}
		})
	})
})

// splitLines is a local helper — kept internal so the test file stays
// self-contained without reaching into production string utilities.
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := range len(s) {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// containsAgent returns true when line contains the given substring.
// Extracted so the intent of the assertion reads clearly.
func containsAgent(line, agent string) bool {
	return indexOf(line, agent) >= 0
}

// indexOf returns the index of substr in s, or -1 if absent.
func indexOf(s, substr string) int {
	n := len(substr)
	if n == 0 || n > len(s) {
		return -1
	}
	for i := 0; i+n <= len(s); i++ {
		if s[i:i+n] == substr {
			return i
		}
	}
	return -1
}
