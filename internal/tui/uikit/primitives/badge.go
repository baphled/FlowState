package primitives

import (
	"strings"

	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/charmbracelet/lipgloss"
)

// BadgeVariant defines the visual style and semantic meaning of a badge.
type BadgeVariant int

const (
	// BadgeDefault is for general purpose badges.
	BadgeDefault BadgeVariant = iota
	// BadgeKey is for keyboard shortcuts compact style (e.g., "[Esc → Back]").
	BadgeKey
	// BadgeHelpKey is for help footer shortcuts (e.g., "[Esc] Back" - two-part styling).
	BadgeHelpKey
	// BadgeStatus is for status indicators (e.g., "Active", "Pending").
	BadgeStatus
	// BadgeTag is for tags and labels (e.g., "Feature", "Bug").
	BadgeTag
)

// Badge is a theme-aware badge component for displaying small pieces of information.
// Badges support multiple variants for different use cases.
//
// Example:
//
//	keyBadge := primitives.KeyBadge("Esc", "cancel", theme)
//	statusBadge := primitives.StatusBadge("Active", theme)
//	tagBadge := primitives.TagBadge("Feature", theme)
type Badge struct {
	theme.Aware
	label   string
	value   string
	variant BadgeVariant
}

// NewBadge creates a new badge with the given label and theme.
// If theme is nil, the default theme is used.
// The badge defaults to Default variant.
//
// Expected:
//   - label must be a non-empty string.
//   - th can be nil (uses default theme).
//
// Returns:
//   - A configured Badge instance ready for rendering.
//
// Side effects:
//   - None.
func NewBadge(label string, th theme.Theme) *Badge {
	b := &Badge{
		label:   label,
		value:   "",
		variant: BadgeDefault,
	}
	if th != nil {
		b.SetTheme(th)
	}
	return b
}

// Value sets the value for the badge (used for key badges).
// Returns the badge for method chaining.
//
// Expected:
//   - value can be any string (including empty).
//
// Returns:
//   - The Badge instance for method chaining.
//
// Side effects:
//   - None.
func (b *Badge) Value(value string) *Badge {
	b.value = value
	return b
}

// Variant sets the badge variant (Default, Key, Status, Tag).
// Returns the badge for method chaining.
//
// Expected:
//   - v must be a valid BadgeVariant constant.
//
// Returns:
//   - The Badge instance for method chaining.
//
// Side effects:
//   - None.
func (b *Badge) Variant(v BadgeVariant) *Badge {
	b.variant = v
	return b
}

// Render returns the styled badge as a string.
//
// Expected:
//   - Badge must be initialized (use NewBadge).
//
// Returns:
//   - A styled string representation of the badge.
//
// Side effects:
//   - None.
func (b *Badge) Render() string {
	// BadgeHelpKey has special two-part rendering
	if b.variant == BadgeHelpKey {
		return b.renderHelpKey()
	}

	style := b.buildStyle()

	// Format content based on variant
	var content string
	switch b.variant {
	case BadgeKey:
		// Key badge: [key] or [key → action]
		if b.value != "" {
			content = "[" + b.label + " → " + b.value + "]"
		} else {
			content = "[" + b.label + "]"
		}
	case BadgeStatus, BadgeTag, BadgeDefault:
		content = b.label
	}

	return style.Render(content)
}

// renderHelpKey renders a two-part help key badge: "[Key] Hint".
//
// Returns:
//   - A styled string with the key and hint rendered side-by-side.
//
// Side effects:
//   - None.
func (b *Badge) renderHelpKey() string {
	th := b.Theme()

	var keyStyle, hintStyle lipgloss.Style
	if th != nil {
		palette := th.Palette()
		keyStyle = lipgloss.NewStyle().
			Background(palette.BackgroundAlt).
			Foreground(palette.Primary).
			Padding(0, 1).
			Bold(true)

		hintStyle = lipgloss.NewStyle().
			Foreground(palette.ForegroundDim)
	} else {
		// Fallback styling when no theme is provided
		keyStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Bold(true)
		hintStyle = lipgloss.NewStyle()
	}

	return keyStyle.Render(b.label) + " " + hintStyle.Render(b.value)
}

