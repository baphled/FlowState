// Package navigation provides breadcrumb navigation components for the UIKit.
//
// # Overview
//
// This package implements breadcrumb navigation patterns for displaying
// hierarchical navigation paths in the TUI interface.
//
// # Components
//
//   - BreadcrumbBar: Horizontal breadcrumb trail with icons
//
// # Usage
//
// Use with BreadcrumbBar:
//
//	bar := navigation.NewBreadcrumbBar(theme).
//	    SetCrumbs([]string{"Home", "Settings", "Profile"})
package navigation
