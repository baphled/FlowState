package widgets

// BaseView is an embeddable struct providing terminal dimensions, theme, and logo
// storage — the same fields that base.Screen provides today.
type BaseView struct {
	terminalWidth  int
	terminalHeight int
	theme          interface{}
	logo           interface{}
	logoSpacing    int
}

// SetTerminalInfo updates the stored terminal dimensions.
//
// Expected:
//   - int must be valid.
//
// Side effects:
//   - None.
func (b *BaseView) SetTerminalInfo(width, height int) {
	b.terminalWidth = width
	b.terminalHeight = height
}

// SetTheme updates the stored theme.
//
// Expected:
//   - interface{} must be valid.
//
// Side effects:
//   - None.
func (b *BaseView) SetTheme(theme interface{}) {
	b.theme = theme
}

// SetLogo updates the stored logo and spacing.
//
// Expected:
//   - interface{} must be valid.
//   - int must be valid.
//
// Side effects:
//   - None.
func (b *BaseView) SetLogo(logo interface{}, spacing int) {
	b.logo = logo
	b.logoSpacing = spacing
}

// GetTerminalWidth returns the stored terminal width.
//
// Returns:
//   - An int value.
//
// Side effects:
//   - None.
func (b *BaseView) GetTerminalWidth() int { return b.terminalWidth }

// GetTerminalHeight returns the stored terminal height.
//
// Returns:
//   - An int value.
//
// Side effects:
//   - None.
func (b *BaseView) GetTerminalHeight() int { return b.terminalHeight }

// GetTheme returns the stored theme.
//
// Returns:
//   - An interface{} value.
//
// Side effects:
//   - None.
func (b *BaseView) GetTheme() interface{} { return b.theme }

// GetLogo returns the stored logo.
//
// Returns:
//   - An interface{} value.
//
// Side effects:
//   - None.
func (b *BaseView) GetLogo() interface{} { return b.logo }

// GetLogoSpacing returns the stored logo spacing.
//
// Returns:
//   - An int value.
//
// Side effects:
//   - None.
func (b *BaseView) GetLogoSpacing() int { return b.logoSpacing }
