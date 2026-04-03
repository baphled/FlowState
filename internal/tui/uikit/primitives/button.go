package primitives

import (
	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/charmbracelet/lipgloss"
)

// ButtonVariant defines the visual style and semantic meaning of a button.
type ButtonVariant int

const (
	// ButtonPrimary is for primary actions (submit, save, confirm).
	ButtonPrimary ButtonVariant = iota
	// ButtonSecondary is for secondary actions (cancel, back).
	ButtonSecondary
	// ButtonDanger is for destructive actions (delete, remove).
	ButtonDanger
)

// Button is a theme-aware button component with fluent API.
// Buttons support variants, focus states, disabled states, and width constraints.
//
// Example:
//
//	save := primitives.PrimaryButton("Save", theme).Focused(true)
//	cancel := primitives.SecondaryButton("Cancel", theme)
//	delete := primitives.DangerButton("Delete", theme).Disabled(true)
type Button struct {
	theme.Aware
	label    string
	variant  ButtonVariant
	focused  bool
	disabled bool
	width    int
}

// NewButton creates a new button with the given label and theme.
//
// Expected:
//   - Must be a valid string.
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Button ready for use.
//
// Side effects:
//   - None.
func NewButton(label string, th theme.Theme) *Button {
	b := &Button{
		label:    label,
		variant:  ButtonSecondary,
		focused:  false,
		disabled: false,
		width:    0,
	}
	if th != nil {
		b.SetTheme(th)
	}
	return b
}

// Variant sets the button variant (Primary, Secondary, Danger).
//
// Expected:
//   - buttonvariant must be valid.
//
// Returns:
//   - A fully initialized Button ready for use.
//
// Side effects:
//   - None.
func (b *Button) Variant(v ButtonVariant) *Button {
	b.variant = v
	return b
}

// Focused sets the focus state of the button.
//
// Expected:
//   - bool must be valid.
//
// Returns:
//   - A fully initialized Button ready for use.
//
// Side effects:
//   - None.
func (b *Button) Focused(f bool) *Button {
	b.focused = f
	return b
}

// Disabled sets the disabled state of the button.
//
// Expected:
//   - bool must be valid.
//
// Returns:
//   - A fully initialized Button ready for use.
//
// Side effects:
//   - None.
func (b *Button) Disabled(d bool) *Button {
	b.disabled = d
	return b
}

// Width sets the minimum width of the button.
//
// Expected:
//   - int must be valid.
//
// Returns:
//   - A fully initialized Button ready for use.
//
// Side effects:
//   - None.
func (b *Button) Width(w int) *Button {
	b.width = w
	return b
}

// Render returns the styled button as a string.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (b *Button) Render() string {
	style := b.buildStyle()
	return style.Render(" " + b.label + " ")
}

// buildStyle creates a lipgloss style based on the button configuration.
//
// Returns:
//   - A configured lipgloss.Style for the button variant.
//
// Side effects:
//   - None.
func (b *Button) buildStyle() lipgloss.Style {
	style := lipgloss.NewStyle()

	style = style.
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1)

	var fg, bg, border lipgloss.Color
	switch b.variant {
	case ButtonPrimary:
		fg = b.Theme().BackgroundColor()
		bg = b.PrimaryColor()
		border = b.PrimaryColor()
	case ButtonSecondary:
		fg = b.SecondaryColor()
		bg = b.Theme().BackgroundColor()
		border = b.SecondaryColor()
	case ButtonDanger:
		fg = b.ErrorColor()
		bg = b.Theme().BackgroundColor()
		border = b.ErrorColor()
	}

	if b.disabled {
		fg = b.MutedColor()
		bg = b.Theme().BackgroundColor()
		border = b.MutedColor()
		style = style.Faint(true)
	}

	style = style.
		Foreground(fg).
		Background(bg).
		BorderForeground(border)

	if b.focused && !b.disabled {
		style = style.
			BorderForeground(b.AccentColor()).
			BorderStyle(lipgloss.ThickBorder()).
			Bold(true)
	}

	if b.width > 0 {
		style = style.Width(b.width)
	}

	return style
}

// Convenience constructors for common button variants

// PrimaryButton creates a primary-styled button.
//
// Expected:
//   - Must be a valid string.
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Button ready for use.
//
// Side effects:
//   - None.
func PrimaryButton(label string, th theme.Theme) *Button {
	return NewButton(label, th).Variant(ButtonPrimary)
}

// SecondaryButton creates a secondary-styled button.
//
// Expected:
//   - Must be a valid string.
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Button ready for use.
//
// Side effects:
//   - None.
func SecondaryButton(label string, th theme.Theme) *Button {
	return NewButton(label, th).Variant(ButtonSecondary)
}

// DangerButton creates a danger-styled button.
//
// Expected:
//   - Must be a valid string.
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Button ready for use.
//
// Side effects:
//   - None.
func DangerButton(label string, th theme.Theme) *Button {
	return NewButton(label, th).Variant(ButtonDanger)
}
