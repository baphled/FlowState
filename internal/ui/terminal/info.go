package terminal

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Info holds terminal dimension information.
type Info struct {
	Width       int
	Height      int
	IsValid     bool
	LastUpdated time.Time
}

// Config defines terminal size constraints and defaults.
type Config struct {
	MinWidth      int
	MinHeight     int
	DefaultWidth  int
	DefaultHeight int
}

// DefaultConfig provides sensible defaults for terminal configuration.
var DefaultConfig = Config{
	MinWidth:      40,
	MinHeight:     15,
	DefaultWidth:  80,
	DefaultHeight: 24,
}

// Margins represents spacing around content.
type Margins struct {
	Top    int
	Right  int
	Bottom int
	Left   int
}

// SizeCategory represents the terminal size category for responsive design.
type SizeCategory int

const (
	// SizeTiny represents terminals < 60 columns.
	SizeTiny SizeCategory = iota
	// SizeCompact represents terminals 60-79 columns.
	SizeCompact
	// SizeNormal represents terminals 80-119 columns.
	SizeNormal
	// SizeLarge represents terminals 120-159 columns.
	SizeLarge
	// SizeXLarge represents terminals >= 160 columns.
	SizeXLarge
)

// NewInfo creates a new Info instance with default values.
//
// Returns:
//   - A fully initialized Info ready for use.
//
// Side effects:
//   - None.
func NewInfo() *Info {
	return &Info{
		Width:   0,
		Height:  0,
		IsValid: false,
	}
}

// Update updates the terminal info from a WindowSizeMsg.
//
// Expected:
//   - windowsizemsg must be valid.
//
// Side effects:
//   - None.
func (i *Info) Update(msg tea.WindowSizeMsg) {
	i.Width = msg.Width
	i.Height = msg.Height
	i.IsValid = true
	i.LastUpdated = time.Now()
}

// GetCategory returns the size category based on terminal width.
//
// Returns:
//   - A SizeCategory value.
//
// Side effects:
//   - None.
func (i *Info) GetCategory() SizeCategory {
	if !i.IsValid {
		return SizeNormal
	}

	switch {
	case i.Width < 60:
		return SizeTiny
	case i.Width < 80:
		return SizeCompact
	case i.Width < 120:
		return SizeNormal
	case i.Width < 160:
		return SizeLarge
	default:
		return SizeXLarge
	}
}

// GetSafeDimensions returns dimensions with enforced minimums and fallback to defaults.
//
// Expected:
//   - config must be a valid Config object.
//
// Returns:
//   - A width value with enforced minimums.
//   - A height value with enforced minimums.
//
// Side effects:
//   - None.
func (i *Info) GetSafeDimensions(config Config) (width, height int) {
	if !i.IsValid {
		return config.DefaultWidth, config.DefaultHeight
	}

	width = maxInt(i.Width, config.MinWidth)
	height = maxInt(i.Height, config.MinHeight)
	return
}

// CanRender returns true if the terminal can render content at minimum size.
//
// Expected:
//   - config must be a valid configuration object.
//
// Returns:
//   - A bool value.
//
// Side effects:
//   - None.
func (i *Info) CanRender(config Config) bool {
	if !i.IsValid {
		return true
	}
	return i.Width >= config.MinWidth && i.Height >= config.MinHeight
}

// ContentArea calculates available space after accounting for margins.
// Returns width and height with enforced minimums.
//
// Expected:
//   - margins must be a valid Margins object.
//
// Returns:
//   - A width value for content area.
//   - A height value for content area.
//
// Side effects:
//   - None.
func (i *Info) ContentArea(margins Margins) (width, height int) {
	safeWidth, safeHeight := i.GetSafeDimensions(DefaultConfig)

	width = safeWidth - margins.Left - margins.Right
	height = safeHeight - margins.Top - margins.Bottom

	// Ensure minimum content area
	width = maxInt(width, 20)
	height = maxInt(height, 5)

	return
}

// maxInt returns the larger of two integers.
//
// Expected:
//   - a and b are valid integers.
//
// Returns:
//   - The larger of the two values.
//
// Side effects:
//   - None.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
