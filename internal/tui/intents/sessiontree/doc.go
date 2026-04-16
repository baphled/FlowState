// Package sessiontree provides a TUI intent for displaying a hierarchical session tree.
//
// This package provides:
//   - A tree view of parent-child session relationships using box-drawing connectors.
//   - A NodeID-based cursor that tracks the currently highlighted session.
//   - Current-session marking with a bullet indicator.
//   - A static snapshot built once from a flat list of SessionNode values.
package sessiontree