// buildStyle creates a lipgloss style based on the badge configuration.
//
// Returns:
//   - A configured lipgloss.Style for the badge variant.
//
// Side effects:
//   - None.
func (b *Badge) buildStyle() lipgloss.Style {
	style := lipgloss.NewStyle()

	switch b.variant {
	case BadgeKey:
		// Key badges: Accent color, bold, no background
		style = style.
			Foreground(b.AccentColor()).
			Bold(true)

	case BadgeStatus:
		// Status badges: Colored text with subtle background
		style = style.
			Foreground(b.SuccessColor()).
			Background(b.Theme().BackgroundColor()).
			Padding(0, 1).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(b.SuccessColor())

	case BadgeTag:
		// Tag badges: Pill-style with background
		style = style.
			Foreground(b.Theme().BackgroundColor()).
			Background(b.SecondaryColor()).
			Padding(0, 2).
			Border(lipgloss.RoundedBorder(), false).
			Bold(false)

	case BadgeDefault:
		// Default badges: Muted style
		style = style.
			Foreground(b.MutedColor()).
			Padding(0, 1)
	}

	return style
}

// Convenience constructors for common badge types

// KeyBadge creates a key badge formatted as "[key → action]".
// If action is empty, formats as "[key]".
//
// Expected:
//   - key should be a non-empty string.
//   - action can be empty (formats as "[key]").
//   - th can be nil (uses default theme).
//
// Returns:
//   - A configured Badge with BadgeKey variant.
//
// Side effects:
//   - None.
func KeyBadge(key, action string, th theme.Theme) *Badge {
	return NewBadge(key, th).Value(action).Variant(BadgeKey)
}

// StatusBadge creates a status badge with success styling.
//
// Expected:
//   - Must be a valid string.
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func StatusBadge(status string, th theme.Theme) *Badge {
	return NewBadge(status, th).Variant(BadgeStatus)
}

// TagBadge creates a tag badge with pill styling.
//
// Expected:
//   - Must be a valid string.
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func TagBadge(tag string, th theme.Theme) *Badge {
	return NewBadge(tag, th).Variant(BadgeTag)
}

// HelpKeyBadge creates a help footer key badge with two-part styling: "[Key] Hint".
//
// Expected:
//   - Must be a valid string.
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func HelpKeyBadge(key, hint string, th theme.Theme) *Badge {
	return NewBadge(key, th).Value(hint).Variant(BadgeHelpKey)
}

// =============================================================================
// Common Help Key Badge Constructors
// =============================================================================
// These create pre-configured badges for common keyboard shortcuts.
// Use these for consistency across the application.

// NavigateBadge returns a badge for navigation keys (arrows and vim-style j/k).
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func NavigateBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("↑↓/jk", "Navigate", th)
}

// NavigateHorizontalBadge returns a badge for horizontal navigation.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func NavigateHorizontalBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("←/→/hl", "Navigate", th)
}

// NavigateVimBadge returns a badge for vim-style navigation.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func NavigateVimBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("j/k", "Navigate", th)
}

// SelectBadge returns a badge for selecting items.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func SelectBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("Enter", "Select", th)
}

// CancelBadge returns a badge for canceling.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func CancelBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("Esc", "Cancel", th)
}

// QuitBadge returns a badge for quitting.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func QuitBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("q", "Quit", th)
}

// HelpBadge returns a badge for showing help.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func HelpBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("?", "Help", th)
}

// BackBadge returns a badge for going back.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func BackBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("Esc", "Back", th)
}

// ConfirmBadge returns a badge for confirming.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func ConfirmBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("Enter", "Confirm", th)
}

// AddBadge returns a badge for adding.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func AddBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("a", "Add", th)
}

// EditBadge returns a badge for editing.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func EditBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("e", "Edit", th)
}

// DeleteBadge returns a badge for deleting.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func DeleteBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("d", "Delete", th)
}

// SaveBadge returns a badge for saving.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func SaveBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("Ctrl+S", "Save", th)
}

// SkipBadge returns a badge for skipping.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func SkipBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("Ctrl+S", "Skip", th)
}

// SubmitBadge returns a badge for submitting.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func SubmitBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("Enter", "Submit", th)
}

// NextBadge returns a badge for going to the next item.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func NextBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("Tab", "Next", th)
}

// NextFieldBadge returns a badge for going to the next form field.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func NextFieldBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("Tab", "Next field", th)
}

// PrevBadge returns a badge for going to the previous item.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func PrevBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("Shift+Tab", "Previous", th)
}

// ApplyBadge returns a badge for applying changes.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func ApplyBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("Enter", "Apply", th)
}

// SearchBadge returns a badge for searching.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func SearchBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("/", "Search", th)
}

// FilterBadge returns a badge for filtering.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func FilterBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("f", "Filter", th)
}

// YesBadge returns a badge for yes/confirm shortcut.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func YesBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("y", "Yes", th)
}

