package navigation

import (
	"fmt"
	"strings"
)

// HelpText represents formatted help information for a set of navigation keys.
type HelpText struct {
	Keys     []NavigationKey
	Template string
	Compact  bool
}

// GetHelpText returns a formatted string displaying navigation keys with descriptions.
//
// Expected:
//   - keys is a slice of NavigationKey values to format.
//
// Returns:
//   - A formatted help text string.
//
// Side effects:
//   - None.
func GetHelpText(keys []NavigationKey) string {
	return GetHelpTextCompact(keys, false)
}

// GetHelpTextCompact returns formatted help text with optional compact formatting.
//
// Expected:
//   - keys is a slice of NavigationKey values to format.
//   - compact determines whether to use single-line formatting.
//
// Returns:
//   - A formatted help text string.
//
// Side effects:
//   - None.
func GetHelpTextCompact(keys []NavigationKey, compact bool) string {
	if len(keys) == 0 {
		return ""
	}

	var parts []string

	for _, key := range keys {
		description, exists := KeyDescription[key]
		if !exists {
			description = string(key)
		}

		if compact {
			parts = append(parts, fmt.Sprintf("%s: %s", key, description))
		} else {
			parts = append(parts, fmt.Sprintf("%s → %s", key, description))
		}
	}

	if compact {
		return strings.Join(parts, " | ")
	}
	return strings.Join(parts, "\n")
}

// GetContextualHelp returns help text for a specific screen context.
//
// Expected:
//   - context is the screen context identifier.
//
// Returns:
//   - A formatted help text string for the context.
//
// Side effects:
//   - None.
func GetContextualHelp(context string) string {
	contextKeyMap := map[string][]NavigationKey{
		"form": {
			KeyUp,
			KeyDown,
			KeySelect,
			KeyToggle,
			KeyBack,
		},
		"list": {
			KeyUp,
			KeyDown,
			KeySelect,
			KeyEdit,
			KeyDelete,
			KeyFilter,
			KeySort,
			KeySearch,
			KeyBack,
		},
		"default": {
			KeyUp,
			KeyDown,
			KeySelect,
			KeyHelp,
			KeyBack,
		},
	}

	keys, exists := contextKeyMap[context]
	if !exists {
		keys = contextKeyMap["default"]
	}

	return GetHelpTextCompact(keys, true)
}

// GetFullHelp returns comprehensive help text for all navigation keys.
//
// Returns:
//   - A formatted help text string with all keys.
//
// Side effects:
//   - None.
func GetFullHelp() string {
	return GetHelpTextCompact(AllNavigationKeys(), false)
}

// GetCompactHelp returns compact help text for all navigation keys (suitable for footers).
//
// Returns:
//   - A compact help text string.
//
// Side effects:
//   - None.
func GetCompactHelp() string {
	return GetHelpTextCompact(AllNavigationKeys(), true)
}

// GetGroupedHelp returns help text organized by key groups.
//
// Returns:
//   - A formatted help text string organized by groups.
//
// Side effects:
//   - None.
func GetGroupedHelp() string {
	groups := map[string][]NavigationKey{
		"Navigation": {KeyUp, KeyDown, KeyLeft, KeyRight},
		"Actions":    {KeySelect, KeyToggle, KeyAdd, KeyEdit, KeyDelete},
		"Tools":      {KeyFilter, KeySort, KeySearch},
		"Global":     {KeyHelp, KeyQuit, KeyBack},
	}

	var result strings.Builder
	for groupName, keys := range groups {
		result.WriteString(fmt.Sprintf("\n%s:\n", groupName))
		for _, key := range keys {
			description, exists := KeyDescription[key]
			if !exists {
				description = string(key)
			}
			result.WriteString(fmt.Sprintf("  %s → %s\n", key, description))
		}
	}

	return strings.TrimPrefix(result.String(), "\n")
}
