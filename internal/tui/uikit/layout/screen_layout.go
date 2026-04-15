package layout

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/baphled/flowstate/internal/tui/uikit/navigation"
	"github.com/baphled/flowstate/internal/ui/terminal"
	"github.com/baphled/flowstate/internal/ui/themes"
)

// ModalRenderer is an interface for components that can render a modal overlay.
// This allows ScreenLayout to work with any modal implementation without
// creating import cycles. Both feedback.Modal and components.ModalContent satisfy this.
type ModalRenderer interface {
	// Render returns the rendered modal string for the given terminal dimensions.
	Render(terminalWidth, terminalHeight int) string
}

// LogoRenderer is an interface for components that can render a logo.
// This allows ScreenLayout to work with any logo implementation without
// creating import cycles.
type LogoRenderer interface {
	// ViewStatic returns the static logo view.
	ViewStatic() string
	// SetWidth sets the width for the logo rendering.
	SetWidth(width int)
}

// ScreenLayout provides a standardized screen layout with logo, content, and help footer.
//
// This is the UIKit version that uses theme-based styling exclusively.
//
// Example usage:
//
//	logo := display.NewLogo(false, termInfo.Width)
//	view := NewScreenLayout(termInfo).
//	    WithLogo(logo, 2).
//	    WithBreadcrumbs("Main Menu", "Settings").
//	    WithContent("Your content here").
//	    WithHelp("↑/k Up  ↓/j Down  Enter Select  Esc Back").
//	    WithFooterSeparator(true)
//	output := view.Render()
type ScreenLayout struct {
	ShowLogo            bool
	Logo                LogoRenderer
	LogoSpacing         int
	ShowHeader          bool
	Breadcrumbs         []string
	Title               string
	Subtitle            string
	Content             string
	ContentStyle        lipgloss.Style
	ShowModal           bool
	Modal               ModalRenderer
	HelpText            string
	InputLine           string
	StatusBarContent    string
	ShowFooter          bool
	ShowFooterSeparator bool
	TerminalInfo        *terminal.Info
	UseFullWidth        bool
	theme               themes.Theme
	secondaryContent    string
}

// NewScreenLayout creates a new ScreenLayout with default settings.
//
// Expected:
//   - info must be valid.
//
// Returns:
//   - A fully initialized ScreenLayout ready for use.
//
// Side effects:
//   - None.
func NewScreenLayout(info *terminal.Info) *ScreenLayout {
	if info == nil || (info.Width == 0 && info.Height == 0) {
		info = &terminal.Info{Width: 140, Height: 40}
	}

	return &ScreenLayout{
		ShowLogo:            false,
		LogoSpacing:         2,
		ShowHeader:          false,
		ShowFooter:          true,
		ShowFooterSeparator: false,
		TerminalInfo:        info,
		UseFullWidth:        true,
		ContentStyle:        lipgloss.NewStyle(),
		theme:               themes.NewDefaultTheme(),
	}
}

// getTheme returns the theme or default if nil.
//
// Returns:
//   - The assigned theme, or a default theme if none is set.
//
// Side effects:
//   - None.
func (sl *ScreenLayout) getTheme() themes.Theme {
	if sl.theme != nil {
		return sl.theme
	}
	return themes.NewDefaultTheme()
}

// WithLogo sets the logo to display at the top with optional spacing before it.
//
// Expected:
//   - logorenderer must be valid.
//   - int must be valid.
//
// Returns:
//   - A fully initialized ScreenLayout ready for use.
//
// Side effects:
//   - None.
func (sl *ScreenLayout) WithLogo(logo LogoRenderer, spacing int) *ScreenLayout {
	sl.ShowLogo = true
	sl.Logo = logo
	sl.LogoSpacing = spacing
	return sl
}

