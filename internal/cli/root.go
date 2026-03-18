package cli

import (
	"fmt"
	"path/filepath"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/config"
	"github.com/spf13/cobra"
)

func NewRootCmd(application *app.App) *cobra.Command {
	var appPtr = application

	cfg := application.Config

	cmd := &cobra.Command{
		Use:   "flowstate",
		Short: "FlowState AI assistant CLI",
		Long:  "FlowState provides an AI assistant TUI plus CLI entry points for chat, serving, discovery, and session management.",
		Args:  cobra.NoArgs,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return initApp(cmd, cfg, &appPtr)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRoot(cmd, appPtr)
		},
	}

	flags := cmd.PersistentFlags()
	flags.String("config", application.ConfigPath(), "Path to the FlowState config file")
	flags.String("agents-dir", application.AgentsDir(), "Path to the agents directory")
	flags.String("skills-dir", application.SkillsDir(), "Path to the skills directory")
	flags.String("sessions-dir", application.SessionsDir(), "Path to the sessions directory")

	getApp := func() *app.App { return appPtr }

	cmd.AddCommand(
		newChatCmd(getApp),
		newRunCmd(getApp),
		newServeCmd(getApp),
		newAgentCmd(getApp),
		newSkillCmd(getApp),
		newDiscoverCmd(getApp),
		newSessionCmd(getApp),
	)

	return cmd
}

func initApp(cmd *cobra.Command, baseCfg *config.AppConfig, appPtr **app.App) error {
	flags := cmd.Flags()

	agentsDir, _ := flags.GetString("agents-dir")
	skillsDir, _ := flags.GetString("skills-dir")
	sessionsDir, _ := flags.GetString("sessions-dir")

	agentsDirChanged := flags.Changed("agents-dir")
	skillsDirChanged := flags.Changed("skills-dir")
	sessionsDirChanged := flags.Changed("sessions-dir")

	if !agentsDirChanged && !skillsDirChanged && !sessionsDirChanged {
		return nil
	}

	cfg := *baseCfg
	if agentsDirChanged {
		cfg.AgentDir = agentsDir
	}
	if skillsDirChanged {
		cfg.SkillDir = skillsDir
	}
	if sessionsDirChanged {
		cfg.DataDir = filepath.Dir(sessionsDir)
	}

	newApp, err := app.New(&cfg)
	if err != nil {
		return fmt.Errorf("reinitialising app with flags: %w", err)
	}
	*appPtr = newApp
	return nil
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
