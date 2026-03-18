package themes

import (
	"os"
	"strconv"
	"strings"
)

// ColorDepth represents the color depth supported by the terminal.
type ColorDepth int

const (
	// ColorDepth16 represents basic 16 color support.
	ColorDepth16 ColorDepth = 16
	// ColorDepth256 represents extended 256 color palette support.
	ColorDepth256 ColorDepth = 256
	// ColorDepthTrue represents 24-bit true color support (16.7 million colors).
	ColorDepthTrue ColorDepth = 16777216
)

// TerminalInfo contains detected information about the terminal.
type TerminalInfo struct {
	ColorDepth ColorDepth
	IsDark     bool
}

// NewTerminalInfo creates a new TerminalInfo by detecting the current terminal capabilities.
//
// Returns:
//   - A fully initialized TerminalInfo ready for use.
//
// Side effects:
//   - None.
func NewTerminalInfo() *TerminalInfo {
	return &TerminalInfo{
		ColorDepth: DetectColorDepth(),
		IsDark:     DetectDarkMode(),
	}
}

// SupportsTrueColor returns true if the terminal supports 24-bit true color.
//
// Returns:
//   - A bool value.
//
// Side effects:
//   - None.
func (ti *TerminalInfo) SupportsTrueColor() bool {
	return ti.ColorDepth == ColorDepthTrue
}

// Supports256Colors returns true if the terminal supports at least 256 colors.
//
// Returns:
//   - A bool value.
//
// Side effects:
//   - None.
func (ti *TerminalInfo) Supports256Colors() bool {
	return ti.ColorDepth >= ColorDepth256
}

// DetectColorDepth detects the color depth supported by the current terminal.
//
// Returns:
//   - A ColorDepth value.
//
// Side effects:
//   - None.
func DetectColorDepth() ColorDepth {
	// Check COLORTERM first for true color support
	colorterm := os.Getenv("COLORTERM")
	if colorterm != "" {
		if colorterm == "truecolor" || colorterm == "24bit" {
			return ColorDepthTrue
		}
	}

	// Check TERM for 256 color support
	term := os.Getenv("TERM")
	if strings.Contains(term, "256color") {
		return ColorDepth256
	}

	// Default to basic 16 colors
	return ColorDepth16
}

// DetectDarkMode attempts to detect whether the terminal is using a dark or light background.
//
// Returns:
//   - A bool value.
//
// Side effects:
//   - None.
func DetectDarkMode() bool {
	// Check COLORFGBG environment variable
	// Format: "foreground;background" where values 0-7 are dark, 8-15 are light
	colorfgbg := os.Getenv("COLORFGBG")
	if colorfgbg != "" {
		parts := strings.Split(colorfgbg, ";")
		if len(parts) >= 2 {
			// The last part is the background color
			if bg, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
				// Dark backgrounds are typically 0-7
				return bg < 8
			}
		}
	}

	// Default to dark mode (most terminal users prefer dark themes)
	return true
}

// AutoSelect automatically selects an appropriate theme based on terminal capabilities.
//
// Side effects:
//   - None.
func (tm *ThemeManager) AutoSelect() {
	info := NewTerminalInfo()

	// Get list of available themes
	themeNames := tm.List()
	if len(themeNames) == 0 {
		return
	}

	// Try to find a theme matching the terminal's dark/light mode preference
	for _, name := range themeNames {
		theme, err := tm.Get(name)
		if err != nil {
			continue
		}

		// Match dark/light mode
		if theme.IsDark() == info.IsDark {
			if tm.SetActive(name) != nil {
				continue
			}
			return
		}
	}

	// Fall back to default theme if no match found
	if _, err := tm.Get("default"); err == nil {
		if err := tm.SetActive("default"); err != nil {
			// Default theme couldn't be set, nothing more to do
			_ = err
		}
	}
}
