package cli

import (
	"fmt"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/tui"
	"github.com/spf13/cobra"
)

// newSessionCmd creates the session command for inspecting and resuming sessions.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command with session subcommands.
//
// Side effects:
//   - Registers the session list and resume subcommands.
func newSessionCmd(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Inspect and resume sessions",
		Long:  "Inspect saved sessions and resume a previous conversation.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newSessionListCmd(getApp), newSessionResumeCmd(getApp))
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

			for _, s := range sessions {
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

const defaultAgentID = "default"

// newSessionResumeCmd creates the session resume subcommand.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command for resuming sessions.
//
// Side effects:
//   - None.
func newSessionResumeCmd(getApp func() *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "resume ID",
		Short: "Resume a saved session",
		Long:  "Resume a saved FlowState session.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			sessionID := args[0]
			a := getApp()

			session, err := findSession(a, sessionID)
			if err != nil {
				return err
			}

			agentID := session.AgentID
			if agentID == "" {
				agentID = defaultAgentID
			}

			return tui.Run(a, agentID, sessionID)
		},
	}
}

// findSession retrieves session information by ID from the session store.
//
// Expected:
//   - a is a non-nil App instance with a populated sessions store.
//   - sessionID is a non-empty string.
//
// Returns:
//   - A pointer to sessionInfo and nil on success, or nil and an error if not found.
//
// Side effects:
//   - None.
func findSession(a *app.App, sessionID string) (*sessionInfo, error) {
	sessions := a.Sessions.List()
	for _, s := range sessions {
		if s.ID == sessionID {
			return &sessionInfo{
				ID:      s.ID,
				Title:   s.Title,
				AgentID: s.AgentID,
			}, nil
		}
	}
	return nil, fmt.Errorf("session %q not found", sessionID)
}

// sessionInfo holds metadata about a saved session.
//
// Expected:
//   - None.
//
// Returns:
//   - N/A (type definition).
//
// Side effects:
//   - None.
type sessionInfo struct {
	ID      string
	Title   string
	AgentID string
}
