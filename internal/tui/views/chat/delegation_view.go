package chat

import (
	"fmt"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/charmbracelet/lipgloss"
)

// DelegationStatusWidget displays the current status of agent delegation.
type DelegationStatusWidget struct {
	info        *provider.DelegationInfo
	theme       theme.Theme
	frame       int
	frames      []string
	dimStyle    lipgloss.Style
	activeStyle lipgloss.Style
	statusStyle lipgloss.Style
	errorStyle  lipgloss.Style
	stylesReady bool
}

// NewDelegationStatusWidget creates a new delegation status widget.
//
// Expected:
//   - info may be nil when the widget should render empty.
//   - t is a valid theme value.
//
// Returns:
//   - A widget configured with the current theme and spinner frames.
//
// Side effects:
//   - None.
func NewDelegationStatusWidget(info *provider.DelegationInfo, t theme.Theme) *DelegationStatusWidget {
	w := &DelegationStatusWidget{
		info:   info,
		theme:  t,
		frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
	}
	if t != nil {
		palette := t.Palette()
		w.dimStyle = lipgloss.NewStyle().Foreground(palette.ForegroundDim)
		w.activeStyle = lipgloss.NewStyle().Foreground(palette.Primary)
		w.statusStyle = lipgloss.NewStyle().Foreground(palette.Secondary)
		w.errorStyle = lipgloss.NewStyle().Foreground(palette.Error)
		w.stylesReady = true
	}
	return w
}

// SetFrame updates the spinner frame for animation.
//
// Expected:
//   - frame is the current animation frame.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Mutates the widget's frame state.
func (w *DelegationStatusWidget) SetFrame(frame int) {
	w.frame = frame
}

// Render returns the widget's string representation.
//
// Expected:
//   - The widget may have delegation information configured.
//
// Returns:
//   - The rendered delegation status, or an empty string when unset.
//
// Side effects:
//   - None.
func (w *DelegationStatusWidget) Render() string {
	if w.info == nil {
		return ""
	}

	dimStyle := w.dimStyle
	activeStyle := w.activeStyle
	statusStyle := w.statusStyle
	errorStyle := w.errorStyle

	if !w.stylesReady && w.theme != nil {
		palette := w.theme.Palette()
		dimStyle = lipgloss.NewStyle().Foreground(palette.ForegroundDim)
		activeStyle = lipgloss.NewStyle().Foreground(palette.Primary)
		statusStyle = lipgloss.NewStyle().Foreground(palette.Secondary)
		errorStyle = lipgloss.NewStyle().Foreground(palette.Error)
	}

	var icon string
	var statusText string

	switch w.info.Status {
	case "completed":
		icon = "✓"
		statusText = statusStyle.Render(w.info.Status)
	case "failed":
		icon = "✗"
		statusText = errorStyle.Render(w.info.Status)
	default:
		icon = w.frames[w.frame%len(w.frames)]
		statusText = activeStyle.Render(w.info.Status)
	}

	parts := []string{
		activeStyle.Render(icon),
		dimStyle.Render("Delegation:"),
		activeStyle.Render(w.info.TargetAgent),
		dimStyle.Render("(" + w.info.ModelName + "/" + w.info.ProviderName + ")"),
		"[" + statusText + "]",
	}

	if w.info.Description != "" {
		parts = append(parts, dimStyle.Render("- "+w.info.Description))
	}

	return strings.Join(parts, " ")
}

// View renders the widget (alias for Render).
//
// Expected:
//   - The widget may have delegation information configured.
//
// Returns:
//   - The same content as Render().
//
// Side effects:
//   - None.
func (w *DelegationStatusWidget) View() string {
	return w.Render()
}

// CollapsibleDelegationBlock displays delegation information in a collapsible format.
// When collapsed, it shows a single-line summary with spinner and status.
// When expanded, it displays detailed information in a multi-line block.
type CollapsibleDelegationBlock struct {
	info        *provider.DelegationInfo
	theme       theme.Theme
	expanded    bool
	YPosition   int
	Height      int
	hovered     bool
	frames      []string
	frame       int
	dimStyle    lipgloss.Style
	activeStyle lipgloss.Style
	statusStyle lipgloss.Style
	errorStyle  lipgloss.Style
	stylesReady bool
}

