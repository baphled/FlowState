package behaviors

// Package behaviors provides embeddable components for table-based UIs.
// This file contains shared types used across all behavior components.

// ColumnDef defines a table column configuration.
type ColumnDef struct {
	// Title is the column header text
	Title string
	// Width is the column width in characters (0 for auto-sizing)
	Width int
}

// MenuOption represents a single option in a menu.
type MenuOption struct {
	// Label is the display text for this option
	Label string
	// Value is the associated data (can be any type)
	Value interface{}
	// IsSelected indicates if this option is currently selected (shows ✓)
	IsSelected bool
	// IsDisabled indicates if this option cannot be selected
	IsDisabled bool
}

// MenuSection groups related menu options under a header.
type MenuSection struct {
	// Title is the section header text
	Title string
	// Options are the menu items in this section
	Options []MenuOption
}

// RowFormatter transforms an item of type T into table row cells.
// The selection indicator (▶) is added automatically to the first column,
// so do not include it in the returned strings.
// Return one string per column defined in the table.
type RowFormatter[T any] func(item T, index int) []string

// FilterPredicate returns true if the item should be included in the filtered view.
type FilterPredicate[T any] func(item T) bool

// SortComparator compares two items for sorting.
// Returns:
//   - negative value if a < b
//   - zero if a == b
//   - positive value if a > b
type SortComparator[T any] func(a, b T) int
