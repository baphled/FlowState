package cli

import (
	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/carbonyl"
)

// routeUIInteraction selects the correct rendering backend for an
// interactive session based on the Carbonyl CLI flags. It is the
// single decision point shared by both `chat` and `session resume`.
//
// Expected:
//   - application is a non-nil, fully initialised App.
//   - opts is a non-nil CarbonylOptions parsed from CLI flags.
//   - agentID and sessionID are pre-resolved by the calling command.
//
// Returns:
//   - nil when the session completes successfully.
//   - An error if the selected backend fails.
//
// Side effects:
//   - Launches the selected Carbonyl backend (terminal renderer, system
//     browser, or URL printer).
func routeUIInteraction(application *app.App, opts *CarbonylOptions, agentID, sessionID string) error {
	adapter := &carbonyl.AppAdapter{App: application}

	if opts.Web {
		return carbonyl.OpenInBrowser(adapter, agentID, sessionID)
	}
	if opts.NoCarbonyl {
		return carbonyl.PrintURL(adapter, agentID, sessionID)
	}
	return carbonyl.Run(adapter, agentID, sessionID)
}
