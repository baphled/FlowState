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

	// Inject the autoresearch runner so wireDelegateToolIfEnabled can
	// register the autoresearch_run engine tool. This breaks the
	// app→cli import cycle: app holds the runner as an interface;
	// cli (which already imports app) provides the concrete implementation.
	application.SetAutoresearchRunner(NewAutoresearchAppRunner(application))
	application.SetAutoresearchPruner(NewAutoresearchPruneAppRunner(application))

	cfg := application.Config

	cmd := &cobra.Command{
		Use:   "flowstate",
		Short: "FlowState AI assistant CLI",
		Long:  "FlowState provides an AI assistant TUI plus CLI entry points for chat, serving, discovery, and session management.",
		Args:  cobra.NoArgs,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			// Bootstrap MUST run before the App's agent / swarm / skill
			// / gate registries are read, otherwise the engine starts
			// with empty registries and dispatch fails silently. Pre-
			// the rebuild-after-bootstrap branch, a freshly-installed
			// `flowstate serve` would seed the seven directories AFTER
			// setupAgentRegistry / loadSkills had already walked them
			// empty, so the engine had zero agents at request time even
			// though the dirs were correctly populated on disk.
			//
			// Gates for invoking Bootstrap + reconstruction:
			//   (a) resolved leaf command opts in via AnnotationBootstrap
			//   (b) appPtr.BootstrapDeferred() — i.e. constructed via
			//       SkipBootstrap=true (cmd/flowstate/main.go's path).
			//
			// Gate (b) is the critical guard against test pollution:
			// internal/cli specs use app.NewForTest (no bootstrap, no
			// SkipBootstrap flip) and then build NewRootCmd against the
			// test App with an isolated agentsDir. Without the
			// BootstrapDeferred() check, every annotated command's
			// PersistentPreRunE would seed all forty bundled agents
			// into the test's agentsDir, dirtying the surface working
			// tree and failing dozens of autoresearch specs. Apps
			// constructed via the legacy app.New (Bootstrap already ran
			// inline at construction) also return false, so a single
			// binary invocation never bootstraps twice.
			//
			// Built-in cobra paths (--help, --version, unknown
			// subcommand) never reach PersistentPreRunE so they are
			// inherently bootstrap-free; the annotation check covers
			// the RunE-bearing read-only commands (`models`, `session
			// list`, `config show`, ...) the brief carves out.
			if needsBootstrap(cmd) && appPtr.BootstrapDeferred() {
				app.Bootstrap(appPtr.Config)
				if err := rebuildAppAfterBootstrap(cmd, cfg, &appPtr); err != nil {
					return err
				}
				appPtr.MarkBootstrapRan()
			} else if err := initApp(cmd, cfg, &appPtr); err != nil {
				return err
			}
			// Re-inject after a potential app reinitialisation so the
			// runner's app pointer stays in sync.
			appPtr.SetAutoresearchRunner(NewAutoresearchAppRunner(appPtr))
			appPtr.SetAutoresearchPruner(NewAutoresearchPruneAppRunner(appPtr))
			return nil
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

	// Commands that exercise the engine (chat/run/serve), the agent or
	// swarm registries (agent/agents/swarm/discover/coordination), the
	// tool catalogue (tools), skill bundles (skill), vault tools, memory
	// tools, autoresearch, plan store, and auth user storage all expect
	// the seeded XDG_CONFIG layout. Each leaf command in those groups
	// inherits AnnotationBootstrap via MarkNeedsBootstrap. Read-only
	// surfaces (`models`, `session`, `config`) and pure plumbing
	// (`--help`, `--version`, unknown subcommands) are intentionally
	// left without the annotation so a fresh-install probe like
	// `flowstate --version` does not materialise permissions.yaml or the
	// agent/skill/swarm/gate seed directories.
	bootstrapping := []*cobra.Command{
		newChatCmd(getApp),
		newAuthCmd(getApp),
		newRunCmd(getApp),
		newServeCmd(getApp),
		newAgentCmd(getApp),
		newAgentsCmd(getApp),
		newVaultToolsCmd(getApp),
		newVaultCmd(getApp),
		newMemoryToolsCmd(getApp),
		newCoordinationCmd(getApp),
		newAutoresearchCmd(getApp),
		newSwarmCmd(getApp),
		newSkillCmd(getApp),
		newDiscoverCmd(getApp),
		NewPlanCommand(getApp),
		newToolsCmd(getApp),
	}
	for _, c := range bootstrapping {
		MarkNeedsBootstrap(c)
		cmd.AddCommand(c)
	}

	// Read-only commands — no XDG seeding required.
	cmd.AddCommand(
		newSessionCmd(getApp),
		newModelsCmd(getApp),
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
	cfg, changed, err := resolveFlagOverrides(cmd, baseCfg)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}

	// Skip bootstrap on reinit: the PersistentPreRunE that just called us
	// will fire app.Bootstrap immediately after this returns for any
	// command annotated with AnnotationBootstrap, so doing it here too
	// would just re-walk the embed FS for a no-op. Read-only commands
	// (`flowstate --config=... models`) get their bootstrap-free path
	// preserved this way; pre-this skip, reinit's app.New ran all eight
	// side effects unconditionally and re-introduced the hazard the
	// outer SkipBootstrap was meant to close.
	newApp, err := app.NewWithOptions(cfg, app.NewOptions{SkipBootstrap: true})
	if err != nil {
		return fmt.Errorf("reinitialising app with flags: %w", err)
	}
	*appPtr = newApp
	return nil
}