// NewCollapsibleDelegationBlock creates a new collapsible delegation block.
//
// Expected:
//   - info contains the delegation metadata to display.
//   - t is a valid theme value (may be nil).
//
// Returns:
//   - A block configured with the current theme and spinner frames.
//
// Side effects:
//   - None.
func NewCollapsibleDelegationBlock(info *provider.DelegationInfo, t theme.Theme) *CollapsibleDelegationBlock {
	b := &CollapsibleDelegationBlock{
		info:     info,
		theme:    t,
		expanded: false,
		frames:   []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		Height:   1,
	}
	if t != nil {
		palette := t.Palette()
		b.dimStyle = lipgloss.NewStyle().Foreground(palette.ForegroundDim)
		b.activeStyle = lipgloss.NewStyle().Foreground(palette.Primary)
		b.statusStyle = lipgloss.NewStyle().Foreground(palette.Secondary)
		b.errorStyle = lipgloss.NewStyle().Foreground(palette.Error)
		b.stylesReady = true
	}
	return b
}

// Toggle switches between collapsed and expanded state.
//
// Expected:
//   - Block is properly initialised.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Flips the expanded flag.
func (b *CollapsibleDelegationBlock) Toggle() {
	b.expanded = !b.expanded
}

// IsExpanded returns whether the block is in expanded state.
//
// Expected:
//   - Block is properly initialised.
//
// Returns:
//   - true if expanded, false if collapsed.
//
// Side effects:
//   - None.
func (b *CollapsibleDelegationBlock) IsExpanded() bool {
	return b.expanded
}

// SetFrame updates the spinner frame for animation.
//
// Expected:
//   - frame is a valid animation frame index.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Mutates the block's frame state.
func (b *CollapsibleDelegationBlock) SetFrame(frame int) {
	b.frame = frame
}

// SetYPosition records the vertical position of this block.
//
// Expected:
//   - y is a valid terminal Y coordinate.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Stores the Y position for mouse hit detection.
func (b *CollapsibleDelegationBlock) SetYPosition(y int) {
	b.YPosition = y
}

// Render returns the string representation of the delegation block.
// If collapsed, returns a single line with spinner and status.
// If expanded, returns a multi-line detailed view with agent, model, tools, and elapsed time.
//
// Expected:
//   - Block is properly initialised with delegation info.
//
// Returns:
//   - Rendered block as a string. Updates Height based on line count.
//
// Side effects:
//   - Updates the Height field based on newline count in output.
func (b *CollapsibleDelegationBlock) Render() string {
	if b.info == nil {
		return ""
	}

	dimStyle := b.dimStyle
	activeStyle := b.activeStyle
	statusStyle := b.statusStyle
	errorStyle := b.errorStyle

	if !b.stylesReady && b.theme != nil {
		palette := b.theme.Palette()
		dimStyle = lipgloss.NewStyle().Foreground(palette.ForegroundDim)
		activeStyle = lipgloss.NewStyle().Foreground(palette.Primary)
		statusStyle = lipgloss.NewStyle().Foreground(palette.Secondary)
		errorStyle = lipgloss.NewStyle().Foreground(palette.Error)
	}

	if b.expanded {
		return b.renderExpanded(dimStyle, activeStyle, statusStyle, errorStyle)
	}
	return b.renderCollapsed(activeStyle, statusStyle, errorStyle)
}

// renderCollapsed renders the collapsed single-line view with spinner and status.
//
// Expected:
//   - Styles are properly initialised.
//
// Returns:
//   - Single-line string representation.
//
// Side effects:
//   - Updates Height based on the rendered output.
func (b *CollapsibleDelegationBlock) renderCollapsed(activeStyle, statusStyle, errorStyle lipgloss.Style) string {
	var icon string
	var statusText string

	switch b.info.Status {
	case "completed":
		icon = "✓"
		statusText = statusStyle.Render(b.info.Status)
	case "failed":
		icon = "✗"
		statusText = errorStyle.Render(b.info.Status)
	default:
		icon = b.frames[b.frame%len(b.frames)]
		statusText = activeStyle.Render(b.info.Status)
	}

	parts := []string{
		activeStyle.Render(icon),
		activeStyle.Render(b.info.TargetAgent),
		"[" + statusText + "]",
	}

	output := strings.Join(parts, " ")
	b.Height = strings.Count(output, "\n") + 1
	return output
}

