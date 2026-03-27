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
	colorterm := os.Getenv("COLORTERM")
	if colorterm != "" {
		if colorterm == "truecolor" || colorterm == "24bit" {
			return ColorDepthTrue
		}
	}

	term := os.Getenv("TERM")
	if strings.Contains(term, "256color") {
		return ColorDepth256
	}

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
	colorfgbg := os.Getenv("COLORFGBG")
	if colorfgbg != "" {
		parts := strings.Split(colorfgbg, ";")
		if len(parts) >= 2 {
			if bg, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
				return bg < 8
			}
		}
	}

	return true
}

// AutoSelect automatically selects an appropriate theme based on terminal capabilities.
//
// Side effects:
//   - None.
func (tm *ThemeManager) AutoSelect() {
	info := NewTerminalInfo()

	themeNames := tm.List()
	if len(themeNames) == 0 {
		return
	}

	for _, name := range themeNames {
		theme, err := tm.Get(name)
		if err != nil {
			continue
		}

		if theme.IsDark() == info.IsDark {
			if tm.SetActive(name) != nil {
				continue
			}
			return
		}
	}

	if _, err := tm.Get("default"); err == nil {
		if err := tm.SetActive("default"); err != nil {
			_ = err
		}
	}
}
