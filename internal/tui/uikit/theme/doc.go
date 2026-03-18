// Package theme provides theme integration utilities.
//
// # Overview
//
// The theme package provides types and utilities for theme-aware
// components. It defines the Theme interface and provides the
// theme.Aware embeddable type.
//
// # Types
//
//   - Theme: Interface for theme implementations
//   - Aware: Embeddable type that provides theme access
//
// # Usage
//
// Embed in a component:
//
//	type MyComponent struct {
//	    theme.Aware
//	    // ... other fields
//	}
//
// Access theme:
//
//	theme := component.Theme() // Returns default if nil
package theme
