package cli

import (
	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/carbonyl"
	"github.com/baphled/flowstate/internal/tui"
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
//   - Launches the selected UI backend (Carbonyl, browser, URL printer,
//     or legacy Bubble Tea TUI).
func routeUIInteraction(application *app.App, opts *CarbonylOptions, agentID, sessionID string) error {
	if opts.LegacyTUI {
		return tui.Run(application, agentID, sessionID)
	}

	adapter := &carbonyl.AppAdapter{App: application}

	if opts.Web {
		return carbonyl.OpenInBrowser(adapter, agentID, sessionID)
	}
	if opts.NoCarbonyl {
		return carbonyl.PrintURL(adapter, agentID, sessionID)
	}
	return carbonyl.Run(adapter, agentID, sessionID)
}
