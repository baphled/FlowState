package behaviors

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// FilterBehavior defines the contract for intents with search/filter/sort functionality.
// This interface establishes a consistent pattern for filter operations.
//
// All intents with data tables (lists) should implement this interface to ensure
// consistent user experience across search, filter, and sort operations.
type FilterBehavior interface {
	// HasActiveFilters returns true if any non-default filters are active.
	// This determines whether the "x: Clear filters" badge should be shown.
	HasActiveFilters() bool

	// ClearFilters resets filters in FIFO order (most recent filter first).
	// Implementation should follow this order:
	//   1. Clear search text if present
	//   2. Clear active filters (companies, categories, etc.) if present
	//   3. Reset sort to default if modified
	ClearFilters()

	// ApplyFilters applies current filter state to the data.
	// This should be called after any filter/sort/search change.
	ApplyFilters()

	// RefreshData reloads/refreshes the filtered data.
	// Returns a tea.Cmd that triggers data reload.
	RefreshData() tea.Cmd
}

// SearchableText performs case-insensitive substring matching for search filtering.
//
// Expected:
//   - text is the content to search within.
//   - query is the search term; empty string matches everything.
//
// Returns:
//   - True if query is empty or text contains query (case-insensitive).
//
// Side effects:
//   - None.
//
// Example:
//
//	SearchableText("Backend Developer", "backend") // true
//	SearchableText("Backend Developer", "frontend") // false
//	SearchableText("Backend Developer", "") // true (no filter)
func SearchableText(text, query string) bool {
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(text), strings.ToLower(query))
}

// SearchableFields checks whether any of the provided fields match a search query
// using case-insensitive substring matching.
//
// This is the recommended pattern for search implementation across all intents.
//
// Expected:
//   - query is the search term; empty string matches everything.
//   - fields are the strings to search within.
//
// Returns:
//   - True if query is empty or any field contains query (case-insensitive).
//
// Side effects:
//   - None.
//
// Example:
//
//	SearchableFields("backend", "Backend Developer", "TechCorp", "Engineering")
//	// Returns true because "Backend Developer" contains "backend"
//
//	SearchableFields("frontend", "Backend Developer", "TechCorp", "Engineering")
//	// Returns false because none of the fields contain "frontend"
func SearchableFields(query string, fields ...string) bool {
	if query == "" {
		return true
	}
	searchLower := strings.ToLower(query)
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), searchLower) {
			return true
		}
	}
	return false
}

// FilterStack represents a stack of active filters for FIFO clearing.
// This allows users to progressively remove filters by pressing 'x' multiple times.
type FilterStack struct {
	// Layers represents the filter layers in FIFO order.
	// Index 0 is the most recently applied filter (cleared first).
	Layers []FilterLayer
}

// FilterLayer represents a single filter type that can be cleared.
type FilterLayer string

const (
	// FilterLayerSearch represents search text filtering.
	FilterLayerSearch FilterLayer = "search"

	// FilterLayerCategory represents category-based filtering.
	FilterLayerCategory FilterLayer = "category"

	// FilterLayerTags represents tag-based filtering.
	FilterLayerTags FilterLayer = "tags"

	// FilterLayerSort represents custom sort (non-default).
	FilterLayerSort FilterLayer = "sort"
)

// NewFilterStack creates an empty filter stack.
//
// Returns:
//   - A fully initialized FilterStack ready for use.
//
// Side effects:
//   - None.
func NewFilterStack() *FilterStack {
	return &FilterStack{
		Layers: []FilterLayer{},
	}
}

// Push adds a filter layer to the stack (most recent).
//
// Expected:
//   - layer is the filter layer to add.
//
// Side effects:
//   - Adds the layer to the front of the stack.
func (s *FilterStack) Push(layer FilterLayer) {
	s.Layers = append([]FilterLayer{layer}, s.Layers...)
}

// Pop removes the most recently pushed filter layer for FIFO clearing.
//
// Returns:
//   - The most recently pushed FilterLayer, or empty string if empty.
//
// Side effects:
//   - Removes the first layer from the stack.
func (s *FilterStack) Pop() FilterLayer {
	if len(s.Layers) == 0 {
		return ""
	}
	layer := s.Layers[0]
	s.Layers = s.Layers[1:]
	return layer
}

// IsEmpty checks whether any filter layers remain in the stack.
//
// Returns:
//   - True if the stack has no layers.
//
// Side effects:
//   - None.
func (s *FilterStack) IsEmpty() bool {
	return len(s.Layers) == 0
}

// Clear removes all filter layers.
//
// Side effects:
//   - Empties the stack.
func (s *FilterStack) Clear() {
	s.Layers = []FilterLayer{}
}
