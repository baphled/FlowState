package swarmactivity

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/baphled/flowstate/internal/streaming"
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
	events      []streaming.SwarmEvent
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

// WithEvents swaps the pane's body source from placeholder items to the
// supplied swarm events. Passing a nil or empty slice preserves the
// placeholder fallback so Wave 1 transition tests continue to render.
//
// Expected:
//   - events is the ordered list of events (oldest first) to render.
//
// Returns:
//   - The receiver, to support fluent builder-style configuration.
//
// Side effects:
//   - Stores a reference to the supplied slice on the receiver. Callers
//     that mutate the slice afterwards will see the change reflected on
//     the next Render; the chat intent always supplies a fresh
//     SwarmEventStore.All() copy so this is safe in practice.
func (p *SwarmActivityPane) WithEvents(events []streaming.SwarmEvent) *SwarmActivityPane {
	p.events = events
	return p
}

// bodyLines returns the styled, width-truncated body lines.
//
// Expected:
//   - width is the visible cell budget per line.
//
// Returns:
//   - A slice of styled lines, one per active body entry (events when set,
//     placeholder items otherwise).
//
// Side effects:
//   - None.
func (p *SwarmActivityPane) bodyLines(width int) []string {
	source := p.activeBodySource()
	out := make([]string, 0, len(source))
	for _, item := range source {
		out = append(out, p.bodyStyle.Render(truncate(item, width)))
	}
	return out
}

// activeBodySource returns the raw, unstyled body strings to render.
//
// When events are set, they take precedence and are formatted as
// "▸ {Type} · {AgentID} · {Status}" so the pane surfaces real swarm
// activity. An empty events slice is treated as "no events yet" and falls
// back to the placeholder items introduced in T5 so tests during the
// Wave 1 transition still render content.
//
// Returns:
//   - The slice of body strings in render order (oldest first).
//
// Side effects:
//   - None.
func (p *SwarmActivityPane) activeBodySource() []string {
	if len(p.events) == 0 {
		return p.items
	}
	out := make([]string, 0, len(p.events))
	for _, ev := range p.events {
		out = append(out, formatEvent(ev))
	}
	return out
}

// formatEvent renders a single SwarmEvent as a concise activity line.
//
// Expected:
//   - ev is a populated SwarmEvent.
//
// Returns:
//   - A single-line string of the form "▸ {Type} · {AgentID} · {Status}".
//
// Side effects:
//   - None.
func formatEvent(ev streaming.SwarmEvent) string {
	agent := ev.AgentID
	if agent == "" {
		agent = "-"
	}
	status := ev.Status
	if status == "" {
		status = "-"
	}
	return "▸ " + string(ev.Type) + " · " + agent + " · " + status
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
