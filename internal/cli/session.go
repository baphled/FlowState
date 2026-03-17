package cli

import "github.com/spf13/cobra"

func newSessionCmd() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "session",
        Short: "Inspect and resume sessions",
        Long:  "Inspect saved sessions and resume a previous conversation.",
        Args:  cobra.NoArgs,
        RunE: func(cmd *cobra.Command, args []string) error {
            return cmd.Help()
        },
    }

    cmd.AddCommand(newSessionListCmd(), newSessionResumeCmd())
    return cmd
}

func newSessionListCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "list",
        Short: "List saved sessions",
        Long:  "List saved FlowState sessions. This stub prints a placeholder message.",
        Args:  cobra.NoArgs,
        RunE: func(cmd *cobra.Command, args []string) error {
            return writePlaceholder(cmd, "session list stub
")
        },
    }
}

func newSessionResumeCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "resume ID",
        Short: "Resume a saved session",
        Long:  "Resume a saved FlowState session. This stub prints the requested session identifier.",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            return writePlaceholder(cmd, "session resume stub: id=%q
", args[0])
        },
    }
}