// NoBadge returns a badge for no/cancel shortcut.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func NoBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("n", "No", th)
}

// ToggleBadge returns a badge for toggle selection (left/right).
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func ToggleBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("←→/hl", "Toggle", th)
}

// RetryBadge returns a badge for retry action (r key only).
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func RetryBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("r", "Retry", th)
}

// RetryEnterBadge returns a badge for retry action (Enter or r key).
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func RetryEnterBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("Enter/r", "Retry", th)
}

// ContinueBadge returns a badge for continue action.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func ContinueBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("Enter", "Continue", th)
}

// ViewBadge returns a badge for viewing item details.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func ViewBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("Enter", "View", th)
}

// PageBadge returns a badge for page navigation (Ctrl+D/U).
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func PageBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("Ctrl+D/U", "Page", th)
}

// PageVimBadge returns a badge for vim-style pagination (n/p).
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func PageVimBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("n/p", "Page", th)
}

// SuggestBadge returns a badge for AI suggestion action.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func SuggestBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("s", "Suggest", th)
}

// ViewEventsBadge returns a badge for viewing events.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func ViewEventsBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("v", "View Events", th)
}

// ViewFactsBadge returns a badge for viewing facts.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func ViewFactsBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("f", "View Facts", th)
}

// ViewSkillsBadge returns a badge for viewing skills.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func ViewSkillsBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("s", "View Skills", th)
}

// ConfirmActionBadge returns a badge for confirm action (c key).
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func ConfirmActionBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("c", "Confirm", th)
}

// AcceptBadge returns a badge for accepting suggestions/items.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func AcceptBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("a", "Accept", th)
}

// RejectBadge returns a badge for rejecting suggestions/items.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func RejectBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("r", "Reject", th)
}

// CloseBadge returns a badge for closing modals/views.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func CloseBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("Enter/Esc", "Close", th)
}

// InferSkillsBadge returns a badge for inferring skills from burst events.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Badge ready for use.
//
// Side effects:
//   - None.
func InferSkillsBadge(th theme.Theme) *Badge {
	return HelpKeyBadge("i", "Infer Skills", th)
}

// =============================================================================
// Help Footer Rendering
// =============================================================================

// RenderHelpFooter renders multiple badges as a help footer.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//   - badge must be valid.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func RenderHelpFooter(th theme.Theme, badges ...*Badge) string {
	if len(badges) == 0 {
		return ""
	}

	var parts []string
	for _, badge := range badges {
		parts = append(parts, badge.Render())
	}

	// Join with spacing between badges
	separator := "  "
	if th != nil {
		palette := th.Palette()
		separatorStyle := lipgloss.NewStyle().
			Foreground(palette.ForegroundMuted)
		separator = separatorStyle.Render("  ")
	}

	return strings.Join(parts, separator)
}

// =============================================================================
// Standard Footer Presets
// =============================================================================

// RenderMenuFooter renders a standard menu footer.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func RenderMenuFooter(th theme.Theme) string {
	return RenderHelpFooter(th,
		NavigateBadge(th),
		SelectBadge(th),
		HelpBadge(th),
		QuitBadge(th),
	)
}

// RenderListFooter renders a standard list footer.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func RenderListFooter(th theme.Theme) string {
	return RenderHelpFooter(th,
		NavigateBadge(th),
		SelectBadge(th),
		BackBadge(th),
	)
}

// RenderFormFooter renders a standard form footer.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func RenderFormFooter(th theme.Theme) string {
	return RenderHelpFooter(th,
		ConfirmBadge(th),
		CancelBadge(th),
	)
}

// RenderEditFooter renders a standard edit footer.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func RenderEditFooter(th theme.Theme) string {
	return RenderHelpFooter(th,
		SaveBadge(th),
		CancelBadge(th),
	)
}

// RenderConfirmFooter renders a standard confirmation footer.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func RenderConfirmFooter(th theme.Theme) string {
	return RenderHelpFooter(th,
		ConfirmBadge(th),
		CancelBadge(th),
	)
}

// RenderBrowseFooter renders a footer for browse views.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func RenderBrowseFooter(th theme.Theme) string {
	return RenderHelpFooter(th,
		NavigateBadge(th),
		SelectBadge(th),
		EditBadge(th),
		DeleteBadge(th),
		BackBadge(th),
	)
}

// RenderExportFooter renders a footer for export views.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func RenderExportFooter(th theme.Theme) string {
	return RenderHelpFooter(th,
		NavigateBadge(th),
		SelectBadge(th),
		ConfirmBadge(th),
		BackBadge(th),
	)
}