// renderExpanded renders the expanded multi-line view with detailed delegation information.
//
// Expected:
//   - Styles are properly initialised.
//
// Returns:
//   - Multi-line string representation with bordered content.
//
// Side effects:
//   - Updates Height based on the rendered output.
func (b *CollapsibleDelegationBlock) renderExpanded(dimStyle, activeStyle, statusStyle, errorStyle lipgloss.Style) string {
	var lines []string

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("8")).
		Padding(1)

	var statusText string
	switch b.info.Status {
	case "completed":
		statusText = statusStyle.Render(b.info.Status)
	case "failed":
		statusText = errorStyle.Render(b.info.Status)
	default:
		statusText = activeStyle.Render(b.info.Status)
	}

	lines = append(lines,
		dimStyle.Render("Agent:")+"    "+activeStyle.Render(b.info.TargetAgent),
		dimStyle.Render("Model:")+"    "+activeStyle.Render(b.info.ModelName+" / "+b.info.ProviderName),
		dimStyle.Render("Status:")+"   "+statusText,
	)

	if b.info.ToolCalls > 0 {
		toolInfo := fmt.Sprintf("%d calls", b.info.ToolCalls)
		if b.info.LastTool != "" {
			toolInfo += " (last: " + b.info.LastTool + ")"
		}
		lines = append(lines, dimStyle.Render("Tools:")+"    "+activeStyle.Render(toolInfo))
	}

	if b.info.StartedAt != nil {
		elapsed := time.Since(*b.info.StartedAt).Round(time.Millisecond).String()
		lines = append(lines, dimStyle.Render("Elapsed:")+"  "+activeStyle.Render(elapsed))
	} else {
		lines = append(lines, dimStyle.Render("Elapsed:")+"  "+activeStyle.Render("0s"))
	}

	if b.info.CompletedAt != nil && b.info.Description != "" {
		lines = append(lines, dimStyle.Render("Result:")+"   "+activeStyle.Render(b.info.Description))
	} else {
		lines = append(lines, dimStyle.Render("Result:")+"   "+dimStyle.Render("(pending)"))
	}

	content := strings.Join(lines, "\n")
	bordered := border.Render(content)

	b.Height = strings.Count(bordered, "\n") + 1
	return bordered
}

// ContainsY reports whether the given Y coordinate falls within this block's rendered bounds.
//
// Expected:
//   - b.Height has been set by a prior call to Render().
//
// Returns:
//   - true when YPosition <= y < YPosition+Height.
//
// Side effects:
//   - None.
func (b *CollapsibleDelegationBlock) ContainsY(y int) bool {
	return y >= b.YPosition && y < b.YPosition+b.Height
}

// SetHovered sets the hover highlight state for this block.
//
// Expected:
//   - hovered is the desired state.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Mutates b.hovered.
func (b *CollapsibleDelegationBlock) SetHovered(hovered bool) {
	b.hovered = hovered
}

// SessionID returns the chain ID used to identify the child session.
//
// Expected:
//   - b.info may be nil.
//
// Returns:
//   - b.info.ChainID, or empty string if info is nil.
//
// Side effects:
//   - None.
func (b *CollapsibleDelegationBlock) SessionID() string {
	if b.info == nil {
		return ""
	}
	return b.info.ChainID
}

// ChainID returns the delegation chain identifier.
//
// Expected:
//   - b.info may be nil.
//
// Returns:
//   - b.info.ChainID, or empty string if info is nil.
//
// Side effects:
//   - None.
func (b *CollapsibleDelegationBlock) ChainID() string {
	if b.info == nil {
		return ""
	}
	return b.info.ChainID
}
