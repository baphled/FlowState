package cli

import (
	"github.com/spf13/cobra"
)

// CarbonylOptions holds the CLI flags that control how the interactive
// session is rendered: terminal (Carbonyl), system browser, or URL print.
type CarbonylOptions struct {
	// Web opens the Vue SPA in the system browser instead of terminal
	// rendering.
	Web bool
	// FPS sets the Carbonyl terminal rendering frame rate.
	FPS int
	// Zoom sets the Carbonyl terminal rendering zoom level.
	Zoom int
	// NoCarbonyl starts the ephemeral server and prints the URL without
	// launching Carbonyl. Useful for debugging or SSH sessions.
	NoCarbonyl bool
}

// addCarbonylFlags registers the Carbonyl CLI flags on the given command.
func addCarbonylFlags(cmd *cobra.Command, opts *CarbonylOptions) {
	flags := cmd.Flags()
	flags.BoolVar(&opts.Web, "web", false, "Open in system browser instead of terminal")
	flags.IntVar(&opts.FPS, "fps", 15, "Terminal rendering frame rate")
	flags.IntVar(&opts.Zoom, "zoom", 100, "Terminal rendering zoom level")
	flags.BoolVar(&opts.NoCarbonyl, "no-carbonyl", false, "Print URL without launching terminal renderer")
}

// defaultCarbonylOptions returns CarbonylOptions with production defaults.
func defaultCarbonylOptions() CarbonylOptions {
	return CarbonylOptions{
		FPS:  15,
		Zoom: 100,
	}
}
