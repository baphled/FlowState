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
	streaming    bool
	spinnerFrame int
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

// SetStreaming sets the streaming state and current spinner frame for animated display.
//
// Expected:
//   - streaming indicates whether a response is currently being generated.
//   - frame is the current spinner frame index from the Intent tick loop.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Updates the streaming and spinnerFrame fields.
func (s *StatusBar) SetStreaming(streaming bool, frame int) {
	s.streaming = streaming
	s.spinnerFrame = frame
}

// tokenColor determines the colour based on token usage ratio.
//
// Expected:
//   - used is the number of tokens used (>=0).
//   - budget is the token budget (>=0).
//   - th is the theme (must be non-nil).
//
// Returns:
//   - A lipgloss.Color: muted if budget is 0, success if <70%, warning if 70-90%, error if >90%.
//
// Side effects:
//   - None.
func tokenColor(used, budget int, th theme.Theme) lipgloss.Color {
	if budget == 0 {
		return th.MutedColor()
	}
	pct := float64(used) / float64(budget)
	switch {
	case pct < 0.70:
		return th.SuccessColor()
	case pct < 0.90:
		return th.WarningColor()
	default:
		return th.ErrorColor()
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

	modeText := s.mode
	if s.streaming {
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		modeText = frames[s.spinnerFrame%len(frames)]
	}
	modeBadge := primitives.NewBadge(modeText, th).Variant(primitives.BadgeStatus).Render()

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

	usageColor := tokenColor(s.tokensUsed, s.tokenBudget, th)
	tokenLabel := fmt.Sprintf("%d / %d", s.tokensUsed, s.tokenBudget)
	tokenLabelStyled := lipgloss.NewStyle().Foreground(usageColor).Render(tokenLabel)

	leftSide := lipgloss.JoinHorizontal(lipgloss.Left, modeBadge, " ", providerText, " ", modelText)
	rightSide := lipgloss.JoinHorizontal(lipgloss.Left, tokenBar, " ", tokenLabelStyled)

	containerStyle := lipgloss.NewStyle().
		Width(width).
		Background(th.MutedColor()).
		Foreground(th.PrimaryColor())

	availableWidth := width - lipgloss.Width(leftSide) - lipgloss.Width(rightSide)
	if availableWidth < 0 {
		availableWidth = 0
	}
	spacer := strings.Repeat(" ", availableWidth)

	return containerStyle.Render(lipgloss.JoinHorizontal(lipgloss.Top, leftSide, spacer, rightSide))
}
