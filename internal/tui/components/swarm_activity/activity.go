package swarmactivity

import (
	"fmt"
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
	// EmptyStateText is shown once a caller has explicitly asserted that
	// the loaded timeline is empty (see hasSeenRealEvents). It differs
	// from the placeholder items so the user can distinguish "still
	// loading" (P3 A1 placeholders) from "genuinely no activity yet"
	// (this text). Defined as a package-internal constant so P3 tests can
	// assert the rendered string without hard-coding a magic literal.
	emptyStateText = "No activity yet"
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
	title        string
	items        []string
	events       []streaming.SwarmEvent
	visibleTypes map[streaming.SwarmEventType]bool
	headerStyle  lipgloss.Style
	bodyStyle    lipgloss.Style
	dimStyle     lipgloss.Style
	// hasSeenRealEvents flips to true the first time a caller passes a
	// non-nil events slice to WithEvents. Once set, the pane never falls
	// back to placeholder items — an empty slice is treated as a genuine
	// "no activity yet" state rather than "still loading".
	//
	// This preserves the useful placeholder affordance during early app
	// startup (before any session has been loaded) while preventing the
	// confusing permanent-placeholder case for brand-new, empty sessions.
	hasSeenRealEvents bool
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
		title: headerText,
		items: append([]string(nil), defaultItems...),
		visibleTypes: map[streaming.SwarmEventType]bool{
			streaming.EventDelegation: true,
			streaming.EventToolCall:   true,
			streaming.EventToolResult: true,
			streaming.EventPlan:       true,
			streaming.EventReview:     true,
		},
		headerStyle: lipgloss.NewStyle().Bold(true),
		bodyStyle:   lipgloss.NewStyle(),
		dimStyle:    lipgloss.NewStyle().Faint(true),
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

	// Header with optional count summary.
	headerLine := p.title + p.countSummary()
	lines = append(lines, p.headerStyle.Render(truncate(headerLine, width)))

	// Filter indicator line when at least one type is hidden.
	filterActive := p.isFilterActive()
	if filterActive {
		lines = append(lines, truncate(p.filterIndicatorLine(), width))
	}

	body := p.bodyLines(width)
	// Drop oldest (top of body) if total lines would exceed height.
	bodyBudget := height - len(lines)
	if bodyBudget < 0 {
		bodyBudget = 0
	}
	if len(body) > bodyBudget {
		body = body[len(body)-bodyBudget:]
	}
	lines = append(lines, body...)

	return strings.Join(lines, "\n")
}

// WithEvents swaps the pane's body source from placeholder items to the
// supplied swarm events.
//
// A nil slice is interpreted as "no caller has asserted a loaded state
// yet" and leaves the placeholder-mode flag unchanged. A non-nil slice —
// even an empty one — flips the pane into loaded mode: on subsequent
// renders the pane displays either the events or an explicit empty-state
// message ("No activity yet"), never the placeholder items again.
//
// This distinction lets early app startup continue to show placeholders
// while a brand-new, genuinely empty session can surface the right
// messaging after first session load.
//
// Expected:
//   - events is the ordered list of events (oldest first) to render, or
//     nil to preserve placeholder-mode semantics.
//
// Returns:
//   - The receiver, to support fluent builder-style configuration.
//
// Side effects:
//   - Stores a reference to the supplied slice on the receiver. Callers
//     that mutate the slice afterwards will see the change reflected on
//     the next Render; the chat intent always supplies a fresh
//     SwarmEventStore.All() copy so this is safe in practice.
//   - Flips hasSeenRealEvents to true when events is non-nil.
func (p *SwarmActivityPane) WithEvents(events []streaming.SwarmEvent) *SwarmActivityPane {
	p.events = events
	if events != nil {
		p.hasSeenRealEvents = true
	}
	return p
}

