package cli

import (
	"fmt"
	"path/filepath"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/config"
	"github.com/spf13/cobra"
)

// NewRootCmd creates the root command for the FlowState CLI.
//
// Expected:
//   - application is a non-nil initialised App instance.
//
// Returns:
//   - A configured cobra.Command with all subcommands registered.
//
// Side effects:
//   - Registers persistent flags for config, agents-dir, skills-dir, and sessions-dir.
func NewRootCmd(application *app.App) *cobra.Command {
	var appPtr = application

	cfg := application.Config

	cmd := &cobra.Command{
		Use:   "flowstate",
		Short: "FlowState AI assistant CLI",
		Long:  "FlowState provides an AI assistant TUI plus CLI entry points for chat, serving, discovery, and session management.",
		Args:  cobra.NoArgs,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return initApp(cmd, cfg, &appPtr)
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
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

// initApp reinitialises the application with command-line flag overrides.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - baseCfg is a non-nil AppConfig.
//   - appPtr is a non-nil pointer to an App pointer.
//
// Returns:
//   - nil if no flags were changed, or an error if reinitialisation fails.
//
// Side effects:
//   - Updates the App instance if directory flags are changed.
func initApp(cmd *cobra.Command, baseCfg *config.AppConfig, appPtr **app.App) error {
	flags := cmd.Flags()

	agentsDir, err := flags.GetString("agents-dir")
	if err != nil {
		return fmt.Errorf("reading agents-dir flag: %w", err)
	}
	skillsDir, err := flags.GetString("skills-dir")
	if err != nil {
		return fmt.Errorf("reading skills-dir flag: %w", err)
	}
	sessionsDir, err := flags.GetString("sessions-dir")
	if err != nil {
		return fmt.Errorf("reading sessions-dir flag: %w", err)
	}

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

// runRoot displays the root command stub with configuration information.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - application is a non-nil App instance.
//
// Returns:
//   - nil on success, or an error if output fails.
//
// Side effects:
//   - Writes configuration information to stdout.
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
