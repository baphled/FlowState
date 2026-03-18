package themes

import (
	"github.com/charmbracelet/glamour"
)

// NewGlamourStyleName returns the appropriate glamour style name based on the theme.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func NewGlamourStyleName(theme Theme) string {
	if theme == nil {
		return "dark"
	}

	if theme.IsDark() {
		return "dark"
	}
	return "light"
}

// RenderMarkdown renders markdown content with theme-aware styling.
// Uses Glamour for rendering with the appropriate color scheme.
//
// Expected:
//   - theme must be a valid Theme instance (can be nil).
//   - content must be a valid markdown string.
//   - width must be a positive integer.
//
// Returns:
//   - A string value with rendered markdown.
//   - An error value if rendering failed.
//
// Side effects:
//   - None.
func RenderMarkdown(theme Theme, content string, width int) (string, error) {
	if content == "" {
		return "", nil
	}

	styleName := NewGlamourStyleName(theme)

	renderer, err := glamour.NewTermRenderer(
		glamour.WithStylePath(styleName),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return "", err
	}

	return renderer.Render(content)
}

// RenderCVPreview renders CV content as formatted markdown for preview.
// This is a convenience wrapper for RenderMarkdown specifically for CV content.
//
// Expected:
//   - theme must be a valid Theme instance (can be nil).
//   - cvcontent must be a valid markdown string.
//   - width must be a positive integer.
//
// Returns:
//   - A string value with rendered markdown.
//   - An error value if rendering failed.
//
// Side effects:
//   - None.
func RenderCVPreview(theme Theme, cvContent string, width int) (string, error) {
	return RenderMarkdown(theme, cvContent, width)
}

// MarkdownRenderer is a helper struct for rendering markdown with consistent theme styling.
type MarkdownRenderer struct {
	theme    Theme
	width    int
	renderer *glamour.TermRenderer
}

// NewMarkdownRenderer creates a new MarkdownRenderer with the given theme and width.
//
// Expected:
//   - theme must be a valid Theme instance (can be nil).
//   - width must be a positive integer.
//
// Returns:
//   - A fully initialized MarkdownRenderer ready for use.
//   - An error value if renderer creation failed.
//
// Side effects:
//   - None.
func NewMarkdownRenderer(theme Theme, width int) (*MarkdownRenderer, error) {
	styleName := NewGlamourStyleName(theme)

	renderer, err := glamour.NewTermRenderer(
		glamour.WithStylePath(styleName),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil, err
	}

	return &MarkdownRenderer{
		theme:    theme,
		width:    width,
		renderer: renderer,
	}, nil
}

// Render renders markdown content.
//
// Expected:
//   - content must be a valid markdown string.
//
// Returns:
//   - A string value with rendered markdown.
//   - An error value if rendering failed.
//
// Side effects:
//   - None.
func (mr *MarkdownRenderer) Render(content string) (string, error) {
	if content == "" {
		return "", nil
	}
	return mr.renderer.Render(content)
}

// SetWidth updates the renderer width.
//
// Expected:
//   - width must be a positive integer.
//
// Returns:
//   - A error value if reinitializing renderer failed.
//
// Side effects:
//   - Reinitializes the internal renderer with new width.
func (mr *MarkdownRenderer) SetWidth(width int) error {
	mr.width = width

	styleName := NewGlamourStyleName(mr.theme)
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStylePath(styleName),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return err
	}

	mr.renderer = renderer
	return nil
}

// SetTheme updates the renderer theme.
//
// Expected:
//   - th must be a valid theme instance (can be nil).
//
// Returns:
//   - A error value if reinitializing renderer failed.
//
// Side effects:
//   - Reinitializes the internal renderer with new theme.
func (mr *MarkdownRenderer) SetTheme(theme Theme) error {
	mr.theme = theme

	styleName := NewGlamourStyleName(theme)
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStylePath(styleName),
		glamour.WithWordWrap(mr.width),
	)
	if err != nil {
		return err
	}

	mr.renderer = renderer
	return nil
}