// WithVisibleTypes sets the per-type visibility filter. Each key maps a
// SwarmEventType to its visibility; true means visible, false means hidden.
// When filtering is active (at least one type hidden), the pane renders a
// filter indicator line below the header and appends a count summary.
//
// Expected:
//   - types is a map whose keys are SwarmEventType values. Missing keys are
//     treated as hidden.
//
// Returns:
//   - The receiver, to support fluent builder-style configuration.
//
// Side effects:
//   - Replaces the receiver's visibleTypes map.
func (p *SwarmActivityPane) WithVisibleTypes(types map[streaming.SwarmEventType]bool) *SwarmActivityPane {
	p.visibleTypes = types
	return p
}

// isFilterActive reports whether at least one event type is hidden.
//
// Returns:
//   - true when at least one type in visibleTypes is false.
//
// Side effects:
//   - None.
func (p *SwarmActivityPane) isFilterActive() bool {
	for _, vis := range p.visibleTypes {
		if !vis {
			return true
		}
	}
	return false
}

// filterIndicatorLine renders the "[D] [T] [P] [R]" indicator showing which
// types are active or dimmed.
//
// Returns:
//   - A space-separated string of type tags, with hidden types rendered faint.
//
// Side effects:
//   - None.
func (p *SwarmActivityPane) filterIndicatorLine() string {
	type entry struct {
		label  string
		evType streaming.SwarmEventType
	}
	entries := []entry{
		{"D", streaming.EventDelegation},
		{"T", streaming.EventToolCall},
		{"P", streaming.EventPlan},
		{"R", streaming.EventReview},
	}
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		tag := "[" + e.label + "]"
		if !p.visibleTypes[e.evType] {
			tag = p.dimStyle.Render(tag)
		}
		parts = append(parts, tag)
	}
	return strings.Join(parts, " ")
}

// countSummary returns "showing X of Y" when filtering is active and events
// are present. It returns empty string otherwise.
//
// When every type is hidden and at least one event exists, the bare
// "showing 0 of N" is replaced with an actionable hint because the numeric
// summary on its own confuses users (QA finding F17: "is this a bug or
// did I turn off everything?"). The hint tells them how to recover.
//
// Returns:
//   - A formatted count string, an actionable hint, or empty when no
//     filtering is active.
//
// Side effects:
//   - None.
func (p *SwarmActivityPane) countSummary() string {
	if len(p.events) == 0 || !p.isFilterActive() {
		return ""
	}
	total := len(p.events)
	shown := 0
	for _, ev := range p.events {
		if p.visibleTypes[ev.Type] {
			shown++
		}
	}
	// P8 T3: when the user has hidden every type, the raw count is
	// misleading. Surface an explicit recovery hint instead.
	if shown == 0 {
		return " — All events hidden (press [T] to toggle filters)"
	}
	return fmt.Sprintf(" (showing %d of %d)", shown, total)
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
// The pane operates in one of three modes:
//
//  1. Placeholder mode — hasSeenRealEvents is false (no caller has
//     asserted a loaded state). The pane shows illustrative items so
//     early startup has visible content. Once a caller passes a non-nil
//     slice to WithEvents this mode is never re-entered.
//
//  2. Loaded-with-events mode — hasSeenRealEvents is true and the events
//     slice is non-empty. Body lines flow through coalesceToolCalls so
//     tool_call / tool_result pairs collapse into one line; other types
//     pass through untouched.
//
//  3. Loaded-empty mode — hasSeenRealEvents is true but there are no
//     events (brand-new session). The pane shows an explicit
//     "No activity yet" message rather than reverting to placeholders,
//     distinguishing the genuine empty state from still-loading.
//
// Returns:
//   - The slice of body strings in render order (oldest first).
//
// Side effects:
//   - None.
func (p *SwarmActivityPane) activeBodySource() []string {
	if !p.hasSeenRealEvents {
		return p.items
	}
	if len(p.events) == 0 {
		return []string{emptyStateText}
	}
	return coalesceToolCalls(p.events, p.visibleTypes)
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
