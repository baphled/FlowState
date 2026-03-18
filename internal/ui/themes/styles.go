package themes

import (
	"github.com/charmbracelet/lipgloss"
)

// StyleSet contains pre-composed Lipgloss styles generated from the theme's palette.
// These styles provide consistent styling across all UI components.
type StyleSet struct {
	// Buttons
	ButtonBase      lipgloss.Style
	ButtonPrimary   lipgloss.Style
	ButtonSecondary lipgloss.Style
	ButtonFocused   lipgloss.Style
	ButtonDisabled  lipgloss.Style

	// Inputs
	InputBase    lipgloss.Style
	InputFocused lipgloss.Style
	InputError   lipgloss.Style
	InputLabel   lipgloss.Style
	InputHint    lipgloss.Style

	// Cards/Panels
	CardBase    lipgloss.Style
	CardHeader  lipgloss.Style
	CardContent lipgloss.Style
	CardFooter  lipgloss.Style

	// Lists
	ListItem         lipgloss.Style
	ListItemSelected lipgloss.Style
	ListItemFocused  lipgloss.Style

	// Messages
	ErrorBox   lipgloss.Style
	WarningBox lipgloss.Style
	SuccessBox lipgloss.Style
	InfoBox    lipgloss.Style

	// Headers
	HeaderMain       lipgloss.Style
	HeaderSection    lipgloss.Style
	HeaderSubsection lipgloss.Style

	// Progress
	ProgressBar  lipgloss.Style
	ProgressText lipgloss.Style

	// Badges/Tags
	Badge         lipgloss.Style
	BadgeSelected lipgloss.Style
	Tag           lipgloss.Style
	TagSelected   lipgloss.Style

	// Key Badges
	KeyBadge     lipgloss.Style
	KeyBadgeHint lipgloss.Style

	// Modal styles
	ModalBase        lipgloss.Style
	ModalTitle       lipgloss.Style
	ModalMessage     lipgloss.Style
	ModalDestructive lipgloss.Style

	// Text styles
	ErrorText   lipgloss.Style
	WarningText lipgloss.Style
	SuccessText lipgloss.Style
	InfoText    lipgloss.Style
	MutedText   lipgloss.Style
}

