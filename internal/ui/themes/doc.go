// Package themes provides theme management and styling for the TUI.
//
// # Overview
//
// The themes package implements a comprehensive theming system for the
// KaRiya CLI. It supports multiple theme formats including lipgloss,
// huh, bubbles, and glamour (markdown) styles.
//
// # Theme System
//
// Themes provide:
//   - Color palettes (primary, secondary, success, error, warning)
//   - Semantic colors (background, foreground, muted, border)
//   - Component styles (buttons, inputs, tables, modals)
//   - Markdown rendering styles
//
// # Usage
//
// Get the current theme:
//
//	theme := themes.Current()
//	primary := theme.Primary()
//
// Create a styled component:
//
//	style := theme.Success().Copy().Bold(true)
//
// For more details, see the theme-aware component documentation.
package themes
