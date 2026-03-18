// Package containers provides layout container components.
//
// # Overview
//
// The containers package implements layout containers that wrap and
// style content. These components provide borders, backgrounds, padding,
// and positioning for UI elements.
//
// # Available Containers
//
//   - Box: Basic bordered container with optional background
//   - Overlay: Full-screen overlay for modals and dialogs
//
// # Usage
//
//	box := containers.NewBox(theme).
//	    Content("Hello World").
//	    Border(theme.Primary()).
//	    Padding(1, 2)
//	rendered := box.Render()
package containers
