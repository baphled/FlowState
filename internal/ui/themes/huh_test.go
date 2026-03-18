package themes_test

import (
	"testing"

	"github.com/baphled/flowstate/internal/ui/themes"
	"github.com/charmbracelet/huh"
)

func TestGenerateHuhTheme_NilTheme(t *testing.T) {
	// When theme is nil, should return Catppuccin theme
	result := themes.GenerateHuhTheme(nil)

	if result == nil {
		t.Error("Expected non-nil theme, got nil")
	}
}

func TestGenerateHuhTheme_WithDefaultTheme(t *testing.T) {
	// Create a default theme
	tm := themes.NewThemeManager()
	theme := tm.Active()

	result := themes.GenerateHuhTheme(theme)

	if result == nil {
		t.Error("Expected non-nil theme, got nil")
	}

	// Verify theme has all required fields (check that styles are not empty)
	// We just verify the theme was created successfully
}

func TestGenerateHuhTheme_FocusedStyles(t *testing.T) {
	tm := themes.NewThemeManager()
	theme := tm.Active()
	result := themes.GenerateHuhTheme(theme)

	// Check focused styles have expected properties
	focused := result.Focused

	// Title should be bold
	if !focused.Title.GetBold() {
		t.Error("Expected Focused.Title to be bold")
	}

	// Select selector should be bold
	if !focused.SelectSelector.GetBold() {
		t.Error("Expected SelectSelector to be bold")
	}
}

func TestGenerateHuhTheme_BlurredStyles(t *testing.T) {
	tm := themes.NewThemeManager()
	theme := tm.Active()
	result := themes.GenerateHuhTheme(theme)

	// Check blurred styles are less prominent
	blurred := result.Blurred

	// Title should NOT be bold in blurred state
	if blurred.Title.GetBold() {
		t.Error("Expected Blurred.Title to NOT be bold")
	}
}

func TestGenerateHuhTheme_HelpStyles(t *testing.T) {
	tm := themes.NewThemeManager()
	theme := tm.Active()
	result := themes.GenerateHuhTheme(theme)

	// Check help styles
	helpStyles := result.Help

	// Short key should be bold
	if !helpStyles.ShortKey.GetBold() {
		t.Error("Expected Help.ShortKey to be bold")
	}
}

func TestNewThemedForm(t *testing.T) {
	tm := themes.NewThemeManager()
	theme := tm.Active()

	// Create a simple form
	group := huh.NewGroup(
		huh.NewInput().
			Key("test").
			Title("Test Input"),
	)

	form := themes.NewThemedForm(theme, group)

	if form == nil {
		t.Error("Expected non-nil form, got nil")
	}
}

func TestNewThemedForm_NilTheme(t *testing.T) {
	// Should work with nil theme (falls back to Catppuccin)
	group := huh.NewGroup(
		huh.NewInput().
			Key("test").
			Title("Test Input"),
	)

	form := themes.NewThemedForm(nil, group)

	if form == nil {
		t.Error("Expected non-nil form, got nil")
	}
}
