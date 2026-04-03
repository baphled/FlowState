// Package primitives provides foundational UI components for the UIKit.
// All primitives are theme-aware and follow a fluent builder pattern.
package primitives

import (
	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/charmbracelet/lipgloss"
)

// Alignment constants for text positioning.
// These wrap lipgloss.Position values for convenience.
const (
	AlignLeft   = lipgloss.Left
	AlignCenter = lipgloss.Center
	AlignRight  = lipgloss.Right
)

// TextStyle defines the semantic style of text.
type TextStyle int

const (
	// TextBody is the default text style for regular content.
	TextBody TextStyle = iota
	// TextTitle is for large, prominent titles.
	TextTitle
	// TextSubtitle is for section subtitles.
	TextSubtitle
	// TextMuted is for disabled or de-emphasized text.
	TextMuted
	// TextError is for error messages.
	TextError
	// TextSuccess is for success messages.
	TextSuccess
	// TextWarning is for warning messages.
	TextWarning
	// TextInfo is for informational messages.
	TextInfo
)

// Text is a theme-aware text component with fluent API.
// It supports semantic styles, bold formatting, width constraints, alignment, margins, and custom colors.
//
// Example:
//
//	title := primitives.Title("Welcome", theme).Bold().Align(lipgloss.Center)
//	spaced := primitives.Body("Content", theme).MarginBottom(1)
//	error := primitives.ErrorText("Failed", theme).Width(40)
//	custom := primitives.NewText("Custom", theme).Foreground(lipgloss.Color("#FF0000"))
//	italic := primitives.Muted("Note", theme).Italic()
type Text struct {
	theme.Aware
	content          string
	textStyle        TextStyle
	bold             bool
	italic           bool
	width            int
	align            lipgloss.Position
	marginTop        int
	marginBottom     int
	marginLeft       int
	marginRight      int
	customForeground *lipgloss.Color // Optional custom foreground color override
}

// NewText creates a new text component with the given content and theme.
//
// Expected:
//   - Must be a valid string.
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Text ready for use.
//
// Side effects:
//   - None.
func NewText(content string, th theme.Theme) *Text {
	t := &Text{
		content:   content,
		textStyle: TextBody,
		bold:      false,
		width:     0,
	}
	if th != nil {
		t.SetTheme(th)
	}
	return t
}

// Style sets the semantic style of the text.
//
// Expected:
//   - textstyle must be valid.
//
// Returns:
//   - A fully initialized Text ready for use.
//
// Side effects:
//   - None.
func (t *Text) Style(style TextStyle) *Text {
	t.textStyle = style
	return t
}

// Bold makes the text bold.
//
// Returns:
//   - A fully initialized Text ready for use.
//
// Side effects:
//   - None.
func (t *Text) Bold() *Text {
	t.bold = true
	return t
}

// Italic makes the text italic.
//
// Returns:
//   - A fully initialized Text ready for use.
//
// Side effects:
//   - None.
func (t *Text) Italic() *Text {
	t.italic = true
	return t
}

// Width sets the maximum width of the text.
//
// Expected:
//   - int must be valid.
//
// Returns:
//   - A fully initialized Text ready for use.
//
// Side effects:
//   - None.
func (t *Text) Width(w int) *Text {
	t.width = w
	return t
}

// Align sets the text alignment (lipgloss.Left, lipgloss.Center, lipgloss.Right).
//
// Expected:
//   - position must be valid.
//
// Returns:
//   - A fully initialized Text ready for use.
//
// Side effects:
//   - None.
func (t *Text) Align(align lipgloss.Position) *Text {
	t.align = align
	return t
}

// Center is a convenience method to center-align the text.
//
// Returns:
//   - A fully initialized Text ready for use.
//
// Side effects:
//   - None.
func (t *Text) Center() *Text {
	return t.Align(lipgloss.Center)
}

// Left is a convenience method to left-align the text.
//
// Returns:
//   - A fully initialized Text ready for use.
//
// Side effects:
//   - None.
func (t *Text) Left() *Text {
	return t.Align(lipgloss.Left)
}

// Right is a convenience method to right-align the text.
//
// Returns:
//   - A fully initialized Text ready for use.
//
// Side effects:
//   - None.
func (t *Text) Right() *Text {
	return t.Align(lipgloss.Right)
}

// MarginTop adds vertical spacing above the text (in lines).
//
// Expected:
//   - int must be valid.
//
// Returns:
//   - A fully initialized Text ready for use.
//
// Side effects:
//   - None.
func (t *Text) MarginTop(n int) *Text {
	t.marginTop = n
	return t
}

// MarginBottom adds vertical spacing below the text (in lines).
//
// Expected:
//   - int must be valid.
//
// Returns:
//   - A fully initialized Text ready for use.
//
// Side effects:
//   - None.
func (t *Text) MarginBottom(n int) *Text {
	t.marginBottom = n
	return t
}

// MarginLeft adds horizontal spacing before the text (in characters).
//
// Expected:
//   - int must be valid.
//
// Returns:
//   - A fully initialized Text ready for use.
//
// Side effects:
//   - None.
func (t *Text) MarginLeft(n int) *Text {
	t.marginLeft = n
	return t
}

// MarginRight adds horizontal spacing after the text (in characters).
//
// Expected:
//   - int must be valid.
//
// Returns:
//   - A fully initialized Text ready for use.
//
// Side effects:
//   - None.
func (t *Text) MarginRight(n int) *Text {
	t.marginRight = n
	return t
}

