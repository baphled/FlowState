package cli

import (
	"encoding/json"
	"fmt"

	"github.com/baphled/flowstate/internal/app"
	"github.com/spf13/cobra"
)

// newAgentCmd creates the agent command for inspecting available agents.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command with agent subcommands.
//
// Side effects:
//   - Registers the agent list and info subcommands.
func newAgentCmd(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Inspect available agents",
		Long:  "Inspect available agents and agent metadata from the FlowState registry.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newAgentListCmd(getApp), newAgentInfoCmd(getApp))
	return cmd
}

// newAgentListCmd creates the agent list subcommand.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command for listing agents.
//
// Side effects:
//   - None.
func newAgentListCmd(getApp func() *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available agents",
		Long:  "List the agents available to FlowState.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAgentList(cmd, getApp())
		},
	}
}

// runAgentList lists all available agents from the registry.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - application is a non-nil App instance with a populated registry.
//
// Returns:
//   - nil on success, or an error if output fails.
//
// Side effects:
//   - Writes agent list to stdout.
func runAgentList(cmd *cobra.Command, application *app.App) error {
	manifests := application.Registry.List()
	if len(manifests) == 0 {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "No agents found.")
		return err
	}

	for _, m := range manifests {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (%s)\n", m.ID, m.Name, m.Complexity)
		if err != nil {
			return err
		}
	}
	return nil
}

// newAgentInfoCmd creates the agent info subcommand.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command for displaying agent details.
//
// Side effects:
//   - None.
func newAgentInfoCmd(getApp func() *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "info NAME",
		Short: "Show agent details",
		Long:  "Show details for a named agent.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentInfo(cmd, getApp(), args[0])
		},
	}
}

// runAgentInfo displays detailed information for a named agent.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - application is a non-nil App instance with a populated registry.
//   - agentID is a non-empty string.
//
// Returns:
//   - nil on success, or an error if the agent is not found or output fails.
//
// Side effects:
//   - Writes agent details as JSON to stdout.
func runAgentInfo(cmd *cobra.Command, application *app.App, agentID string) error {
	manifest, ok := application.Registry.Get(agentID)
	if !ok {
		return fmt.Errorf("agent %q not found", agentID)
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding agent: %w", err)
	}

	_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
	return err
}
