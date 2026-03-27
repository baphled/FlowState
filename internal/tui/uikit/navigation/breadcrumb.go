// Package navigation provides navigation components for the UIKit.
// These components handle breadcrumbs, menus, and navigation bars.
package navigation

import (
	"strings"

	"github.com/baphled/flowstate/internal/ui/themes"
	"github.com/charmbracelet/lipgloss"
)

// Breadcrumb represents a single breadcrumb in the navigation trail.
type Breadcrumb struct {
	Label  string
	Icon   string
	Intent string // For future navigation support
}

// BreadcrumbBar renders a navigation breadcrumb trail with icons.
// It supports theme-aware styling and optional boxing.
//
// Usage:
//
//	bar := navigation.NewBreadcrumbBar(80, false).
//	    WithTheme(theme).
//	    AddCrumb(navigation.Breadcrumb{Label: "Home", Icon: IconHome}).
//	    AddCrumb(navigation.Breadcrumb{Label: "Settings", Icon: IconConfigure})
//	rendered := bar.View()
type BreadcrumbBar struct {
	crumbs   []Breadcrumb
	width    int
	boxed    bool
	showIcon bool
	theme    themes.Theme
}

// Icon constants for different intents.
const (
	IconHome         = "🏠"
	IconCaptureEvent = "✏️"
	IconTimeline     = "📅"
	IconGenerateCV   = "📄"
	IconExport       = "💾"
	IconConfigure    = "⚙️"
	IconBursts       = "📊"
	IconFacts        = "💡"
	IconImport       = "📥"
	IconMetadata     = "🏷️"
	IconBulkOps      = "📦"
	IconSearch       = "🔍"
	IconFilter       = "🔎"
	IconSort         = "↕️"
	IconEdit         = "✎"
	IconDelete       = "🗑️"
	IconSave         = "💾"
	IconCancel       = "❌"
	IconConfirm      = "✓"
)

// NewBreadcrumbBar creates a new breadcrumb bar.
//
// Expected:
//   - int must be valid.
//   - bool must be valid.
//
// Returns:
//   - A fully initialized BreadcrumbBar ready for use.
//
// Side effects:
//   - None.
func NewBreadcrumbBar(width int, boxed bool) *BreadcrumbBar {
	return &BreadcrumbBar{
		crumbs:   []Breadcrumb{},
		width:    width,
		boxed:    boxed,
		showIcon: true,
	}
}

// WithTheme sets the theme for the breadcrumb bar.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized BreadcrumbBar ready for use.
//
// Side effects:
//   - None.
func (b *BreadcrumbBar) WithTheme(theme themes.Theme) *BreadcrumbBar {
	b.theme = theme
	return b
}

// SetCrumbs sets the breadcrumb trail.
//
// Expected:
//   - []breadcrumb must be valid.
//
// Returns:
//   - A fully initialized BreadcrumbBar ready for use.
//
// Side effects:
//   - None.
func (b *BreadcrumbBar) SetCrumbs(crumbs []Breadcrumb) *BreadcrumbBar {
	b.crumbs = crumbs
	return b
}

// AddCrumb adds a breadcrumb to the trail.
//
// Expected:
//   - breadcrumb must be valid.
//
// Returns:
//   - A fully initialized BreadcrumbBar ready for use.
//
// Side effects:
//   - None.
func (b *BreadcrumbBar) AddCrumb(crumb Breadcrumb) *BreadcrumbBar {
	b.crumbs = append(b.crumbs, crumb)
	return b
}

// SetWidth sets the width for the breadcrumb bar.
//
// Expected:
//   - int must be valid.
//
// Returns:
//   - A fully initialized BreadcrumbBar ready for use.
//
// Side effects:
//   - None.
func (b *BreadcrumbBar) SetWidth(width int) *BreadcrumbBar {
	b.width = width
	return b
}

// SetBoxed sets whether to show a box around the breadcrumbs.
//
// Expected:
//   - bool must be valid.
//
// Returns:
//   - A fully initialized BreadcrumbBar ready for use.
//
// Side effects:
//   - None.
func (b *BreadcrumbBar) SetBoxed(boxed bool) *BreadcrumbBar {
	b.boxed = boxed
	return b
}

// ShowIcons controls icon visibility.
//
// Expected:
//   - bool must be valid.
//
// Returns:
//   - A fully initialized BreadcrumbBar ready for use.
//
// Side effects:
//   - None.
func (b *BreadcrumbBar) ShowIcons(show bool) *BreadcrumbBar {
	b.showIcon = show
	return b
}

