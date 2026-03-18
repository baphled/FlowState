// Package layout provides screen layout components.
//
// # Overview
//
// The layout package implements standard screen layouts that provide
// consistent structure across the application. These components handle
// header, content, and footer placement.
//
// # Available Components
//
//   - ScreenLayout: Standard screen with header, content, footer
//   - Header: Application header with title and breadcrumbs
//   - Footer: Status bar and navigation hints
//
// # Usage
//
// Create a standard screen layout:
//
//	layout := layout.NewScreenLayout(theme)
//	layout.SetHeader("Page Title")
//	layout.SetContent(contentView)
//	layout.SetFooter(footerView)
//	return layout.Render()
package layout
