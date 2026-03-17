package cli

import (
    "fmt"
    "path/filepath"

    "github.com/baphled/flowstate/internal/config"
    "github.com/spf13/cobra"
)

// RootOptions stores the global CLI flag values.
type RootOptions struct {
    ConfigPath  string
    AgentsDir   string
    SkillsDir   string
    SessionsDir string
}

// NewRootCmd builds the FlowState Cobra command tree.
func NewRootCmd() *cobra.Command {
    defaults := config.DefaultConfig()
    opts := &RootOptions{
        ConfigPath:  filepath.Join(defaults.DataDir, "config.yaml"),
        AgentsDir:   defaults.AgentDir,
        SkillsDir:   defaults.SkillDir,
        SessionsDir: filepath.Join(defaults.DataDir, "sessions"),
    }

    cmd := &cobra.Command{
        Use:   "flowstate",
        Short: "FlowState AI assistant CLI",
        Long: "FlowState provides an AI assistant TUI plus CLI entry points for chat, serving, discovery, and session management.",
        Args:  cobra.NoArgs,
        RunE: func(cmd *cobra.Command, args []string) error {
            return runRoot(cmd, opts)
        },
    }

    flags := cmd.PersistentFlags()
    flags.StringVar(&opts.ConfigPath, "config", opts.ConfigPath, "Path to the FlowState config file")
    flags.StringVar(&opts.AgentsDir, "agents-dir", opts.AgentsDir, "Path to the agents directory")
    flags.StringVar(&opts.SkillsDir, "skills-dir", opts.SkillsDir, "Path to the skills directory")
    flags.StringVar(&opts.SessionsDir, "sessions-dir", opts.SessionsDir, "Path to the sessions directory")

    cmd.AddCommand(
        newChatCmd(),
        newServeCmd(),
        newAgentCmd(),
        newSkillCmd(),
        newDiscoverCmd(),
        newSessionCmd(),
    )

    return cmd
}

func runRoot(cmd *cobra.Command, opts *RootOptions) error {
    return writePlaceholder(
        cmd,
        "root stub: launch TUI with config=%q agents-dir=%q skills-dir=%q sessions-dir=%q
",
        opts.ConfigPath,
        opts.AgentsDir,
        opts.SkillsDir,
        opts.SessionsDir,
    )
}

func writePlaceholder(cmd *cobra.Command, format string, args ...any) error {
    _, err := fmt.Fprintf(cmd.OutOrStdout(), format, args...)
    return err
}
