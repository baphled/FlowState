// Package theme provides theme infrastructure for UIKit components.
// It re-exports the themes.Theme interface and provides a default theme.
package theme

import (
	"github.com/baphled/flowstate/internal/ui/themes"
)

// Theme is re-exported from internal/ui/themes for convenience.
// It defines the contract for all themes used in UIKit components.
type Theme = themes.Theme

// Default returns the default KaRiya theme.
//
// Returns:
//   - A Theme value.
//
// Side effects:
//   - None.
func Default() Theme {
	return themes.NewDefaultTheme()
}
