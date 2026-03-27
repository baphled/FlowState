// Package layout provides layout components for the UIKit.
// These components handle page structure, headers, footers, and content areas.
package layout

import (
	"strings"

	"github.com/baphled/flowstate/internal/ui/themes"
	"github.com/charmbracelet/lipgloss"
)

// Header renders a consistent screen header with title and subtitle.
// It supports breadcrumb navigation and theme-aware styling.
//
// Usage:
//
//	header := layout.NewHeader("My Title", 80).
//	    WithTheme(theme).
//	    WithSubtitle("Description").
//	    WithBreadcrumbs([]string{"Home", "Settings"})
//	rendered := header.View()
type Header struct {
	title       string
	subtitle    string
	breadcrumbs []string
	width       int
	height      int
	showBorder  bool
	theme       themes.Theme
}

// NewHeader creates a new header with a title.
//
// Expected:
//   - Must be a valid string.
//   - int must be valid.
//
// Returns:
//   - A fully initialized Header ready for use.
//
// Side effects:
//   - None.
func NewHeader(title string, width int) *Header {
	return &Header{
		title:      title,
		subtitle:   "",
		width:      width,
		height:     1,
		showBorder: false,
	}
}

// WithTheme sets the theme for the header.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Header ready for use.
//
// Side effects:
//   - None.
func (h *Header) WithTheme(theme themes.Theme) *Header {
	h.theme = theme
	return h
}

// WithSubtitle sets the subtitle.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized Header ready for use.
//
// Side effects:
//   - None.
func (h *Header) WithSubtitle(subtitle string) *Header {
	h.subtitle = subtitle
	return h
}

// WithBreadcrumbs sets the breadcrumbs.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A fully initialized Header ready for use.
//
// Side effects:
//   - None.
func (h *Header) WithBreadcrumbs(breadcrumbs []string) *Header {
	h.breadcrumbs = breadcrumbs
	return h
}

// WithBorder enables border rendering.
//
// Returns:
//   - A fully initialized Header ready for use.
//
// Side effects:
//   - None.
func (h *Header) WithBorder() *Header {
	h.showBorder = true
	return h
}

// SetWidth sets the header width.
//
// Expected:
//   - int must be valid.
//
// Side effects:
//   - None.
func (h *Header) SetWidth(width int) {
	h.width = width
}

// SetHeight sets the header height.
//
// Expected:
//   - int must be valid.
//
// Side effects:
//   - None.
func (h *Header) SetHeight(height int) {
	h.height = height
}

// GetTitle returns the title.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (h *Header) GetTitle() string {
	return h.title
}

// GetSubtitle returns the subtitle.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (h *Header) GetSubtitle() string {
	return h.subtitle
}

// GetBreadcrumbs returns the breadcrumbs.
//
// Returns:
//   - A []string value.
//
// Side effects:
//   - None.
func (h *Header) GetBreadcrumbs() []string {
	return h.breadcrumbs
}

// AddBreadcrumb adds a single breadcrumb.
//
// Expected:
//   - Must be a valid string.
//
// Side effects:
//   - None.
func (h *Header) AddBreadcrumb(crumb string) {
	h.breadcrumbs = append(h.breadcrumbs, crumb)
}

// ClearBreadcrumbs clears all breadcrumbs.
//
// Side effects:
//   - None.
func (h *Header) ClearBreadcrumbs() {
	h.breadcrumbs = []string{}
}

// getTheme returns the theme or a default.
//
// Returns:
//   - The assigned theme, or a default theme if none is set.
//
// Side effects:
//   - None.
func (h *Header) getTheme() themes.Theme {
	if h.theme != nil {
		return h.theme
	}
	return themes.NewDefaultTheme()
}

// View renders the header.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (h *Header) View() string {
	if h.width <= 0 {
		return ""
	}

	theme := h.getTheme()
	var parts []string

	if len(h.breadcrumbs) > 0 {
		breadcrumbStr := h.renderBreadcrumbs(theme)
		parts = append(parts, breadcrumbStr)
	}

	titleStr := h.renderTitle(theme)
	parts = append(parts, titleStr)

	if h.subtitle != "" {
		subtitleStr := h.renderSubtitle(theme)
		parts = append(parts, subtitleStr)
	}

	content := strings.Join(parts, "\n")

	if h.showBorder {
		borderStyle := lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(theme.BorderColor())
		return borderStyle.Render(content)
	}

	return content
}

// renderTitle renders the main title.
//
// Expected:
//   - theme must be a valid theme instance.
//
// Returns:
//   - A styled title string, truncated if needed.
//
// Side effects:
//   - None.
func (h *Header) renderTitle(theme themes.Theme) string {
	titleStyle := lipgloss.NewStyle().
		Foreground(theme.AccentColor()).
		Bold(true).
		Padding(1, 0).
		MarginBottom(1)

	maxWidth := h.width - 4
	if len(h.title) > maxWidth {
		title := h.title[:maxWidth-3] + "..."
		return titleStyle.Render(title)
	}

	return titleStyle.Render(h.title)
}

// renderSubtitle renders the subtitle.
//
// Expected:
//   - theme must be a valid theme instance.
//
// Returns:
//   - A styled subtitle string, truncated if needed.
//
// Side effects:
//   - None.
func (h *Header) renderSubtitle(theme themes.Theme) string {
	subtitleStyle := lipgloss.NewStyle().
		Foreground(theme.MutedColor()).
		Italic(true).
		MarginTop(1)

	maxWidth := h.width - 4
	if len(h.subtitle) > maxWidth {
		subtitle := h.subtitle[:maxWidth-3] + "..."
		return subtitleStyle.Render(subtitle)
	}

	return subtitleStyle.Render(h.subtitle)
}

// renderBreadcrumbs renders the breadcrumb navigation.
//
// Expected:
//   - theme must be a valid theme instance.
//
// Returns:
//   - A styled breadcrumb trail string joined by separators.
//
// Side effects:
//   - None.
func (h *Header) renderBreadcrumbs(theme themes.Theme) string {
	if len(h.breadcrumbs) == 0 {
		return ""
	}

	breadcrumbStyle := lipgloss.NewStyle().
		Foreground(theme.MutedColor())

	separator := " > "
	return breadcrumbStyle.Render(strings.Join(h.breadcrumbs, separator))
}

// GetClickedBreadcrumbIndex returns the index of the breadcrumb clicked at the given position.
//
// Expected:
//   - int must be valid.
//
// Returns:
//   - A int value.
//
// Side effects:
//   - None.
func (h *Header) GetClickedBreadcrumbIndex(x, _ int) int {
	if len(h.breadcrumbs) == 0 {
		return -1
	}

	separator := " > "
	currentX := 0

	for i, crumb := range h.breadcrumbs {
		crumbLen := len(crumb)
		if x >= currentX && x < currentX+crumbLen {
			return i
		}
		currentX += crumbLen + len(separator)
	}

	return -1
}
