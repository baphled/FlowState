package cli

import (
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/discovery"
	"github.com/spf13/cobra"
)

func newDiscoverCmd(opts *RootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "discover MESSAGE",
		Short: "Suggest an agent for a task",
		Long:  "Suggest an agent for a task description.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDiscover(cmd, opts, args)
		},
	}
}

func runDiscover(cmd *cobra.Command, opts *RootOptions, args []string) error {
	registry := agent.NewAgentRegistry()
	if err := registry.Discover(opts.AgentsDir); err != nil {
		return fmt.Errorf("discovering agents: %w", err)
	}

	manifests := registry.List()
	if len(manifests) == 0 {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "No agents available for discovery.")
		return err
	}

	manifestValues := make([]agent.AgentManifest, len(manifests))
	for i, m := range manifests {
		manifestValues[i] = *m
	}

	disc := discovery.NewAgentDiscovery(manifestValues)
	message := strings.Join(args, " ")
	suggestions := disc.Suggest(message)

	if len(suggestions) == 0 {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "No matching agents found.")
		return err
	}

	for _, s := range suggestions {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s (confidence: %.2f): %s\n", s.AgentID, s.Confidence, s.Reason)
		if err != nil {
			return err
		}
	}
	return nil
}