// rebuildAppAfterBootstrap reconstructs the App after the
// PersistentPreRunE has fired app.Bootstrap. The reconstruction is
// unconditional (unlike initApp's "skip when no flags changed") because
// the existing App's agent / swarm / skill / gate registries were loaded
// from the empty pre-Bootstrap dirs — those need a re-walk now that the
// seed dirs exist on disk. Reusing resolveFlagOverrides keeps --config /
// --agents-dir / --skills-dir / --sessions-dir flag handling identical
// to initApp's path, so users overriding the seeded location see their
// override populated post-Bootstrap.
//
// SkipBootstrap is set to true on the new App so the deferred-flag flip
// stays consistent — Bootstrap already ran above this call; PersistentPre
// RunE marks it ran via MarkBootstrapRan once the rebuild returns clean.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - baseCfg is a non-nil AppConfig.
//   - appPtr is a non-nil pointer to an App pointer.
//
// Side effects:
//   - Reassigns *appPtr to the freshly-constructed App with populated
//     registries.
func rebuildAppAfterBootstrap(cmd *cobra.Command, baseCfg *config.AppConfig, appPtr **app.App) error {
	cfg, _, err := resolveFlagOverrides(cmd, baseCfg)
	if err != nil {
		return err
	}
	newApp, err := app.NewWithOptions(cfg, app.NewOptions{SkipBootstrap: true})
	if err != nil {
		return fmt.Errorf("reconstructing app after bootstrap: %w", err)
	}
	*appPtr = newApp
	return nil
}

// resolveFlagOverrides reads the four persistent flags (`--config`,
// `--agents-dir`, `--skills-dir`, `--sessions-dir`) off cmd and applies
// any explicit user override to baseCfg, returning the effective config
// + whether any flag changed.
//
// Shared by initApp (which short-circuits when no flag changed) and
// rebuildAppAfterBootstrap (which reconstructs unconditionally). Splitting
// the reader out avoids a copy-paste drift between the two paths.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - baseCfg is a non-nil AppConfig.
//
// Returns:
//   - The effective AppConfig (may be the same pointer as baseCfg).
//   - true when any of the four flags was changed by the user.
//   - An error when --config points at an unreadable file.
//
// Side effects:
//   - When --config was changed: reads the config file from disk.
//   - Mutates the returned AppConfig's AgentDir / SkillDir / DataDir
//     to reflect the supplied flag values when changed.
func resolveFlagOverrides(cmd *cobra.Command, baseCfg *config.AppConfig) (*config.AppConfig, bool, error) {
	flags := cmd.Flags()

	configPath, err := flags.GetString("config")
	if err != nil {
		return nil, false, fmt.Errorf("reading config flag: %w", err)
	}
	agentsDir, err := flags.GetString("agents-dir")
	if err != nil {
		return nil, false, fmt.Errorf("reading agents-dir flag: %w", err)
	}
	skillsDir, err := flags.GetString("skills-dir")
	if err != nil {
		return nil, false, fmt.Errorf("reading skills-dir flag: %w", err)
	}
	sessionsDir, err := flags.GetString("sessions-dir")
	if err != nil {
		return nil, false, fmt.Errorf("reading sessions-dir flag: %w", err)
	}

	configChanged := flags.Changed("config")
	agentsDirChanged := flags.Changed("agents-dir")
	skillsDirChanged := flags.Changed("skills-dir")
	sessionsDirChanged := flags.Changed("sessions-dir")
	anyChanged := configChanged || agentsDirChanged || skillsDirChanged || sessionsDirChanged

	cfg := baseCfg
	if configChanged {
		loadedCfg, err := config.LoadConfigFromPath(configPath)
		if err != nil {
			return nil, false, fmt.Errorf("loading config from %q: %w", configPath, err)
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

	return cfg, anyChanged, nil
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
