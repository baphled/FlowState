package navigation

// ListNavigator provides minimal callbacks for list navigation.
// Implementers are responsible for managing their own data and table updates.
type ListNavigator interface {
	// GetTotalItems returns the total number of items in the list
	GetTotalItems() int

	// GetSelectedIndex returns the current selection index (0-based, absolute)
	GetSelectedIndex() int

	// SetSelectedIndex sets the selection index and updates display.
	// Implementers should sync table cursor and call updateTableRows() here.
	SetSelectedIndex(idx int)

	// GetPageSize returns items per page (for page up/down navigation)
	GetPageSize() int
}

// ListNavigationHandler handles keyboard navigation for lists.
// It is the single source of truth for navigation operations.
type ListNavigationHandler struct {
	navigator ListNavigator
}

// NewListNavigationHandler creates a new list navigation handler.
//
// Expected:
//   - navigator must be a valid ListNavigator implementation.
//
// Returns:
//   - A fully initialized ListNavigationHandler ready for use.
//
// Side effects:
//   - None.
func NewListNavigationHandler(navigator ListNavigator) *ListNavigationHandler {
	return &ListNavigationHandler{
		navigator: navigator,
	}
}

// HandleKey processes navigation keys and updates the selection accordingly.
//
// Expected:
//   - keyStr must be a recognized navigation key string.
//
// Returns:
//   - True if the key was recognized and handled, false otherwise.
//
// Side effects:
//   - Updates the selected index via the navigator when a key is handled.
func (h *ListNavigationHandler) HandleKey(keyStr string) bool {
	totalItems := h.navigator.GetTotalItems()
	if totalItems == 0 {
		return false
	}

	currentIndex := h.navigator.GetSelectedIndex()
	pageSize := h.navigator.GetPageSize()

	switch keyStr {
	case "up", "k":
		newIndex := currentIndex - 1
		if newIndex < 0 {
			newIndex = 0
		}
		h.navigator.SetSelectedIndex(newIndex)
		return true

	case "down", "j":
		newIndex := currentIndex + 1
		if newIndex >= totalItems {
			newIndex = totalItems - 1
		}
		h.navigator.SetSelectedIndex(newIndex)
		return true

	case "pgup", "ctrl+u":
		newIndex := currentIndex - pageSize
		if newIndex < 0 {
			newIndex = 0
		}
		h.navigator.SetSelectedIndex(newIndex)
		return true

	case "pgdn", "ctrl+d":
		newIndex := currentIndex + pageSize
		if newIndex >= totalItems {
			newIndex = totalItems - 1
		}
		h.navigator.SetSelectedIndex(newIndex)
		return true

	case "home", "g":
		h.navigator.SetSelectedIndex(0)
		return true

	case "end", "G":
		h.navigator.SetSelectedIndex(totalItems - 1)
		return true

	default:
		return false
	}
}

// FormatRowText returns text with a selection indicator if this is the selected row.
//
// Expected:
//   - rowIndex is the index of the row being formatted.
//   - text is the content to display in the row.
//
// Returns:
//   - The formatted text with or without selection indicator.
//
// Side effects:
//   - None.
func (h *ListNavigationHandler) FormatRowText(rowIndex int, text string) string {
	selectedIndex := h.navigator.GetSelectedIndex()
	if rowIndex == selectedIndex {
		return "▶ " + text
	}
	return "  " + text
}
