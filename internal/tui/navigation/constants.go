package navigation

// NavigationKey maps a user-facing keyboard shortcut label to the action it
// triggers throughout the TUI. Components display the string value in help
// bars and badges so the user knows which key to press.
//
//nolint:revive // "NavigationKey" name is intentional for clarity in external packages.
type NavigationKey string

const (
	// KeyBack dismisses the current screen and returns to its parent, such as
	// closing a modal or navigating from a detail view back to a list.
	KeyBack NavigationKey = "Esc"
	// KeyUp moves the cursor or highlight one row upward in a list, menu, or
	// form field group.
	KeyUp NavigationKey = "↑/k"
	// KeyDown moves the cursor or highlight one row downward in a list, menu,
	// or form field group.
	KeyDown NavigationKey = "↓/j"
	// KeyLeft moves focus one column or tab to the left in multi-column
	// layouts and form navigation.
	KeyLeft NavigationKey = "←/h"
	// KeyRight moves focus one column or tab to the right in multi-column
	// layouts and form navigation.
	KeyRight NavigationKey = "→/l"
	// KeySelect confirms the currently highlighted item or submits the active
	// form, advancing the intent to the next state.
	KeySelect NavigationKey = "Enter"
	// KeyToggle flips the checked state of a checkbox, tag, or multi-select
	// option without advancing the cursor.
	KeyToggle NavigationKey = "Space"

	// KeyAdd opens the creation form for a new item in the current context.
	KeyAdd NavigationKey = "a"
	// KeyFilter opens or toggles the filter modal.
	KeyFilter NavigationKey = "f"
	// KeySort opens or cycles the sort modal.
	KeySort NavigationKey = "s"
	// KeySearch activates the search input overlay.
	KeySearch NavigationKey = "/"
	// KeyEdit opens the edit form for the currently selected item.
	KeyEdit NavigationKey = "e"
	// KeyDelete initiates deletion of the currently selected item.
	KeyDelete NavigationKey = "d"
	// KeyHelp toggles the help overlay.
	KeyHelp NavigationKey = "?"
	// KeyQuit exits the application.
	KeyQuit NavigationKey = "q"
)

// AllNavigationKeys returns every defined NavigationKey value.
//
// Returns:
//   - A []NavigationKey value.
//
// Side effects:
//   - None.
func AllNavigationKeys() []NavigationKey {
	return []NavigationKey{
		KeyBack,
		KeyUp,
		KeyDown,
		KeyLeft,
		KeyRight,
		KeySelect,
		KeyToggle,
		KeyAdd,
		KeyFilter,
		KeySort,
		KeySearch,
		KeyEdit,
		KeyDelete,
		KeyHelp,
		KeyQuit,
	}
}

// KeyDescription maps each NavigationKey to a concise human-readable label
// displayed in help overlays and tooltip badges.
var KeyDescription = map[NavigationKey]string{
	KeyBack:   "Go back to previous screen",
	KeyUp:     "Navigate up (also j)",
	KeyDown:   "Navigate down (also k)",
	KeyLeft:   "Navigate left (also h)",
	KeyRight:  "Navigate right (also l)",
	KeySelect: "Confirm selection or submit",
	KeyToggle: "Toggle selection or expansion",
	KeyAdd:    "Add new item",
	KeyFilter: "Show or toggle filters",
	KeySort:   "Show or toggle sort options",
	KeySearch: "Show search functionality",
	KeyEdit:   "Edit selected item",
	KeyDelete: "Delete selected item",
	KeyHelp:   "Show help information",
	KeyQuit:   "Quit application",
}
