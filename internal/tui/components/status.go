// Package components provides reusable UIKit components for the FlowState TUI.
package components

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// StatusBarMsg carries status updates to the StatusBar.
type StatusBarMsg struct {
	Provider    string
	Model       string
	TokensUsed  int
	TokenBudget int
	Mode        string // "NORMAL" or "INSERT"
}

// StatusBar renders provider, model, token usage, and input mode.
type StatusBar struct {
	provider    string
	model       string
	tokensUsed  int
	tokenBudget int
	mode        string
}

// New creates a new StatusBar with defaults.
func New() *StatusBar {
	return &StatusBar{
		mode: "NORMAL",
	}
}

// Update applies a StatusBarMsg to the StatusBar state.
func (s *StatusBar) Update(msg StatusBarMsg) {
	if msg.Provider != "" {
		s.provider = msg.Provider
	}
	if msg.Model != "" {
		s.model = msg.Model
	}
	// Always update tokens as they might be 0
	s.tokensUsed = msg.TokensUsed
	s.tokenBudget = msg.TokenBudget

	if msg.Mode != "" {
		s.mode = msg.Mode
	}
}

// tokenColor is a helper to determine color based on usage.
func tokenColor(used, budget int) lipgloss.Color {
	if budget == 0 {
		return lipgloss.Color("#888888") // grey when no budget set
	}
	pct := float64(used) / float64(budget)
	switch {
	case pct < 0.70:
		return lipgloss.Color("#00FF00") // green
	case pct < 0.90:
		return lipgloss.Color("#FFAA00") // yellow
	default:
		return lipgloss.Color("#FF0000") // red
	}
}

// RenderContent renders the status bar for the given width.
// Uses lipgloss for styling:
// - subtle background
// - provider name in bold
// - token count colour-coded: green <70%, yellow 70-90%, red >90%
// - mode indicator (NORMAL/INSERT)
func (s *StatusBar) RenderContent(width int) string {
	var (
		providerStyle  = lipgloss.NewStyle().Bold(true).Padding(0, 1)
		modelStyle     = lipgloss.NewStyle().Padding(0, 1)
		tokenStyle     = lipgloss.NewStyle().Padding(0, 1)
		modeStyle      = lipgloss.NewStyle().Padding(0, 1).Bold(true)
		containerStyle = lipgloss.NewStyle().
				Width(width).
				Background(lipgloss.Color("#222222")). // Subtle background
				Foreground(lipgloss.Color("#DDDDDD"))
	)

	// Mode Indicator
	modeStr := s.mode
	if modeStr == "" {
		modeStr = "NORMAL"
	}
	modeSection := modeStyle.Render(modeStr)

	// Provider
	providerSection := providerStyle.Render(s.provider)

	// Model
	modelStr := s.model
	if width < 60 && len(modelStr) > 10 {
		modelStr = modelStr[:10] + "..."
	}
	modelSection := modelStyle.Render(modelStr)

	// Token Usage
	usageColor := tokenColor(s.tokensUsed, s.tokenBudget)
	tokenStr := fmt.Sprintf("%d / %d", s.tokensUsed, s.tokenBudget)
	tokenSection := tokenStyle.Foreground(usageColor).Render(tokenStr)

	// Layout: Mode | Provider | Model | ... | Tokens
	leftSide := lipgloss.JoinHorizontal(lipgloss.Left, modeSection, providerSection, modelSection)

	// Calculate available space for spacer
	availableWidth := width - lipgloss.Width(leftSide) - lipgloss.Width(tokenSection)
	if availableWidth < 0 {
		availableWidth = 0
	}
	spacer := strings.Repeat(" ", availableWidth)

	return containerStyle.Render(lipgloss.JoinHorizontal(lipgloss.Top, leftSide, spacer, tokenSection))
}
