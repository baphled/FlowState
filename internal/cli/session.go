package cli

import (
	"fmt"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/tui"
	"github.com/spf13/cobra"
)

func newSessionCmd(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Inspect and resume sessions",
		Long:  "Inspect saved sessions and resume a previous conversation.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newSessionListCmd(getApp), newSessionResumeCmd(getApp))
	return cmd
}

func newSessionListCmd(getApp func() *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List saved sessions",
		Long:  "List saved FlowState sessions.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			sessions := getApp().Sessions.List()
			if len(sessions) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "No sessions yet.")
				return err
			}

			for _, s := range sessions {
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: %d messages (last active: %s)\n",
					s.ID, s.MessageCount, s.LastActive.Format("2006-01-02 15:04"))
				if err != nil {
					return err
				}
			}
			return nil
		},
	}
}

const defaultAgentID = "default"

func newSessionResumeCmd(getApp func() *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "resume ID",
		Short: "Resume a saved session",
		Long:  "Resume a saved FlowState session.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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

			return tui.Run(a.Engine, agentID, sessionID)
		},
	}
}

func findSession(a *app.App, sessionID string) (*sessionInfo, error) {
	sessions := a.Sessions.List()
	for _, s := range sessions {
		if s.ID == sessionID {
			return &sessionInfo{
				ID:      s.ID,
				AgentID: s.AgentID,
			}, nil
		}
	}
	return nil, fmt.Errorf("session %q not found", sessionID)
}

type sessionInfo struct {
	ID      string
	AgentID string
}
