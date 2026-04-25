package cli

import (
	"fmt"
	"path/filepath"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/config"
	"github.com/spf13/cobra"
)

var versionInfo struct {
	version string
	commit  string
	date    string
}

// SetVersion sets the version information for the root command.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - version, commit, and date are strings (may be empty).
//
// Side effects:
//   - Stores version info in package-level versionInfo struct.
//   - Sets cmd.Version to a formatted version string.
func SetVersion(cmd *cobra.Command, version, commit, date string) {
	versionInfo.version = version
	versionInfo.commit = commit
	versionInfo.date = date
	cmd.Version = fmt.Sprintf("%s (commit: %s, built: %s)", version, commit, date)
}

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
		newAuthCmd(getApp),
		newRunCmd(getApp),
		newServeCmd(getApp),
		newAgentCmd(getApp),
		newAgentsCmd(getApp),
		newCoordinationCmd(getApp),
		newSkillCmd(getApp),
		newDiscoverCmd(getApp),
		newSessionCmd(getApp),
		newModelsCmd(getApp),
		NewPlanCommand(getApp),
		newConfigCmd(getApp),
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
//   - Updates the App instance if directory flags or config path are changed.
func initApp(cmd *cobra.Command, baseCfg *config.AppConfig, appPtr **app.App) error {
	flags := cmd.Flags()

	configPath, err := flags.GetString("config")
	if err != nil {
		return fmt.Errorf("reading config flag: %w", err)
	}
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

	configChanged := flags.Changed("config")
	agentsDirChanged := flags.Changed("agents-dir")
	skillsDirChanged := flags.Changed("skills-dir")
	sessionsDirChanged := flags.Changed("sessions-dir")

	if !configChanged && !agentsDirChanged && !skillsDirChanged && !sessionsDirChanged {
		return nil
	}

	cfg := baseCfg
	if configChanged {
		loadedCfg, err := config.LoadConfigFromPath(configPath)
		if err != nil {
			return fmt.Errorf("loading config from %q: %w", configPath, err)
		}
		cfg = loadedCfg
	}

	if agentsDirChanged {
		cfg.AgentDir = agentsDir
	}
	if skillsDirChanged {
		cfg.SkillDir = skillsDir
	}
	if sessionsDirChanged {
		cfg.DataDir = filepath.Dir(sessionsDir)
	}

	newApp, err := app.New(cfg)
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
