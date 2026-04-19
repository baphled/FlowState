package chat_test

import (
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

// ansiEscapeViewWidth strips SGR escape sequences before measuring rendered
// row widths — lipgloss.Width already handles ANSI, but we also use a raw
// regexp when slicing byte ranges for human-readable debug output.
var ansiEscapeViewWidth = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// Behaviour-Pinned: ADR Chat Swarm Dual-Pane Layout, "Required width test
// matrix" — Primary + 1 + Secondary == W for all W >= 80. The contract is
// asserted in the ScreenLayout spec for the layout helper in isolation; this
// spec drives the same arithmetic end-to-end through chat.Intent.View and
// additionally guards the global invariant:
//
//	for every rendered row r, lipgloss.Width(r) <= W
//
// If any rendered row exceeds W, the terminal wraps it to the next display
// row, shifting the dual-pane vertical separator column off-grid on the
// wrapped continuation. The visible effect is an Activity Timeline pane
// that appears to "shrink" — the first few rows render at the expected
// 30% width, then the wrapped continuations of the overflowing rows bleed
// into the pane's column from the left. The only tests currently covering
// this path assert primary-column width <= primary, which is a noop
// (primaryColumn truncates), so the overflow slips through undetected.
//
// The canonical reproducer is the status-bar hint suffix (chatHintSuffix),
// which is 157 cells wide — wider than every dual-pane width in the ADR
// matrix up to 156. A second reproducer is the footer separator, which
// buildFooterParts hardcodes to strings.Repeat("─", 100) regardless of
// TerminalInfo.Width.
var _ = Describe("Intent.View total row width invariant", func() {
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

	// Matrix covers the ADR dual-pane widths plus one below-threshold entry
	// to prove the invariant holds in single-pane fallback too.
	DescribeTable("no rendered row exceeds the terminal width",
		func(width int) {
			intent.Update(tea.WindowSizeMsg{Width: width, Height: 40})

			view := intent.View()
			lines := strings.Split(view, "\n")

			for i, line := range lines {
				stripped := ansiEscapeViewWidth.ReplaceAllString(line, "")
				Expect(lipgloss.Width(line)).To(BeNumerically("<=", width),
					"row %d exceeds terminal width (got %d cells, want <= %d) — overflowing rows wrap in the terminal and break dual-pane column alignment; stripped=%q",
					i, lipgloss.Width(line), width, stripped)
			}
		},
		Entry("W=80 (dual-pane minimum)", 80),
		Entry("W=100 (canonical ADR width)", 100),
		Entry("W=120 (wide ADR width)", 120),
		Entry("W=140", 140),
		Entry("W=79 (single-pane fallback)", 79),
	)
})

// Behaviour-Pinned: the footer help text must not force the composed view
// wider than the terminal. The status-bar hint suffix in chat.Intent is a
// fixed 157-cell string — when the terminal is narrower than the hint, it
// has to be truncated or wrapped somewhere; leaving it intact inflates the
// whole render and cascades into the dual-pane column misalignment covered
// above. This spec pins that the help text (as rendered on its own row in
// the footer) never exceeds the terminal width.
var _ = Describe("Intent.View footer help text width", func() {
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

	DescribeTable("all help rows fit within the terminal width and keep Ctrl hints",
		func(width int) {
			intent.Update(tea.WindowSizeMsg{Width: width, Height: 40})

			view := intent.View()
			stripped := ansiEscapeViewWidth.ReplaceAllString(view, "")

			// Every wrap-split hint token ("Ctrl+C", "Ctrl+T", "quit") must
			// appear somewhere in the stripped view — the help text must be
			// wrapped, not truncated, so narrow terminals keep every Ctrl
			// hint the chat intent advertises. A single substring match on
			// the full stripped buffer is the right assertion because
			// word-wrap will split on spaces but the token itself is
			// contiguous in the buffer (a newline separates rows, not
			// letters within a token).
			Expect(stripped).To(ContainSubstring("Ctrl+C"),
				"'Ctrl+C' hint must survive wrap at W=%d — truncation drops critical quit info", width)
			Expect(stripped).To(ContainSubstring("quit"),
				"'quit' hint must survive wrap at W=%d — the last help token cannot be dropped", width)
			Expect(stripped).To(ContainSubstring("Ctrl+T"),
				"'Ctrl+T' hint must survive wrap at W=%d — swarm-activity-toggle test depends on this", width)

			// And every row of the rendered view must fit within W cells.
			lines := strings.Split(view, "\n")
			for i, line := range lines {
				Expect(lipgloss.Width(line)).To(BeNumerically("<=", width),
					"footer wrap preserved information but row %d still overflows W=%d (got %d cells)",
					i, width, lipgloss.Width(line))
			}
		},
		Entry("W=80", 80),
		Entry("W=100", 100),
		Entry("W=120", 120),
		Entry("W=140", 140),
	)
})

// Behaviour-Pinned: the footer separator rendered by ScreenLayout must be
// sized to the terminal width, not a hardcoded 100-cell line. When the
// separator is wider than the terminal, it pads the whole view wider than
// W; when it is narrower, it visually shortens the horizontal divider and
// breaks the bottom-pinned boundary between the content band and footer.
// Both manifest as the activity-pane column looking wrong on the right
// edge of the screen.
var _ = Describe("Intent.View footer separator width", func() {
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

	DescribeTable("separator row width matches terminal width",
		func(width int) {
			intent.Update(tea.WindowSizeMsg{Width: width, Height: 40})

			view := intent.View()
			stripped := ansiEscapeViewWidth.ReplaceAllString(view, "")
			lines := strings.Split(stripped, "\n")

			var sepRow string
			for _, line := range lines {
				// The footer separator is a run of box-drawing "─"; match a
				// row that is effectively all separator glyphs (ignoring
				// trailing spaces introduced by vertical padding in
				// JoinVertical).
				trimmed := strings.TrimRight(line, " ")
				if trimmed != "" && strings.Count(trimmed, "─") == lipgloss.Width(trimmed) {
					sepRow = line
					break
				}
			}
			Expect(sepRow).NotTo(BeEmpty(), "footer separator row must render")

			trimmed := strings.TrimRight(sepRow, " ")
			// The separator must not exceed terminal width, and the meaningful
			// glyph run must match terminal width so the divider spans the
			// full render.
			Expect(lipgloss.Width(sepRow)).To(BeNumerically("<=", width),
				"separator row width %d exceeds terminal width %d",
				lipgloss.Width(sepRow), width)
			Expect(lipgloss.Width(trimmed)).To(Equal(width),
				"separator glyph-run width %d must match terminal width %d",
				lipgloss.Width(trimmed), width)
		},
		Entry("W=80", 80),
		Entry("W=100", 100),
		Entry("W=120", 120),
		Entry("W=140", 140),
		Entry("W=160", 160),
	)
})
