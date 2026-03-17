package cli

import "github.com/spf13/cobra"

func newSkillCmd() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "skill",
        Short: "Inspect available skills",
        Long:  "Inspect skills available to FlowState and its agents.",
        Args:  cobra.NoArgs,
        RunE: func(cmd *cobra.Command, args []string) error {
            return cmd.Help()
        },
    }

    cmd.AddCommand(newSkillListCmd())
    return cmd
}

func newSkillListCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "list",
        Short: "List available skills",
        Long:  "List the skills available to FlowState. This stub prints a placeholder message.",
        Args:  cobra.NoArgs,
        RunE: func(cmd *cobra.Command, args []string) error {
            return writePlaceholder(cmd, "skill list stub
")
        },
    }
}