// GenerateStyles creates a complete StyleSet from the given color palette.
//
// Expected:
//   - colorpalette must be valid.
//
// Returns:
//   - A fully initialized StyleSet ready for use.
//
// Side effects:
//   - None.
//
//nolint:funlen // Vendored from KaRiya; complexity acceptable for style generation
func GenerateStyles(palette *ColorPalette) *StyleSet {
	if palette == nil {
		return &StyleSet{}
	}

	return &StyleSet{
		// Buttons
		ButtonBase: lipgloss.NewStyle().
			Padding(0, 3).
			MarginRight(2).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(palette.Border),

		ButtonPrimary: lipgloss.NewStyle().
			Padding(0, 3).
			MarginRight(2).
			Foreground(palette.Foreground).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(palette.Primary).
			Background(palette.BackgroundCard),

		ButtonSecondary: lipgloss.NewStyle().
			Padding(0, 3).
			MarginRight(2).
			Foreground(palette.ForegroundDim).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(palette.Border).
			Background(palette.Background),

		ButtonFocused: lipgloss.NewStyle().
			Padding(0, 3).
			MarginRight(2).
			Foreground(palette.Foreground).
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(palette.Primary).
			Background(palette.BackgroundCard).
			Bold(true),

		ButtonDisabled: lipgloss.NewStyle().
			Padding(0, 3).
			MarginRight(2).
			Foreground(palette.ForegroundMuted).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(palette.Border).
			Faint(true),

		// Inputs
		InputBase: lipgloss.NewStyle().
			Padding(0, 1).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(palette.Border).
			Background(palette.BackgroundCard),

		InputFocused: lipgloss.NewStyle().
			Padding(0, 1).
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(palette.BorderActive).
			Background(palette.BackgroundCard),

		InputError: lipgloss.NewStyle().
			Padding(0, 1).
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(palette.BorderError).
			Background(palette.BackgroundCard),

		InputLabel: lipgloss.NewStyle().
			Foreground(palette.ForegroundDim).
			Bold(true).
			MarginBottom(1),

		InputHint: lipgloss.NewStyle().
			Foreground(palette.ForegroundMuted).
			Italic(true).
			MarginTop(1),

		// Cards/Panels
		CardBase: lipgloss.NewStyle().
			Padding(1, 2).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(palette.Border).
			Background(palette.BackgroundCard),

		CardHeader: lipgloss.NewStyle().
			Foreground(palette.Foreground).
			Bold(true).
			MarginBottom(1),

		CardContent: lipgloss.NewStyle().
			Foreground(palette.Foreground),

		CardFooter: lipgloss.NewStyle().
			Foreground(palette.ForegroundDim).
			MarginTop(1),

		// Lists
		ListItem: lipgloss.NewStyle().
			Padding(0, 1).
			Foreground(palette.Foreground),

		ListItemSelected: lipgloss.NewStyle().
			Padding(0, 1).
			Foreground(palette.Foreground).
			Background(palette.Selection).
			Bold(true),

		ListItemFocused: lipgloss.NewStyle().
			Padding(0, 1).
			Foreground(palette.Foreground).
			BorderLeft(true).
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(palette.Primary),

		// Messages
		ErrorBox: lipgloss.NewStyle().
			Padding(1, 2).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(palette.BorderError).
			Background(palette.BackgroundCard).
			Foreground(palette.Error),

		WarningBox: lipgloss.NewStyle().
			Padding(1, 2).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(palette.Warning).
			Background(palette.BackgroundCard).
			Foreground(palette.Warning),

		SuccessBox: lipgloss.NewStyle().
			Padding(1, 2).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(palette.Success).
			Background(palette.BackgroundCard).
			Foreground(palette.Success),

		InfoBox: lipgloss.NewStyle().
			Padding(1, 2).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(palette.Info).
			Background(palette.BackgroundCard).
			Foreground(palette.Info),

		// Headers
		HeaderMain: lipgloss.NewStyle().
			Foreground(palette.Primary).
			Bold(true).
			Padding(1, 0).
			MarginBottom(1),

		HeaderSection: lipgloss.NewStyle().
			Foreground(palette.Foreground).
			Bold(true).
			Padding(0, 0).
			MarginTop(1).
			MarginBottom(1),

		HeaderSubsection: lipgloss.NewStyle().
			Foreground(palette.ForegroundDim).
			Bold(true).
			MarginBottom(1),

		// Progress
		ProgressBar: lipgloss.NewStyle().
			Foreground(palette.Primary).
			Background(palette.BackgroundAlt),

		ProgressText: lipgloss.NewStyle().
			Foreground(palette.ForegroundDim).
			MarginLeft(2),

		// Badges/Tags
		Badge: lipgloss.NewStyle().
			Padding(0, 1).
			MarginRight(1).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(palette.Border).
			Background(palette.BackgroundAlt).
			Foreground(palette.ForegroundDim),

		BadgeSelected: lipgloss.NewStyle().
			Padding(0, 1).
			MarginRight(1).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(palette.Tertiary).
			Background(palette.Tertiary).
			Foreground(palette.Foreground).
			Bold(true),

		Tag: lipgloss.NewStyle().
			Padding(0, 1).
			MarginRight(1).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(palette.Border).
			Background(palette.BackgroundAlt).
			Foreground(palette.ForegroundDim),

		TagSelected: lipgloss.NewStyle().
			Padding(0, 1).
			MarginRight(1).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(palette.Tertiary).
			Background(palette.Tertiary).
			Foreground(palette.Foreground).
			Bold(true),

		// Key Badges
		KeyBadge: lipgloss.NewStyle().
			Padding(0, 1).
			Background(palette.BackgroundAlt).
			Foreground(palette.Primary).
			Bold(true),

		KeyBadgeHint: lipgloss.NewStyle().
			Foreground(palette.ForegroundDim),

		// Modal styles
		ModalBase: lipgloss.NewStyle().
			Padding(1, 2).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(palette.Border).
			Background(palette.BackgroundCard),

		ModalTitle: lipgloss.NewStyle().
			Foreground(palette.Foreground).
			Bold(true).
			MarginBottom(1),

		ModalMessage: lipgloss.NewStyle().
			Foreground(palette.Foreground).
			MarginBottom(2),

		ModalDestructive: lipgloss.NewStyle().
			Padding(1, 2).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(palette.Error).
			Background(palette.BackgroundCard),

		// Text styles
		ErrorText: lipgloss.NewStyle().
			Foreground(palette.Error).
			Bold(true),

		WarningText: lipgloss.NewStyle().
			Foreground(palette.Warning).
			Bold(true),

		SuccessText: lipgloss.NewStyle().
			Foreground(palette.Success).
			Bold(true),

		InfoText: lipgloss.NewStyle().
			Foreground(palette.Info).
			Bold(true),

		MutedText: lipgloss.NewStyle().
			Foreground(palette.ForegroundMuted),
	}
}
