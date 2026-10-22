package cli

import (
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/app"
	"github.com/spf13/cobra"
)

// newDiscoverCmd creates the discover command for suggesting agents.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command for agent discovery.
//
// Side effects:
//   - None.
func newDiscoverCmd(getApp func() *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "discover MESSAGE",
		Short: "Suggest an agent for a task",
		Long:  "Suggest an agent for a task description.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDiscover(cmd, getApp(), args)
		},
	}
}

// runDiscover suggests agents matching the provided message description.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - application is a non-nil App instance with a populated registry and discovery service.
//   - args is a non-empty slice of strings.
//
// Returns:
//   - nil on success, or an error if output fails.
//
// Side effects:
//   - Writes agent suggestions to stdout.
func runDiscover(cmd *cobra.Command, application *app.App, args []string) error {
	manifests := application.Registry.List()
	if len(manifests) == 0 {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "No agents available for discovery.")
		return err
	}

	message := strings.Join(args, " ")
	suggestions := application.Discovery.Suggest(message)

	if len(suggestions) == 0 {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "No matching agents found.")
		return err
	}

	for _, s := range suggestions {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s (confidence: %.2f): %s\n", s.AgentID, s.Confidence, s.Reason)
		if err != nil {
			return err
		}
	}
	return nil
}
