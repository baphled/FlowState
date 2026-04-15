package navigation

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// SessionTrail renders the session-ancestry hierarchy introduced by the
// Multi-Agent Chat UX Plan.
//
// BreadcrumbBar in this package already represents intent-chain navigation;
// SessionTrail is the distinct session-ancestry hierarchy render path.
// They coexist because the two concepts render different semantics: intent
// breadcrumbs describe where a user is in the navigation graph, whereas the
// session trail describes the parent/child relationship between delegated
// agent sessions.
//
// Truncation policy: when there are more than five items, the trail collapses
// to the first two, an ellipsis, and the last three entries. If the joined
// output still exceeds the requested width, the middle section is collapsed
// further and, as a last resort, individual labels are truncated with a
// trailing ellipsis so the whole line fits.
//
// Usage:
//
//	trail := navigation.NewSessionTrail().
//	    WithItems([]navigation.SessionTrailItem{
//	        {SessionID: "root", AgentID: "orchestrator", Label: "root"},
//	        {SessionID: "child", AgentID: "engineer", Label: "child"},
//	    })
//	rendered := trail.Render(80)

// SessionTrailItem is a single entry in a session-ancestry trail.
type SessionTrailItem struct {
	SessionID string
	AgentID   string
	Label     string
}

// SessionTrail holds an ordered slice of session ancestry items and renders a
// truncated hierarchy display.
type SessionTrail struct {
	items     []SessionTrailItem
	separator string
}

const (
	sessionTrailSeparator = " > "
	sessionTrailEllipsis  = "…"
)

// NewSessionTrail creates a new, empty SessionTrail with the default
// separator.
//
// Expected:
//   - No arguments.
//
// Returns:
//   - A fully initialised SessionTrail ready to accept items.
//
// Side effects:
//   - None.
func NewSessionTrail() *SessionTrail {
	return &SessionTrail{
		items:     []SessionTrailItem{},
		separator: sessionTrailSeparator,
	}
}

// WithItems replaces the trail's items with a copy of the supplied slice and
// returns the trail for chaining.
//
// Expected:
//   - items may be nil or empty; both are treated as an empty trail.
//
// Returns:
//   - The receiver, enabling a fluent builder style.
//
// Side effects:
//   - Replaces the internal item slice with a defensive copy.
func (t *SessionTrail) WithItems(items []SessionTrailItem) *SessionTrail {
	copied := make([]SessionTrailItem, len(items))
	copy(copied, items)
	t.items = copied
	return t
}

// Items returns a defensive copy of the trail items so callers cannot mutate
// internal state.
//
// Expected:
//   - No arguments.
//
// Returns:
//   - A freshly allocated slice containing the current items in order.
//
// Side effects:
//   - None.
func (t *SessionTrail) Items() []SessionTrailItem {
	out := make([]SessionTrailItem, len(t.items))
	copy(out, t.items)
	return out
}

// Render produces the trail string clamped to the supplied width.
//
// Expected:
//   - width is the maximum permitted visual width; non-positive values yield
//     an empty string.
//
// Returns:
//   - The rendered trail respecting the first-2 + ellipsis + last-3 policy
//     and further truncation when the width is exceeded.
//
// Side effects:
//   - None.
func (t *SessionTrail) Render(width int) string {
	if width <= 0 || len(t.items) == 0 {
		return ""
	}

	labels := collectLabels(t.items)

	if len(labels) == 1 {
		return clampLabel(labels[0], width)
	}

	display := applyEllipsisPolicy(labels)
	rendered := strings.Join(display, t.separator)
	if lipgloss.Width(rendered) <= width {
		return rendered
	}

	// Width exceeded: collapse the middle more aggressively. Keep the first
	// and last label and drop everything in between to an ellipsis.
	if len(display) > 2 {
		collapsed := []string{display[0], sessionTrailEllipsis, display[len(display)-1]}
		rendered = strings.Join(collapsed, t.separator)
		if lipgloss.Width(rendered) <= width {
			return rendered
		}
	}

	// Last resort: shrink each retained label until the whole line fits, or
	// fall back to truncating the final rendered string with an ellipsis.
	return shrinkToWidth(rendered, width)
}

// collectLabels returns the label slice corresponding to trail items.
//
// Expected:
//   - items is a non-nil slice of SessionTrailItem.
//
// Returns:
//   - A slice of labels in the same order as items.
//
// Side effects:
//   - None.
func collectLabels(items []SessionTrailItem) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.Label
	}
	return out
}

// applyEllipsisPolicy implements the first-2 + ellipsis + last-3 rule.
//
// Expected:
//   - labels has at least one entry (callers guard against the zero case).
//
// Returns:
//   - The original slice when it has five or fewer items, otherwise a new
//     slice of the form [labels[0], labels[1], "…", labels[n-3], labels[n-2],
//     labels[n-1]].
//
// Side effects:
//   - None.
func applyEllipsisPolicy(labels []string) []string {
	if len(labels) <= 5 {
		return labels
	}
	n := len(labels)
	return []string{
		labels[0],
		labels[1],
		sessionTrailEllipsis,
		labels[n-3],
		labels[n-2],
		labels[n-1],
	}
}

// clampLabel truncates a single label so its visual width does not exceed the
// supplied budget, appending an ellipsis when any characters are dropped.
//
// Expected:
//   - width is positive.
//
// Returns:
//   - The original label when it already fits, otherwise a shortened string
//     ending with a single ellipsis rune.
//
// Side effects:
//   - None.
func clampLabel(label string, width int) string {
	if lipgloss.Width(label) <= width {
		return label
	}
	if width <= 1 {
		return sessionTrailEllipsis
	}

	runes := []rune(label)
	// Drop runes from the end until the label plus ellipsis fits.
	for i := len(runes); i > 0; i-- {
		candidate := string(runes[:i]) + sessionTrailEllipsis
		if lipgloss.Width(candidate) <= width {
			return candidate
		}
	}
	return sessionTrailEllipsis
}

// shrinkToWidth progressively trims the tail of a rendered string so it fits
// the supplied width, appending an ellipsis to indicate truncation.
//
// Expected:
//   - rendered is the fully joined trail and width is positive.
//
// Returns:
//   - A string no wider than width visual columns.
//
// Side effects:
//   - None.
func shrinkToWidth(rendered string, width int) string {
	if lipgloss.Width(rendered) <= width {
		return rendered
	}
	if width <= 1 {
		return sessionTrailEllipsis
	}

	runes := []rune(rendered)
	for i := len(runes); i > 0; i-- {
		candidate := string(runes[:i]) + sessionTrailEllipsis
		if lipgloss.Width(candidate) <= width {
			return candidate
		}
	}
	return sessionTrailEllipsis
}