// WithBreadcrumbs sets breadcrumbs for the header.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized ScreenLayout ready for use.
//
// Side effects:
//   - None.
func (sl *ScreenLayout) WithBreadcrumbs(crumbs ...string) *ScreenLayout {
	sl.ShowHeader = true
	sl.Breadcrumbs = crumbs
	return sl
}

// WithTitle sets the title and subtitle for the header.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized ScreenLayout ready for use.
//
// Side effects:
//   - None.
func (sl *ScreenLayout) WithTitle(title, subtitle string) *ScreenLayout {
	sl.ShowHeader = true
	sl.Title = title
	sl.Subtitle = subtitle
	return sl
}

// WithContent sets the main content to display.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized ScreenLayout ready for use.
//
// Side effects:
//   - None.
func (sl *ScreenLayout) WithContent(content string) *ScreenLayout {
	sl.Content = content
	return sl
}

// WithSecondaryContent sets the secondary pane content used by dual-pane renders.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized ScreenLayout ready for use.
//
// Side effects:
//   - None.
func (sl *ScreenLayout) WithSecondaryContent(content string) *ScreenLayout {
	sl.secondaryContent = content
	return sl
}

// WithContentStyle sets a custom style for the content
//
// Expected:
//   - style must be valid.
//
// Returns:
//   - A fully initialized ScreenLayout ready for use.
//
// Side effects:
//   - None.
func (sl *ScreenLayout) WithContentStyle(style lipgloss.Style) *ScreenLayout {
	sl.ContentStyle = style
	return sl
}

// WithHelp sets the help text to display in the footer
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized ScreenLayout ready for use.
//
// Side effects:
//   - None.
func (sl *ScreenLayout) WithHelp(helpText string) *ScreenLayout {
	sl.ShowFooter = true
	sl.HelpText = helpText
	return sl
}

// WithInput sets the input prompt line rendered in the footer above the separator.
//
// Expected:
//   - input is the current user input line (e.g., "> hello world").
//
// Returns:
//   - The updated ScreenLayout for method chaining.
//
// Side effects:
//   - Sets InputLine on the ScreenLayout.
func (sl *ScreenLayout) WithInput(input string) *ScreenLayout {
	sl.InputLine = input
	return sl
}

// WithStatusBar sets the status bar content rendered in the footer between
// the input line and the help text.
//
// Expected:
//   - content is a pre-rendered status bar string.
//
// Returns:
//   - The updated ScreenLayout for method chaining.
//
// Side effects:
//   - Sets StatusBarContent on the ScreenLayout.
func (sl *ScreenLayout) WithStatusBar(content string) *ScreenLayout {
	sl.StatusBarContent = content
	return sl
}

// WithFooterSeparator enables/disables the footer separator line
//
// Expected:
//   - bool must be valid.
//
// Returns:
//   - A fully initialized ScreenLayout ready for use.
//
// Side effects:
//   - None.
func (sl *ScreenLayout) WithFooterSeparator(show bool) *ScreenLayout {
	sl.ShowFooterSeparator = show
	return sl
}

// ShowModalOverlay displays a modal overlay on top of the content.
//
// Expected:
//   - modalrenderer must be valid.
//
// Returns:
//   - A fully initialized ScreenLayout ready for use.
//
// Side effects:
//   - None.
func (sl *ScreenLayout) ShowModalOverlay(modal ModalRenderer) *ScreenLayout {
	sl.ShowModal = true
	sl.Modal = modal
	return sl
}

// SetUseFullWidth sets whether to use full terminal width for content
//
// Expected:
//   - bool must be valid.
//
// Returns:
//   - A fully initialized ScreenLayout ready for use.
//
// Side effects:
//   - None.
func (sl *ScreenLayout) SetUseFullWidth(full bool) *ScreenLayout {
	sl.UseFullWidth = full
	return sl
}

// WithTheme sets the theme for the view
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized ScreenLayout ready for use.
//
// Side effects:
//   - None.
func (sl *ScreenLayout) WithTheme(theme themes.Theme) *ScreenLayout {
	sl.theme = theme
	return sl
}