// getTheme returns the theme or a default.
//
// Returns:
//   - The assigned theme, or a default theme if none is set.
//
// Side effects:
//   - None.
func (b *BreadcrumbBar) getTheme() themes.Theme {
	if b.theme != nil {
		return b.theme
	}
	return themes.NewDefaultTheme()
}

// View renders the breadcrumb bar.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (b *BreadcrumbBar) View() string {
	if len(b.crumbs) == 0 {
		return ""
	}

	theme := b.getTheme()

	separator := "  ▸  "
	var parts []string

	for i, crumb := range b.crumbs {
		var part string

		if b.showIcon && crumb.Icon != "" {
			part = crumb.Icon + " " + crumb.Label
		} else {
			part = crumb.Label
		}

		if i == len(b.crumbs)-1 {
			styledPart := lipgloss.NewStyle().
				Foreground(theme.AccentColor()).
				Bold(true).
				Render(part)
			parts = append(parts, styledPart)
		} else {
			styledPart := lipgloss.NewStyle().
				Foreground(theme.MutedColor()).
				Render(part)
			parts = append(parts, styledPart)
		}
	}

	breadcrumbStr := strings.Join(parts, separator)

	visualWidth := lipgloss.Width(breadcrumbStr)
	if visualWidth > b.width-4 && len(b.crumbs) > 2 {
		breadcrumbStr = b.renderTruncated(theme)
	}

	if b.boxed {
		boxStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(theme.BorderColor()).
			Padding(0, 1)

		return boxStyle.Render(breadcrumbStr)
	}

	return breadcrumbStr
}

// renderTruncated renders a truncated version of breadcrumbs.
//
// Expected:
//   - theme must be a valid theme instance.
//
// Returns:
//   - A rendered breadcrumb string showing first and last items with ellipsis.
//
// Side effects:
//   - None.
func (b *BreadcrumbBar) renderTruncated(theme themes.Theme) string {
	if len(b.crumbs) < 2 {
		return b.View()
	}

	first := b.crumbs[0]
	last := b.crumbs[len(b.crumbs)-1]

	var firstPart, lastPart string

	if b.showIcon && first.Icon != "" {
		firstPart = first.Icon + " " + first.Label
	} else {
		firstPart = first.Label
	}
	firstPart = lipgloss.NewStyle().
		Foreground(theme.MutedColor()).
		Render(firstPart)

	if b.showIcon && last.Icon != "" {
		lastPart = last.Icon + " " + last.Label
	} else {
		lastPart = last.Label
	}
	lastPart = lipgloss.NewStyle().
		Foreground(theme.AccentColor()).
		Bold(true).
		Render(lastPart)

	ellipsis := lipgloss.NewStyle().
		Foreground(theme.MutedColor()).
		Render("...")

	return firstPart + "  ▸  " + ellipsis + "  ▸  " + lastPart
}

// GetIconForIntent returns the appropriate icon for a given intent name.
//
// Expected:
//   - Must be a valid string.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func GetIconForIntent(intent string) string {
	iconMap := map[string]string{
		"home":              IconHome,
		"capture_event":     IconCaptureEvent,
		"browse_timeline":   IconTimeline,
		"generate_cv":       IconGenerateCV,
		"export_artifact":   IconExport,
		"configure_system":  IconConfigure,
		"burst_management":  IconBursts,
		"fact_management":   IconFacts,
		"import_wizard":     IconImport,
		"review_enrichment": IconMetadata,
		"bulk_operations":   IconBulkOps,
		"search":            IconSearch,
		"filter":            IconFilter,
		"sort":              IconSort,
		"edit":              IconEdit,
		"delete":            IconDelete,
		"save":              IconSave,
		"cancel":            IconCancel,
		"confirm":           IconConfirm,
	}

	if icon, ok := iconMap[intent]; ok {
		return icon
	}
	return ""
}

// CreateBreadcrumbTrail is a helper to create a breadcrumb trail.
//
// Expected:
//   - trail must be a valid slice of breadcrumb items.
//
// Returns:
//   - A []Breadcrumb value ready for use.
//
// Side effects:
//   - None.
func CreateBreadcrumbTrail(trail ...struct {
	Label  string
	Intent string
}) []Breadcrumb {
	crumbs := make([]Breadcrumb, len(trail))
	for i, item := range trail {
		crumbs[i] = Breadcrumb{
			Label:  item.Label,
			Icon:   GetIconForIntent(item.Intent),
			Intent: item.Intent,
		}
	}
	return crumbs
}
