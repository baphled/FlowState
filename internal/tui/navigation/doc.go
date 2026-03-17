// Package navigation provides keyboard navigation handlers and utilities.
//
// # Overview
//
// The navigation package implements consistent keyboard navigation patterns
// across the TUI application. It provides reusable handlers for common
// navigation scenarios like lists, forms, and menus.
//
// # Available Handlers
//
//   - ListNavigationHandler: Up/down navigation with vim keys
//   - GlobalKeyMap: Application-wide shortcuts (quit, help, back)
//   - ListKeyMap: List navigation shortcuts
//   - FormKeyMap: Form navigation shortcuts
//
// # Usage
//
// Use with TableBehavior:
//
//	table := behaviors.NewTableBehavior(...)
//	table.SetNavigationHandler(navigation.NewListHandler())
package navigation
