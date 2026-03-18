package cli

import (
	"encoding/json"
	"fmt"

	"github.com/baphled/flowstate/internal/app"
	"github.com/spf13/cobra"
)

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
