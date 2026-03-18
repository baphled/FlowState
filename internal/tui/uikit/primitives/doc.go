// Package primitives provides base UI primitives.
//
// # Overview
//
// The primitives package implements the foundational UI components
// used throughout the application. These are the building blocks for
// higher-level components.
//
// # Available Primitives
//
//   - Text: Styled text rendering (Title, Body, ErrorText)
//   - Button: Interactive button component
//   - ButtonGroup: Group of related buttons
//   - Badge: Status and label badges
//   - Input: Text input styling
//   - KeyValue: Key-value pair display
//   - ProgressBar: Progress indication
//
// # Usage
//
// Render styled text:
//
//	title := primitives.Title("Section Title", theme)
//	body := primitives.Body("Description text", theme)
//
// Create badges:
//
//	badge := primitives.HelpKeyBadge("enter", "Submit", theme)
package primitives
