package swarmactivity

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Minimum usable dimensions below which the pane renders nothing.
const (
	minRenderWidth  = 10
	minRenderHeight = 2
	headerText      = "Activity Timeline"
	ellipsis        = "…"
)

// defaultItems holds the placeholder timeline entries shown until T6 wires
// real swarm events. They are ordered oldest-first so overflow trims from
// the top of the body, never the header.
var defaultItems = []string{
	"▸ Delegation → senior-engineer",
	"▸ Tool: ReadFile",
	"▸ Plan: Wave 2",
	"▸ Review: ADR-3",
}

// SwarmActivityPane renders a secondary-pane timeline of swarm events.
//
// The pane is width- and height-aware: long lines are truncated with an
// ellipsis (never wrapped) and total line count is clamped to the supplied
// height. Below minimum thresholds the pane renders nothing, allowing the
// dual-pane layout in T2 to fall back to a single-pane view gracefully.
//
// The name intentionally repeats the package qualifier to distinguish it
// from other pane types in the TUI at the call site.
//
//nolint:revive // Stuttering name mandated by the multi-agent chat UX API contract.
type SwarmActivityPane struct {
	title       string
	items       []string
	headerStyle lipgloss.Style
	bodyStyle   lipgloss.Style
}

// NewSwarmActivityPane constructs a pane with placeholder timeline items.
//
// Expected:
//   - None.
//
// Returns:
//   - An initialised SwarmActivityPane ready to Render.
//
// Side effects:
//   - None.
func NewSwarmActivityPane() *SwarmActivityPane {
	return &SwarmActivityPane{
		title:       headerText,
		items:       append([]string(nil), defaultItems...),
		headerStyle: lipgloss.NewStyle().Bold(true),
		bodyStyle:   lipgloss.NewStyle(),
	}
}

// Render produces the pane's view clamped to the given width and height.
//
// Expected:
//   - width and height describe the available cells for this pane.
//
// Returns:
//   - A lipgloss-rendered string joined by newlines, or empty when the
//     pane cannot fit within minimum thresholds.
//
// Side effects:
//   - None.
func (p *SwarmActivityPane) Render(width, height int) string {
	if width < minRenderWidth || height < minRenderHeight {
		return ""
	}

	lines := make([]string, 0, height)
	lines = append(lines, p.headerStyle.Render(truncate(p.title, width)))

	body := p.bodyLines(width)
	// Drop oldest (top of body) if total lines would exceed height.
	bodyBudget := height - 1
	if len(body) > bodyBudget {
		body = body[len(body)-bodyBudget:]
	}
	lines = append(lines, body...)

	return strings.Join(lines, "\n")
}

// bodyLines returns the styled, width-truncated body lines.
//
// Expected:
//   - width is the visible cell budget per line.
//
// Returns:
//   - A slice of styled lines, one per placeholder item.
//
// Side effects:
//   - None.
func (p *SwarmActivityPane) bodyLines(width int) []string {
	out := make([]string, 0, len(p.items))
	for _, item := range p.items {
		out = append(out, p.bodyStyle.Render(truncate(item, width)))
	}
	return out
}

// truncate shortens s so that its visual width does not exceed max cells,
// appending a single-cell ellipsis when truncation occurs.
//
// Expected:
//   - limit >= minRenderWidth; callers guard smaller widths at Render entry.
//
// Returns:
//   - The original string when it already fits, otherwise a rune-safe
//     prefix with an ellipsis suffix.
//
// Side effects:
//   - None.
func truncate(s string, limit int) string {
	if lipgloss.Width(s) <= limit {
		return s
	}

	// Accumulate runes until adding the next one would push us past the
	// budget, leaving a single cell for the ellipsis glyph.
	budget := limit - lipgloss.Width(ellipsis)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		next := b.String() + string(r)
		if lipgloss.Width(next) > budget {
			break
		}
		b.WriteRune(r)
	}
	return b.String() + ellipsis
}
