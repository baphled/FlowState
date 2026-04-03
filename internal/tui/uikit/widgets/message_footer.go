package widgets

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/baphled/flowstate/internal/tui/uikit/theme"
)

// MessageFooter renders the per-message metadata footer below an assistant message.
//
// The footer is formatted as:
//
//	▣ {Mode} · {modelID} · {duration}
//
// with an optional " · interrupted" suffix when the message was interrupted.
type MessageFooter struct {
	muted       lipgloss.Style
	indicator   lipgloss.Style
	mode        string
	modelID     string
	duration    time.Duration
	interrupted bool
	agentColor  lipgloss.Color
	th          theme.Theme
}

// NewMessageFooter creates a new MessageFooter using the given theme.
//
// Expected:
//   - th is a valid theme; must not be nil.
//
// Returns:
//   - A MessageFooter with pre-allocated lipgloss styles.
//
// Side effects:
//   - None.
func NewMessageFooter(th theme.Theme) *MessageFooter {
	f := &MessageFooter{
		th:    th,
		muted: lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Italic(true),
	}
	f.indicator = f.muted
	return f
}

// SetMetadata sets the display metadata for the footer.
//
// Expected:
//   - mode is the agent mode (e.g. "chat", "build", "plan"); empty omits the mode segment.
//   - modelID is the model identifier; required for the footer to render at all.
//   - duration is the time taken to generate the response.
//   - interrupted indicates whether the response was cut short.
//   - agentColor is an optional colour for the ▣ indicator; zero value falls back to the theme secondary colour.
//
// Side effects:
//   - Updates mode, modelID, duration, interrupted, agentColor, and indicator fields.
func (f *MessageFooter) SetMetadata(mode, modelID string, duration time.Duration, interrupted bool, agentColor lipgloss.Color) {
	f.mode = mode
	f.modelID = modelID
	f.duration = duration
	f.interrupted = interrupted
	f.agentColor = agentColor

	indicatorColor := f.th.SecondaryColor()
	if agentColor != lipgloss.Color("") {
		indicatorColor = agentColor
	}
	f.indicator = lipgloss.NewStyle().Foreground(indicatorColor).Italic(true)
}

// Render returns the formatted footer string, or empty string if no modelID is set.
//
// Returns:
//   - A styled string in the format "▣ {Mode} · {modelID} · {duration}" with optional " · interrupted".
//   - Empty string when modelID is not set.
//
// Side effects:
//   - None.
func (f *MessageFooter) Render() string {
	if f.modelID == "" {
		return ""
	}

	var sb strings.Builder

	sb.WriteString(f.indicator.Render("▣"))

	if f.mode != "" {
		sb.WriteString(f.muted.Render(" " + titleCase(f.mode) + " ·"))
	}

	sb.WriteString(f.muted.Render(" " + f.modelID + " · " + formatDuration(f.duration)))

	if f.interrupted {
		sb.WriteString(f.muted.Render(" · interrupted"))
	}

	return sb.String()
}

// titleCase uppercases the first letter of a string.
//
// Expected:
//   - s is any string; empty string returns unchanged.
//
// Returns:
//   - The input string with the first character uppercased.
//
// Side effects:
//   - None.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// formatDuration converts a duration to a human-readable string.
//
// Expected:
//   - d is a non-negative time.Duration.
//
// Returns:
//   - "{N}ms" for durations under 1 second.
//   - "{N}s" for durations from 1 second up to (but not including) 1 minute.
//   - "{M}m {S}s" for durations of 1 minute or more.
//
// Side effects:
//   - None.
func formatDuration(d time.Duration) string {
	if d < time.Second {
		ms := d.Milliseconds()
		return itoa(ms) + "ms"
	}
	if d < time.Minute {
		s := int(d.Seconds())
		return itoa(int64(s)) + "s"
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return itoa(int64(m)) + "m " + itoa(int64(s)) + "s"
}

// itoa converts an int64 to its decimal string representation without importing fmt.
//
// Expected:
//   - n is any int64, including negative values.
//
// Returns:
//   - The decimal string representation of n.
//
// Side effects:
//   - None.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
