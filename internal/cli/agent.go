package cli

import (
	"encoding/json"
	"fmt"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/spf13/cobra"
)

func newAgentCmd(opts *RootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Inspect available agents",
		Long:  "Inspect available agents and agent metadata from the FlowState registry.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newAgentListCmd(opts), newAgentInfoCmd(opts))
	return cmd
}

func newAgentListCmd(opts *RootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available agents",
		Long:  "List the agents available to FlowState.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentList(cmd, opts)
		},
	}
}

func runAgentList(cmd *cobra.Command, opts *RootOptions) error {
	registry := agent.NewAgentRegistry()
	if err := registry.Discover(opts.AgentsDir); err != nil {
		return fmt.Errorf("discovering agents: %w", err)
	}

	manifests := registry.List()
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

func newAgentInfoCmd(opts *RootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "info NAME",
		Short: "Show agent details",
		Long:  "Show details for a named agent.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentInfo(cmd, opts, args[0])
		},
	}
}

func runAgentInfo(cmd *cobra.Command, opts *RootOptions, agentID string) error {
	registry := agent.NewAgentRegistry()
	if err := registry.Discover(opts.AgentsDir); err != nil {
		return fmt.Errorf("discovering agents: %w", err)
	}

	manifest, ok := registry.Get(agentID)
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
