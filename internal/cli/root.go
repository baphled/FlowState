package cli

import (
	"fmt"

	"github.com/baphled/flowstate/internal/app"
	"github.com/spf13/cobra"
)

func NewRootCmd(application *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "flowstate",
		Short: "FlowState AI assistant CLI",
		Long:  "FlowState provides an AI assistant TUI plus CLI entry points for chat, serving, discovery, and session management.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRoot(cmd, application)
		},
	}

	flags := cmd.PersistentFlags()
	flags.String("config", application.ConfigPath(), "Path to the FlowState config file")
	flags.String("agents-dir", application.AgentsDir(), "Path to the agents directory")
	flags.String("skills-dir", application.SkillsDir(), "Path to the skills directory")
	flags.String("sessions-dir", application.SessionsDir(), "Path to the sessions directory")

	cmd.AddCommand(
		newChatCmd(application),
		newServeCmd(application),
		newAgentCmd(application),
		newSkillCmd(application),
		newDiscoverCmd(application),
		newSessionCmd(application),
	)

	return cmd
}

func runRoot(cmd *cobra.Command, application *app.App) error {
	_, err := fmt.Fprintf(
		cmd.OutOrStdout(),
		"root stub: launch TUI with config=%q agents-dir=%q skills-dir=%q sessions-dir=%q\n",
		application.ConfigPath(),
		application.AgentsDir(),
		application.SkillsDir(),
		application.SessionsDir(),
	)
	return err
}
