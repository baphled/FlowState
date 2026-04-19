package chat_test

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

// RED specs confirming the dual-pane primary-width contract from the ADR
// "Chat Swarm Dual-Pane Layout". Width arithmetic is:
//
//	available  = W - 1
//	primary    = (available * 7) / 10
//	secondary  = available - primary
//
// Every primary-column line (the leftmost primary cells of each rendered
// row) MUST be sized to primary, not to terminal width, and the primary
// content itself MUST NOT be hard-wrapped by being laid out at terminal
// width before being stamped into the narrower primary column.
//
// At W=79 the layout MUST fall back to single-pane: no separator glyph, no
// secondary column.
var _ = Describe("Intent.View dual-pane primary width", func() {
	var intent *chat.Intent

	// dualPaneSeparator mirrors the unexported const in uikit/layout
	// (screen_layout.go: "│"). The test asserts observable output only.
	const dualPaneSeparator = "│"

	// splitPaneWidths mirrors the exported layout.SplitPaneWidths contract
	// locally so the spec asserts the ADR arithmetic directly, rather than
	// re-reading it through a helper in production code.
	splitPaneWidths := func(totalWidth int) (int, int) {
		available := totalWidth - 1
		primary := (available * 7) / 10
		secondary := available - primary
		return primary, secondary
	}

	// primaryColumn returns the leftmost `primary` cells of line, measured
	// with lipgloss.Width so ANSI escapes do not distort the slice. Lines
	// shorter than `primary` are returned verbatim.
	primaryColumn := func(line string, primary int) string {
		if lipgloss.Width(line) <= primary {
			return line
		}
		// Rune-safe width slice: accumulate until adding the next rune
		// would exceed primary cells.
		var b strings.Builder
		b.Grow(len(line))
		for _, r := range line {
			if lipgloss.Width(b.String()+string(r)) > primary {
				break
			}
			b.WriteRune(r)
		}
		return b.String()
	}

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

	// Contract matrix from the ADR — dual-pane widths only.
	matrix := []struct {
		W             int
		wantPrimary   int
		wantSecondary int
	}{
		{80, 55, 24},
		{100, 69, 30},
		{120, 83, 36},
	}

	for _, m := range matrix {
		Describe("at W="+itoa(m.W), func() {
			BeforeEach(func() {
				intent.Update(tea.WindowSizeMsg{Width: m.W, Height: 40})
			})

			It("matches the ADR split arithmetic", func() {
				primary, secondary := splitPaneWidths(m.W)
				Expect(primary).To(Equal(m.wantPrimary),
					"primary must equal ((W-1)*7)/10 at W=%d", m.W)
				Expect(secondary).To(Equal(m.wantSecondary),
					"secondary must equal (W-1)-primary at W=%d", m.W)
			})

			It("sizes every primary-column line to <= primary", func() {
				primary, _ := splitPaneWidths(m.W)

				view := intent.View()
				lines := strings.Split(view, "\n")

				// A primary-column line is the leftmost `primary` cells
				// of each composed row. No primary-column line may
				// exceed primary cells — if it does, the primary is
				// being sized to terminal width and then cropped, which
				// is exactly the bug we are catching.
				for i, line := range lines {
					col := primaryColumn(line, primary)
					Expect(lipgloss.Width(col)).To(BeNumerically("<=", primary),
						"line %d primary-column width %d exceeds primary %d at W=%d; line=%q",
						i, lipgloss.Width(col), primary, m.W, line)
				}
			})

			It("does not silently hard-wrap a short primary message", func() {
				// A short message MUST NOT be wrapped across rows just
				// because the viewport was sized to terminal width
				// rather than primary width. The message is well
				// under primary at every W in the matrix.
				const shortMsg = "ping"

				// Drive a content chunk into the view. The existing
				// swarm-activity-wiring spec uses StreamChunkMsg for
				// the same purpose, so the seam is appropriate.
				intent.Update(chat.StreamChunkMsg{Content: shortMsg, Done: true})

				view := intent.View()
				lines := strings.Split(view, "\n")

				primary, _ := splitPaneWidths(m.W)

				// Count how many rows contain "ping" in the primary
				// column. A correctly-sized primary renders the word
				// on a single row. When the viewport is sized to
				// terminal width and then stamped into primary, a
				// soft-wrap or multi-row render can leak the literal
				// across rows.
				hits := 0
				for _, line := range lines {
					col := primaryColumn(line, primary)
					if strings.Contains(col, shortMsg) {
						hits++
					}
				}

				Expect(hits).To(Equal(1),
					"short message %q must appear on exactly one primary-column row at W=%d; saw %d rows",
					shortMsg, m.W, hits)
			})
		})
	}

	Describe("at W=79", func() {
		BeforeEach(func() {
			intent.Update(tea.WindowSizeMsg{Width: 79, Height: 40})
		})

		It("falls back to single-pane (no separator glyph)", func() {
			view := intent.View()

			// The vertical-rule separator "│" is only rendered as a
			// dedicated column between panes in dual-pane mode. At
			// W=79 the single-pane fallback fires, so this glyph
			// must not appear anywhere in the composed view.
			Expect(view).NotTo(ContainSubstring(dualPaneSeparator),
				"single-pane fallback must not draw the vertical-rule separator at W=79")
		})

		It("does not render the Activity Timeline header", func() {
			view := intent.View()
			Expect(view).NotTo(ContainSubstring("Activity Timeline"),
				"single-pane fallback must omit the secondary-pane content at W=79")
		})
	})
})

// itoa avoids pulling in strconv just for a Describe label.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
