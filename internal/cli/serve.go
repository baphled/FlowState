package cli

import (
	"fmt"
	"net/http"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/discovery"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/spf13/cobra"
)

type ServeOptions struct {
	Port int
	Host string
}

func newServeCmd(rootOpts *RootOptions) *cobra.Command {
	opts := &ServeOptions{
		Port: 8080,
		Host: "localhost",
	}

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the FlowState HTTP API server",
		Long:  "Start the FlowState HTTP API server.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd, rootOpts, opts)
		},
	}

	flags := cmd.Flags()
	flags.IntVar(&opts.Port, "port", opts.Port, "Port to bind the HTTP server to")
	flags.StringVar(&opts.Host, "host", opts.Host, "Host interface to bind the HTTP server to")

	return cmd
}

func runServe(cmd *cobra.Command, rootOpts *RootOptions, opts *ServeOptions) error {
	registry := agent.NewAgentRegistry()
	_ = registry.Discover(rootOpts.AgentsDir)

	manifests := registry.List()
	manifestValues := make([]agent.AgentManifest, len(manifests))
	for i, m := range manifests {
		manifestValues[i] = *m
	}

	disc := discovery.NewAgentDiscovery(manifestValues)

	loader := skill.NewFileSkillLoader(rootOpts.SkillsDir)
	skills, _ := loader.LoadAll()

	server := api.NewServer(nil, registry, disc, skills)
	addr := fmt.Sprintf("%s:%d", opts.Host, opts.Port)

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Starting server on %s\n", addr)
	return http.ListenAndServe(addr, server.Handler())
}
