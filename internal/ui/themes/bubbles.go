package themes

import (
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
)

// NewThemedListStyles creates theme-aware list.Styles for use with bubbles/list.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A list.Styles value.
//
// Side effects:
//   - None.
func NewThemedListStyles(theme Theme) list.Styles {
	// Return default styles if theme is nil
	if theme == nil {
		return list.DefaultStyles()
	}

	palette := theme.Palette()
	styles := list.DefaultStyles()

	// Style the title bar
	styles.TitleBar = lipgloss.NewStyle().
		Foreground(palette.Foreground).
		Background(palette.BackgroundCard).
		Padding(0, 1)

	// Style the title
	styles.Title = lipgloss.NewStyle().
		Foreground(palette.Primary).
		Bold(true)

	// Style the spinner
	styles.Spinner = lipgloss.NewStyle().
		Foreground(palette.Primary)

	// Style the filter prompt
	styles.FilterPrompt = lipgloss.NewStyle().
		Foreground(palette.Primary)

	// Style the filter cursor
	styles.FilterCursor = lipgloss.NewStyle().
		Foreground(palette.Primary)

	// Style the status bar
	styles.StatusBar = lipgloss.NewStyle().
		Foreground(palette.ForegroundDim)

	// Style the status empty message
	styles.StatusEmpty = lipgloss.NewStyle().
		Foreground(palette.ForegroundMuted)

	// Style the no items message
	styles.NoItems = lipgloss.NewStyle().
		Foreground(palette.ForegroundMuted)

	// Style the pagination
	styles.PaginationStyle = lipgloss.NewStyle().
		Foreground(palette.ForegroundDim)

	// Style the active pagination dot
	styles.ActivePaginationDot = lipgloss.NewStyle().
		Foreground(palette.Primary)

	// Style the inactive pagination dot
	styles.InactivePaginationDot = lipgloss.NewStyle().
		Foreground(palette.ForegroundMuted)

	// Style the help
	styles.HelpStyle = lipgloss.NewStyle().
		Foreground(palette.ForegroundDim)

	return styles
}

// NewThemedTableStyles creates theme-aware table.Styles for use with bubbles/table.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A table.Styles value.
//
// Side effects:
//   - None.
func NewThemedTableStyles(theme Theme) table.Styles {
	// Return default styles if theme is nil
	if theme == nil {
		return table.DefaultStyles()
	}

	palette := theme.Palette()
	styles := table.DefaultStyles()

	// Style the header row
	styles.Header = lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(palette.Border).
		BorderBottom(true).
		Bold(true).
		Foreground(palette.Primary).
		Padding(0, 1)

	// Style the selected row
	styles.Selected = lipgloss.NewStyle().
		Foreground(palette.Foreground).
		Background(palette.Selection).
		Bold(true).
		Padding(0, 1)

	// Style the cells
	styles.Cell = lipgloss.NewStyle().
		Foreground(palette.Foreground).
		Padding(0, 1)

	return styles
}

// NewThemedProgress creates a theme-aware progress.Model for use with bubbles/progress.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A progress.Model value.
//
// Side effects:
//   - None.
func NewThemedProgress(theme Theme) progress.Model {
	// Create default progress model if theme is nil
	if theme == nil {
		return progress.New(progress.WithDefaultGradient())
	}

	palette := theme.Palette()

	// Create progress bar with theme-aware gradient
	p := progress.New(
		progress.WithGradient(
			string(palette.Primary),
			string(palette.Secondary),
		),
	)

	return p
}

// NewThemedSpinner creates a theme-aware spinner.Model for use with bubbles/spinner.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A spinner.Model value.
//
// Side effects:
//   - None.
func NewThemedSpinner(theme Theme) spinner.Model {
	s := spinner.New()
	s.Spinner = spinner.Dot

	// Apply theme color if available
	if theme != nil {
		palette := theme.Palette()
		s.Style = lipgloss.NewStyle().Foreground(palette.Primary)
	}

	return s
}

// NewThemedHelpStyles creates theme-aware help.Styles for use with bubbles/help.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A help.Styles value.
//
// Side effects:
//   - None.
func NewThemedHelpStyles(theme Theme) help.Styles {
	// Return default styles if theme is nil
	if theme == nil {
		// Create sensible defaults manually
		return help.Styles{
			ShortKey:       lipgloss.NewStyle().Bold(true),
			ShortDesc:      lipgloss.NewStyle(),
			ShortSeparator: lipgloss.NewStyle(),
			Ellipsis:       lipgloss.NewStyle(),
			FullKey:        lipgloss.NewStyle().Bold(true),
			FullDesc:       lipgloss.NewStyle(),
			FullSeparator:  lipgloss.NewStyle(),
		}
	}

	palette := theme.Palette()

	return help.Styles{
		ShortKey: lipgloss.NewStyle().
			Foreground(palette.Primary).
			Bold(true),
		ShortDesc: lipgloss.NewStyle().
			Foreground(palette.ForegroundDim),
		ShortSeparator: lipgloss.NewStyle().
			Foreground(palette.ForegroundMuted),
		Ellipsis: lipgloss.NewStyle().
			Foreground(palette.ForegroundMuted),
		FullKey: lipgloss.NewStyle().
			Foreground(palette.Primary),
		FullDesc: lipgloss.NewStyle().
			Foreground(palette.Foreground),
		FullSeparator: lipgloss.NewStyle().
			Foreground(palette.ForegroundMuted),
	}
}

// ApplyThemeToList applies theme styling to an existing list.Model.
//
// Expected:
//   - model must be valid.
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A list.Model value.
//
// Side effects:
//   - None.
func ApplyThemeToList(l list.Model, theme Theme) list.Model {
	if theme == nil {
		return l
	}

	l.Styles = NewThemedListStyles(theme)
	return l
}

// ApplyThemeToTable applies theme styling to an existing table.Model.
//
// Expected:
//   - model must be valid.
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A table.Model value.
//
// Side effects:
//   - None.
func ApplyThemeToTable(t table.Model, theme Theme) table.Model {
	if theme == nil {
		return t
	}

	t.SetStyles(NewThemedTableStyles(theme))
	return t
}

// NewThemedListDelegate creates a theme-aware list.DefaultDelegate.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A list.DefaultDelegate value.
//
// Side effects:
//   - None.
func NewThemedListDelegate(theme Theme) list.DefaultDelegate {
	d := list.NewDefaultDelegate()

	if theme == nil {
		return d
	}

	palette := theme.Palette()

	d.Styles.SelectedTitle = lipgloss.NewStyle().
		Foreground(palette.Primary).
		Background(palette.Selection).
		Bold(true).
		Padding(0, 1)

	d.Styles.NormalTitle = lipgloss.NewStyle().
		Foreground(palette.Foreground).
		Padding(0, 1)

	d.Styles.SelectedDesc = lipgloss.NewStyle().
		Foreground(palette.ForegroundDim).
		Background(palette.Selection).
		Padding(0, 1)

	d.Styles.NormalDesc = lipgloss.NewStyle().
		Foreground(palette.ForegroundMuted).
		Padding(0, 1)

	d.Styles.DimmedTitle = lipgloss.NewStyle().
		Foreground(palette.ForegroundMuted).
		Padding(0, 1)

	d.Styles.DimmedDesc = lipgloss.NewStyle().
		Foreground(palette.ForegroundMuted).
		Padding(0, 1)

	return d
}