// buildHeaderParts constructs the header section parts (logo, breadcrumbs, title/subtitle).
// This is shared by both GetAvailableContentHeight and Render to ensure consistent layout calculations.
//
// Expected:
//   - theme must be a valid theme instance.
//
// Returns:
//   - A slice of rendered strings for the header area.
//
// Side effects:
//   - May mutate sl.Logo width via SetWidth.
func (sl *ScreenLayout) buildHeaderParts(theme themes.Theme) []string {
	var parts []string

	if sl.ShowLogo && sl.Logo != nil {
		for range sl.LogoSpacing {
			parts = append(parts, "")
		}
		sl.Logo.SetWidth(sl.TerminalInfo.Width)
		logoOutput := sl.Logo.ViewStatic()
		parts = append(parts, logoOutput, "")
	}

	if sl.ShowHeader {
		if len(sl.Breadcrumbs) > 0 {
			crumbs := make([]navigation.Breadcrumb, len(sl.Breadcrumbs))
			for i, label := range sl.Breadcrumbs {
				intent := strings.ToLower(strings.ReplaceAll(label, " ", "_"))
				crumbs[i] = navigation.Breadcrumb{
					Label:  label,
					Icon:   navigation.GetIconForIntent(intent),
					Intent: intent,
				}
			}
			bar := navigation.NewBreadcrumbBar(sl.TerminalInfo.Width, false).
				WithTheme(theme)
			bar.SetCrumbs(crumbs)
			breadcrumbOutput := bar.View()
			parts = append(parts, breadcrumbOutput, "")
		}

		if sl.Title != "" {
			titleStyle := lipgloss.NewStyle().
				Foreground(theme.ForegroundColor()).
				Bold(true)
			styledTitle := titleStyle.Render(sl.Title)
			parts = append(parts, styledTitle)

			if sl.Subtitle != "" {
				subtitleStyle := lipgloss.NewStyle().
					Foreground(theme.MutedColor())
				styledSubtitle := subtitleStyle.Render(sl.Subtitle)
				parts = append(parts, styledSubtitle)
			}

			parts = append(parts, "")
		}
	}

	return parts
}

// buildFooterParts constructs the footer section parts (separator, input, status bar, help).
// This is shared by both GetAvailableContentHeight and Render to ensure consistent layout calculations.
//
// Expected:
//   - theme must be a valid theme instance.
//
// Returns:
//   - A slice of rendered strings for the footer area.
//
// Side effects:
//   - None.
func (sl *ScreenLayout) buildFooterParts(theme themes.Theme) []string {
	var parts []string

	hasFooterContent := sl.HelpText != "" || sl.InputLine != "" || sl.StatusBarContent != ""
	if sl.ShowFooter && sl.ShowFooterSeparator && hasFooterContent {
		separator := strings.Repeat("─", 100)
		separatorStyle := lipgloss.NewStyle().Foreground(theme.BorderColor())
		parts = append(parts, "", separatorStyle.Render(separator))
	}

	if sl.InputLine != "" {
		inputStyle := lipgloss.NewStyle().Foreground(theme.PrimaryColor())
		parts = append(parts, inputStyle.Render(sl.InputLine))
	}

	if sl.StatusBarContent != "" {
		parts = append(parts, sl.StatusBarContent)
	}

	if sl.ShowFooter && sl.HelpText != "" {
		helpStyle := lipgloss.NewStyle().Foreground(theme.MutedColor())
		parts = append(parts, helpStyle.Render(sl.HelpText))
	}

	return parts
}

