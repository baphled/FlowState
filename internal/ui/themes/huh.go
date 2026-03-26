package themes

import (
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// GenerateHuhTheme creates a huh.Theme that matches the active KaRiya theme.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized huh.Theme ready for use.
//
// Side effects:
//   - None.
func GenerateHuhTheme(theme Theme) *huh.Theme {
	if theme == nil {
		return huh.ThemeCatppuccin()
	}

	palette := theme.Palette()

	textInputStyles := huh.TextInputStyles{
		Cursor:      lipgloss.NewStyle().Foreground(palette.Primary),
		Placeholder: lipgloss.NewStyle().Foreground(palette.ForegroundMuted),
		Prompt:      lipgloss.NewStyle().Foreground(palette.Primary),
		Text:        lipgloss.NewStyle().Foreground(palette.Foreground),
	}

	// Focused field styles
	focusedStyles := huh.FieldStyles{
		Base: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(palette.BorderActive).
			Padding(0, 1),
		Title: lipgloss.NewStyle().
			Foreground(palette.Primary).
			Bold(true),
		Description: lipgloss.NewStyle().
			Foreground(palette.ForegroundDim),
		ErrorIndicator: lipgloss.NewStyle().
			Foreground(palette.Error),
		ErrorMessage: lipgloss.NewStyle().
			Foreground(palette.Error),

		// Select styles
		SelectSelector: lipgloss.NewStyle().
			Foreground(palette.Primary).
			Bold(true),
		Option: lipgloss.NewStyle().
			Foreground(palette.Foreground),
		NextIndicator: lipgloss.NewStyle().
			Foreground(palette.ForegroundMuted),
		PrevIndicator: lipgloss.NewStyle().
			Foreground(palette.ForegroundMuted),

		// Multi-select styles
		MultiSelectSelector: lipgloss.NewStyle().
			Foreground(palette.Primary),
		SelectedOption: lipgloss.NewStyle().
			Foreground(palette.Primary).
			Bold(true),
		SelectedPrefix: lipgloss.NewStyle().
			Foreground(palette.Success),
		UnselectedOption: lipgloss.NewStyle().
			Foreground(palette.Foreground),
		UnselectedPrefix: lipgloss.NewStyle().
			Foreground(palette.ForegroundMuted),

		// Text input styles
		TextInput: textInputStyles,

		// Confirm button styles
		FocusedButton: lipgloss.NewStyle().
			Foreground(palette.Background).
			Background(palette.Primary).
			Padding(0, 1).
			Bold(true),
		BlurredButton: lipgloss.NewStyle().
			Foreground(palette.ForegroundDim).
			Background(palette.BackgroundAlt).
			Padding(0, 1),

		// Card styles
		Card: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(palette.Border).
			Padding(1, 2),
		NoteTitle: lipgloss.NewStyle().
			Foreground(palette.Primary).
			Bold(true),
		Next: lipgloss.NewStyle().
			Foreground(palette.Primary),
	}

	// Blurred field styles (less prominent)
	blurredStyles := huh.FieldStyles{
		Base: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(palette.Border).
			Padding(0, 1),
		Title: lipgloss.NewStyle().
			Foreground(palette.ForegroundDim),
		Description: lipgloss.NewStyle().
			Foreground(palette.ForegroundMuted),
		ErrorIndicator: lipgloss.NewStyle().
			Foreground(palette.Error),
		ErrorMessage: lipgloss.NewStyle().
			Foreground(palette.Error),

		// Select styles (blurred)
		SelectSelector: lipgloss.NewStyle().
			Foreground(palette.ForegroundDim),
		Option: lipgloss.NewStyle().
			Foreground(palette.ForegroundDim),
		NextIndicator: lipgloss.NewStyle().
			Foreground(palette.ForegroundMuted),
		PrevIndicator: lipgloss.NewStyle().
			Foreground(palette.ForegroundMuted),

		// Multi-select styles (blurred)
		MultiSelectSelector: lipgloss.NewStyle().
			Foreground(palette.ForegroundDim),
		SelectedOption: lipgloss.NewStyle().
			Foreground(palette.ForegroundDim),
		SelectedPrefix: lipgloss.NewStyle().
			Foreground(palette.ForegroundMuted),
		UnselectedOption: lipgloss.NewStyle().
			Foreground(palette.ForegroundMuted),
		UnselectedPrefix: lipgloss.NewStyle().
			Foreground(palette.ForegroundMuted),

		// Text input styles (blurred)
		TextInput: huh.TextInputStyles{
			Cursor:      lipgloss.NewStyle().Foreground(palette.ForegroundDim),
			Placeholder: lipgloss.NewStyle().Foreground(palette.ForegroundMuted),
			Prompt:      lipgloss.NewStyle().Foreground(palette.ForegroundDim),
			Text:        lipgloss.NewStyle().Foreground(palette.ForegroundDim),
		},

		// Confirm button styles (blurred)
		FocusedButton: lipgloss.NewStyle().
			Foreground(palette.ForegroundDim).
			Background(palette.BackgroundAlt).
			Padding(0, 1),
		BlurredButton: lipgloss.NewStyle().
			Foreground(palette.ForegroundMuted).
			Background(palette.BackgroundAlt).
			Padding(0, 1),

		// Card styles (blurred)
		Card: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(palette.Border).
			Padding(1, 2),
		NoteTitle: lipgloss.NewStyle().
			Foreground(palette.ForegroundDim),
		Next: lipgloss.NewStyle().
			Foreground(palette.ForegroundDim),
	}

	// Form styles
	formStyles := huh.FormStyles{
		Base: lipgloss.NewStyle(),
	}

	// Group styles
	groupStyles := huh.GroupStyles{
		Base: lipgloss.NewStyle(),
		Title: lipgloss.NewStyle().
			Foreground(palette.Primary).
			Bold(true).
			Padding(0, 0, 1, 0),
		Description: lipgloss.NewStyle().
			Foreground(palette.ForegroundDim).
			Padding(0, 0, 1, 0),
	}

	// Help styles
	helpStyles := help.Styles{
		ShortKey: lipgloss.NewStyle().
			Foreground(palette.Primary).
			Bold(true),
		ShortDesc: lipgloss.NewStyle().
			Foreground(palette.ForegroundDim),
		ShortSeparator: lipgloss.NewStyle().
			Foreground(palette.ForegroundMuted),
		FullKey: lipgloss.NewStyle().
			Foreground(palette.Primary),
		FullDesc: lipgloss.NewStyle().
			Foreground(palette.Foreground),
		FullSeparator: lipgloss.NewStyle().
			Foreground(palette.ForegroundMuted),
	}

	return &huh.Theme{
		Form:           formStyles,
		Group:          groupStyles,
		FieldSeparator: lipgloss.NewStyle().Margin(1, 0),
		Blurred:        blurredStyles,
		Focused:        focusedStyles,
		Help:           helpStyles,
	}
}

// NewThemedForm creates a new huh.Form with the given theme.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//   - group must be valid.
//
// Returns:
//   - A fully initialized huh.Form ready for use.
//
// Side effects:
//   - None.
func NewThemedForm(theme Theme, groups ...*huh.Group) *huh.Form {
	return huh.NewForm(groups...).WithTheme(GenerateHuhTheme(theme))
}
