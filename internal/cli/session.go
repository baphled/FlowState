package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

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
		Long:  "List saved FlowState sessions.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "No sessions yet.")
			return err
		},
	}
}

func newSessionResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume ID",
		Short: "Resume a saved session",
		Long:  "Resume a saved FlowState session.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "Resuming session: %s\n", args[0])
			return err
		},
	}
}