// GetAvailableContentHeight calculates the height available for content between header and footer.
//
// Returns:
//   - A int value.
//
// Side effects:
//   - None.
func (sl *ScreenLayout) GetAvailableContentHeight() int {
	theme := sl.getTheme()

	headerParts := sl.buildHeaderParts(theme)
	footerParts := sl.buildFooterParts(theme)

	header := lipgloss.JoinVertical(lipgloss.Left, headerParts...)
	footer := lipgloss.JoinVertical(lipgloss.Left, footerParts...)

	headerHeight := lipgloss.Height(header)
	footerHeight := lipgloss.Height(footer)

	availableHeight := sl.TerminalInfo.Height - headerHeight - footerHeight

	if availableHeight < 1 {
		availableHeight = 1
	}

	return availableHeight
}

// Render renders the complete view with all components.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (sl *ScreenLayout) Render() string {
	theme := sl.getTheme()

	headerParts := sl.buildHeaderParts(theme)
	footerParts := sl.buildFooterParts(theme)

	header := lipgloss.JoinVertical(lipgloss.Left, headerParts...)
	footer := lipgloss.JoinVertical(lipgloss.Left, footerParts...)

	headerHeight := lipgloss.Height(header)
	footerHeight := lipgloss.Height(footer)

	contentAreaHeight := sl.TerminalInfo.Height - headerHeight - footerHeight
	if contentAreaHeight < 1 {
		contentAreaHeight = 1
	}

	contentToRender := sl.Content
	if contentToRender != "" {
		hasStyle := sl.ContentStyle.GetBackground() != lipgloss.NoColor{} || sl.ContentStyle.GetForeground() != lipgloss.NoColor{}
		if hasStyle {
			contentToRender = sl.ContentStyle.Render(sl.Content)
		}
	}

	contentStyle := lipgloss.NewStyle().
		Width(sl.TerminalInfo.Width).
		Height(contentAreaHeight).
		MaxHeight(contentAreaHeight)
	contentView := contentStyle.Render(contentToRender)

	var allParts []string
	if header != "" {
		allParts = append(allParts, header)
	}
	allParts = append(allParts, contentView)
	if footer != "" {
		allParts = append(allParts, footer)
	}

	combined := lipgloss.JoinVertical(lipgloss.Left, allParts...)

	rendered := lipgloss.Place(sl.TerminalInfo.Width, sl.TerminalInfo.Height,
		lipgloss.Left, lipgloss.Top, combined)

	if sl.ShowModal && sl.Modal != nil {
		dimmedContent := sl.dimContent(rendered)
		modalOutput := sl.Modal.Render(sl.TerminalInfo.Width, sl.TerminalInfo.Height)
		rendered = sl.overlayModal(dimmedContent, modalOutput)
	}

	return rendered
}

// dimContent applies a dimming effect to the content.
// Uses Faint(true) for consistency with feedback.DimContent().
//
// Expected:
//   - content is a rendered string.
//
// Returns:
//   - The content with a faint styling applied.
//
// Side effects:
//   - None.
func (sl *ScreenLayout) dimContent(content string) string {
	dimStyle := lipgloss.NewStyle().
		Faint(true)
	return dimStyle.Render(content)
}

// overlayModal overlays modal content on top of background content.
//
// Expected:
//   - background and modal are rendered multi-line strings.
//
// Returns:
//   - The background with modal lines overlaid at the vertical centre.
//
// Side effects:
//   - None.
func (sl *ScreenLayout) overlayModal(background, modal string) string {
	bgLines := strings.Split(background, "\n")
	modalLines := strings.Split(modal, "\n")

	bgHeight := len(bgLines)
	modalHeight := len(modalLines)
	startLine := (bgHeight - modalHeight) / 2
	if startLine < 0 {
		startLine = 0
	}

	result := make([]string, len(bgLines))
	copy(result, bgLines)

	for i, modalLine := range modalLines {
		lineIndex := startLine + i
		if lineIndex >= 0 && lineIndex < len(result) {
			centeredModalLine := lipgloss.PlaceHorizontal(sl.TerminalInfo.Width, lipgloss.Center, modalLine)
			result[lineIndex] = centeredModalLine
		}
	}

	return strings.Join(result, "\n")
}
