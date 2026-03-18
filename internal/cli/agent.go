package cli

import "github.com/spf13/cobra"

func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Inspect available agents",
		Long:  "Inspect available agents and agent metadata from the FlowState registry.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newAgentListCmd(), newAgentInfoCmd())
	return cmd
}

func newAgentListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available agents",
		Long:  "List the agents available to FlowState. This stub prints a placeholder message.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return writePlaceholder(cmd, "agent list stub\n")
		},
	}
}

func newAgentInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info NAME",
		Short: "Show agent details",
		Long:  "Show details for a named agent. This stub prints the requested agent name.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return writePlaceholder(cmd, "agent info stub: name=%q\n", args[0])
		},
	}
}