// Foreground sets a custom foreground color, overriding the semantic style color.
//
// Expected:
//   - color must be valid.
//
// Returns:
//   - A fully initialized Text ready for use.
//
// Side effects:
//   - None.
func (t *Text) Foreground(color lipgloss.Color) *Text {
	t.customForeground = &color
	return t
}

// Render returns the styled text as a string.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (t *Text) Render() string {
	style := t.buildStyle()
	return style.Render(t.content)
}

// buildStyle creates a lipgloss style based on the text configuration.
//
// Returns:
//   - A configured lipgloss.Style for the text variant.
//
// Side effects:
//   - None.
func (t *Text) buildStyle() lipgloss.Style {
	style := lipgloss.NewStyle()

	if t.customForeground != nil {
		style = style.Foreground(*t.customForeground)
	} else {
		switch t.textStyle {
		case TextTitle:
			style = style.Foreground(t.PrimaryColor())
		case TextSubtitle:
			style = style.Foreground(t.SecondaryColor())
		case TextBody:
			style = style.Foreground(t.Theme().ForegroundColor())
		case TextMuted:
			style = style.Foreground(t.MutedColor())
		case TextError:
			style = style.Foreground(t.ErrorColor())
		case TextSuccess:
			style = style.Foreground(t.SuccessColor())
		case TextWarning:
			style = style.Foreground(t.WarningColor())
		case TextInfo:
			style = style.Foreground(t.Theme().InfoColor())
		}
	}

	if t.textStyle == TextTitle {
		style = style.Bold(true)
	}

	if t.bold {
		style = style.Bold(true)
	}

	if t.italic {
		style = style.Italic(true)
	}

	if t.width > 0 {
		style = style.Width(t.width)
	}

	if t.width > 0 && t.align != 0 {
		style = style.Align(t.align)
	}

	if t.marginTop > 0 {
		style = style.MarginTop(t.marginTop)
	}
	if t.marginBottom > 0 {
		style = style.MarginBottom(t.marginBottom)
	}
	if t.marginLeft > 0 {
		style = style.MarginLeft(t.marginLeft)
	}
	if t.marginRight > 0 {
		style = style.MarginRight(t.marginRight)
	}

	return style
}

// Convenience constructors for common text styles

// Title creates a title-styled text component.
//
// Expected:
//   - Must be a valid string.
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Text ready for use.
//
// Side effects:
//   - None.
func Title(content string, th theme.Theme) *Text {
	return NewText(content, th).Style(TextTitle)
}

// Subtitle creates a subtitle-styled text component.
//
// Expected:
//   - Must be a valid string.
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Text ready for use.
//
// Side effects:
//   - None.
func Subtitle(content string, th theme.Theme) *Text {
	return NewText(content, th).Style(TextSubtitle)
}

// Body creates a body-styled text component.
//
// Expected:
//   - Must be a valid string.
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Text ready for use.
//
// Side effects:
//   - None.
func Body(content string, th theme.Theme) *Text {
	return NewText(content, th).Style(TextBody)
}

// Muted creates a muted-styled text component.
//
// Expected:
//   - Must be a valid string.
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Text ready for use.
//
// Side effects:
//   - None.
func Muted(content string, th theme.Theme) *Text {
	return NewText(content, th).Style(TextMuted)
}

// ErrorText creates an error-styled text component.
//
// Expected:
//   - Must be a valid string.
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Text ready for use.
//
// Side effects:
//   - None.
func ErrorText(content string, th theme.Theme) *Text {
	return NewText(content, th).Style(TextError)
}

// SuccessText creates a success-styled text component.
//
// Expected:
//   - Must be a valid string.
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Text ready for use.
//
// Side effects:
//   - None.
func SuccessText(content string, th theme.Theme) *Text {
	return NewText(content, th).Style(TextSuccess)
}

// WarningText creates a warning-styled text component.
//
// Expected:
//   - Must be a valid string.
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Text ready for use.
//
// Side effects:
//   - None.
func WarningText(content string, th theme.Theme) *Text {
	return NewText(content, th).Style(TextWarning)
}

// InfoText creates an info-styled text component.
//
// Expected:
//   - Must be a valid string.
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A fully initialized Text ready for use.
//
// Side effects:
//   - None.
func InfoText(content string, th theme.Theme) *Text {
	return NewText(content, th).Style(TextInfo)
}

// Layout helpers

// CenterInTerminal centers content both horizontally and vertically in the terminal.
//
// Expected:
//   - Must be a valid string.
//   - int must be valid.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func CenterInTerminal(content string, width, height int) string {
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content)
}

// PlaceInTerminal places content at the top-center of the available area.
//
// Expected:
//   - Must be a valid string.
//   - int must be valid.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func PlaceInTerminal(content string, width, height int) string {
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Top, content)
}

// JoinVertical joins multiple strings vertically with the specified alignment.
//
// Expected:
//   - position must be valid.
//   - Must be a valid string.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func JoinVertical(align lipgloss.Position, parts ...string) string {
	return lipgloss.JoinVertical(align, parts...)
}

// JoinHorizontal joins multiple strings horizontally with the specified alignment.
//
// Expected:
//   - position must be valid.
//   - Must be a valid string.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func JoinHorizontal(align lipgloss.Position, parts ...string) string {
	return lipgloss.JoinHorizontal(align, parts...)
}
