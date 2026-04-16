// Package eventdetails provides a TUI intent for displaying full SwarmEvent metadata
// as a read-only bordered modal overlay.
//
// This package provides:
//   - A detail view of a single SwarmEvent including type, status, timestamp, agent ID,
//     and alphabetically sorted metadata key-value pairs.
//   - Scrollable content when the rendered detail exceeds the available height.
//   - Keyboard handling for scroll (Up/Down, j/k) and dismiss (Escape).
//   - Integration as a modal overlay via the standard ShowModalMsg / DismissModalMsg pattern.
package eventdetails
