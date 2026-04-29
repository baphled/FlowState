package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/baphled/flowstate/internal/app"
	"github.com/spf13/cobra"
)

// newToolsCmd creates the "tools" command group for inspecting registered
// engine tools.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command with a "list" subcommand.
//
// Side effects:
//   - None.
func newToolsCmd(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tools",
		Short: "Inspect registered tools",
		Long:  "Commands for inspecting tools registered with the FlowState engine.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newToolsListCmd(getApp))
	return cmd
}

// newToolsListCmd creates the "tools list [--agent <name>]" subcommand.
// Without --agent, it lists tools registered for the default engine manifest.
// With --agent, it reconfigures the engine for the named agent before listing.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command with an optional --agent flag.
//
// Side effects:
//   - None.
func newToolsListCmd(getApp func() *app.App) *cobra.Command {
	var agentName string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tools registered with the engine",
		Long:  "List all tools registered with the FlowState engine, optionally filtered to a specific agent's manifest.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runToolsList(cmd, getApp(), agentName)
		},
	}

	cmd.Flags().StringVar(&agentName, "agent", "", "Agent name or alias to list tools for")
	return cmd
}

// runToolsList lists registered engine tools, optionally scoped to an agent.
//
// Expected:
//   - cmd is a non-nil cobra.Command with an initialised output writer.
//   - application is a non-nil App instance.
//   - agentName is the agent ID or alias to scope tool listing (empty = default).
//
// Returns:
//   - nil on success.
//   - non-nil error when the named agent cannot be found or output fails.
//
// Side effects:
//   - Calls ConfigureEngineForAgent when agentName is non-empty, which updates
//     the engine manifest filter and wires manifest-gated tools (delegation,
//     autoresearch) for the duration of this command.
//   - Writes the tool list to cmd.OutOrStdout().
func runToolsList(cmd *cobra.Command, application *app.App, agentName string) error {
	if agentName != "" {
		manifest, ok := application.Registry.GetByNameOrAlias(agentName)
		if !ok {
			return fmt.Errorf("agent %q not found in registry", agentName)
		}
		application.ConfigureEngineForAgent(*manifest)
	}

	tools := application.Engine.ToolSchemas()

	if len(tools) == 0 {
		_, err := fmt.Fprint(cmd.OutOrStdout(), "no tools registered for this agent\n")
		return err
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	_, err := fmt.Fprintln(w, "NAME\tDESCRIPTION")
	if err != nil {
		return err
	}
	for _, t := range tools {
		if _, err := fmt.Fprintf(w, "%s\t%s\n", t.Name, t.Description); err != nil {
			return err
		}
	}
	return w.Flush()
}
