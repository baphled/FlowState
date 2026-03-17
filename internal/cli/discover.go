package cli

import (
    "strings"

    "github.com/spf13/cobra"
)

func newDiscoverCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "discover MESSAGE",
        Short: "Suggest an agent for a task",
        Long:  "Suggest an agent for a task description. This stub prints the supplied message.",
        Args:  cobra.MinimumNArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            return runDiscover(cmd, args)
        },
    }
}

func runDiscover(cmd *cobra.Command, args []string) error {
    return writePlaceholder(cmd, "discover stub: message=%q
", strings.Join(args, " "))
}
