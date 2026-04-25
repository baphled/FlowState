// Package widgets provides reusable UI components for the TUI.
// BlockTool is a collapsible widget for tool call output display.
package widgets

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const defaultBlockToolMaxLines = 10

// BlockTool renders tool call output in collapsed and expanded forms.
type BlockTool struct {
	name      string
	input     string
	output    string
	expanded  bool
	maxLines  int
	width     int
	yPosition int

	collapsedStyle lipgloss.Style
	borderStyle    lipgloss.Style
	titleStyle     lipgloss.Style
}

// NewBlockTool creates a BlockTool for displaying tool call output.
//
// Expected:
//   - name is the tool name.
//   - input is the primary argument string.
//   - output is the tool result text.
//
// Returns:
//   - A new BlockTool in collapsed state with maxLines=10.
//
// Side effects:
//   - None.
func NewBlockTool(name, input, output string) *BlockTool {
	return &BlockTool{
		name:           name,
		input:          input,
		output:         output,
		maxLines:       defaultBlockToolMaxLines,
		collapsedStyle: lipgloss.NewStyle(),
		borderStyle: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
			BorderLeft(true).
			BorderRight(false).
			BorderTop(false).
			BorderBottom(false),
		titleStyle: lipgloss.NewStyle().Bold(true),
	}
}

// SetExpanded updates the expanded state.
//
// Expected:
//   - expanded is the desired expansion state.
//
// Side effects:
//   - Updates the widget state.
func (b *BlockTool) SetExpanded(expanded bool) {
	b.expanded = expanded
}

// IsExpanded reports whether the widget is expanded.
//
// Returns:
//   - True when the widget is expanded.
//
// Side effects:
//   - None.
func (b *BlockTool) IsExpanded() bool {
	return b.expanded
}

// SetMaxLines updates the output line limit.
//
// Expected:
//   - n is the desired maximum line count.
//
// Side effects:
//   - Updates the widget state.
func (b *BlockTool) SetMaxLines(n int) {
	if n > 0 {
		b.maxLines = n
	}
}

// SetWidth updates the widget width.
//
// Expected:
//   - w is the desired width.
//
// Side effects:
//   - Updates the widget state.
func (b *BlockTool) SetWidth(w int) {
	b.width = w
}

// SetYPosition updates the widget y position.
//
// Expected:
//   - y is the desired vertical position.
//
// Side effects:
//   - Updates the widget state.
func (b *BlockTool) SetYPosition(y int) {
	b.yPosition = y
}

// YPosition returns the stored vertical position.
//
// Returns:
//   - The stored y position.
//
// Side effects:
//   - None.
func (b *BlockTool) YPosition() int {
	return b.yPosition
}

// Render returns the rendered block tool string.
//
// Returns:
//   - A collapsed single-line summary or an expanded bordered output.
//
// Side effects:
//   - None.
func (b *BlockTool) Render() string {
	if !b.expanded {
		return b.collapsedStyle.Render(ToolIcon(b.name) + " " + b.name + ": " + truncateText(b.input, 50))
	}

	lines := strings.Split(b.output, "\n")
	if len(lines) > b.maxLines {
		lines = lines[:b.maxLines]
	}

	content := strings.Join(lines, "\n")
	// Title carries the tool-specific icon so the expanded form retains
	// the visual identity the collapsed form already had.
	title := ToolIcon(b.name) + " " + b.name + " " + b.input
	return b.borderStyle.Render(b.titleStyle.Render(title) + "\n" + content)
}

// truncateText returns the input truncated to the requested length.
//
// Expected:
//   - text is the source string.
//   - max is the maximum rune count.
//
// Returns:
//   - The original text or a truncated prefix with an ellipsis.
//
// Side effects:
//   - None.
func truncateText(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}
