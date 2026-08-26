package cli

import (
	"fmt"

	"github.com/baphled/flowstate/internal/app"
	"github.com/spf13/cobra"
)

// newSessionCmd creates the session command for inspecting sessions.
//
// The previous `resume` subcommand launched the interactive TUI, which
// has been decommissioned in favour of the Vue web frontend; resuming a
// session is now done through the web UI (or by passing --session to
// `flowstate run`). The remaining subcommands (list, tree) are TUI-free
// inspection helpers.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command with session subcommands.
//
// Side effects:
//   - Registers the session list and tree subcommands.
func newSessionCmd(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Inspect saved sessions",
		Long:  "Inspect saved FlowState sessions.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(
		newSessionListCmd(getApp),
		newSessionTreeCmd(getApp),
	)
	return cmd
}

// newSessionListCmd creates the session list subcommand.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command for listing sessions.
//
// Side effects:
//   - None.
func newSessionListCmd(getApp func() *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List saved sessions",
		Long:  "List saved FlowState sessions.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sessions := getApp().Sessions.List()
			if len(sessions) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "No sessions yet.")
				return err
			}

			for i := range sessions {
				s := &sessions[i]
				title := s.Title
				if title == "" {
					title = s.ID[:8]
				}
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %d messages (last active: %s)\n",
					s.ID[:8], title, s.MessageCount, s.LastActive.Format("2006-01-02 15:04"))
				if err != nil {
					return err
				}
			}
			return nil
		},
	}
}

// newSessionTreeCmd creates the session tree subcommand.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command for displaying session hierarchy.
//
// Side effects:
//   - None.
func newSessionTreeCmd(_ func() *app.App) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "tree <session-id>",
		Short: "Show session hierarchy as an ASCII tree",
		Long:  "Display the session hierarchy starting from the given session ID, showing parent-child relationships.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "Session tree requires a session manager. Use 'flowstate session list' to see sessions.")
			return err
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}
