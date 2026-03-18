package layout

import (
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/tui/uikit/primitives"
	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/charmbracelet/lipgloss"
)

// StatusBarMsg carries status updates to the StatusBar component.
type StatusBarMsg struct {
	Provider    string
	Model       string
	TokensUsed  int
	TokenBudget int
	Mode        string // "NORMAL" or "INSERT"
}

// StatusBar renders provider, model, token usage, and input mode using UIKit primitives.
// It is theme-aware and uses Badge for mode, Text for provider/model, and ProgressBar for tokens.
type StatusBar struct {
	theme.Aware
	provider    string
	model       string
	tokensUsed  int
	tokenBudget int
	mode        string
	width       int
}

// NewStatusBar creates a new StatusBar with the given width.
//
// Expected:
//   - width is a positive integer representing terminal columns.
//
// Returns:
//   - An initialised StatusBar with mode set to "NORMAL" and the default theme.
//
// Side effects:
//   - None.
func NewStatusBar(width int) *StatusBar {
	sb := &StatusBar{
		mode:  "NORMAL",
		width: width,
	}
	sb.SetTheme(theme.Default())
	return sb
}

// WithTheme sets the theme for the StatusBar.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - The StatusBar instance for method chaining.
//
// Side effects:
//   - None.
func (s *StatusBar) WithTheme(th theme.Theme) *StatusBar {
	s.SetTheme(th)
	return s
}

// Update applies a StatusBarMsg to the StatusBar state.
//
// Expected:
//   - msg is a StatusBarMsg with status updates.
//
// Side effects:
//   - Updates provider, model, tokens, and mode fields from the message.
func (s *StatusBar) Update(msg StatusBarMsg) {
	if msg.Provider != "" {
		s.provider = msg.Provider
	}
	if msg.Model != "" {
		s.model = msg.Model
	}
	s.tokensUsed = msg.TokensUsed
	s.tokenBudget = msg.TokenBudget

	if msg.Mode != "" {
		s.mode = msg.Mode
	}
}

// tokenColor determines the colour based on token usage ratio.
//
// Expected:
//   - used is the number of tokens used (>=0).
//   - budget is the token budget (>=0).
//
// Returns:
//   - A lipgloss.Color: grey if budget is 0, green if <70%, yellow if 70-90%, red if >90%.
//
// Side effects:
//   - None.
func tokenColor(used, budget int) lipgloss.Color {
	if budget == 0 {
		return lipgloss.Color("#888888")
	}
	pct := float64(used) / float64(budget)
	switch {
	case pct < 0.70:
		return lipgloss.Color("#00FF00")
	case pct < 0.90:
		return lipgloss.Color("#FFAA00")
	default:
		return lipgloss.Color("#FF0000")
	}
}

// RenderContent renders the status bar for the given width using UIKit primitives.
//
// Expected:
//   - width is the terminal width in columns (>0).
//
// Returns:
//   - A rendered status bar string with mode badge, provider, model, and token usage.
//
// Side effects:
//   - None.
func (s *StatusBar) RenderContent(width int) string {
	th := s.Theme()

	modeBadge := primitives.NewBadge(s.mode, th).Variant(primitives.BadgeStatus).Render()

	providerText := primitives.NewText(s.provider, th).Bold().Render()

	modelStr := s.model
	if width < 60 && len(modelStr) > 10 {
		modelStr = modelStr[:10] + "..."
	}
	modelText := primitives.NewText(modelStr, th).Render()

	var tokenValue float64
	if s.tokenBudget > 0 {
		tokenValue = float64(s.tokensUsed) / float64(s.tokenBudget)
	}
	tokenBar := primitives.NewProgressBar(tokenValue, th).Width(12).ShowPercentage(false).Render()

	usageColor := tokenColor(s.tokensUsed, s.tokenBudget)
	tokenLabel := fmt.Sprintf("%d / %d", s.tokensUsed, s.tokenBudget)
	tokenLabelStyled := lipgloss.NewStyle().Foreground(usageColor).Render(tokenLabel)

	leftSide := lipgloss.JoinHorizontal(lipgloss.Left, modeBadge, " ", providerText, " ", modelText)
	rightSide := lipgloss.JoinHorizontal(lipgloss.Left, tokenBar, " ", tokenLabelStyled)

	containerStyle := lipgloss.NewStyle().
		Width(width).
		Background(lipgloss.Color("#222222")).
		Foreground(lipgloss.Color("#DDDDDD"))

	availableWidth := width - lipgloss.Width(leftSide) - lipgloss.Width(rightSide)
	if availableWidth < 0 {
		availableWidth = 0
	}
	spacer := strings.Repeat(" ", availableWidth)

	return containerStyle.Render(lipgloss.JoinHorizontal(lipgloss.Top, leftSide, spacer, rightSide))
}
