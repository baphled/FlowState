package cli

import (
	"fmt"

	"github.com/baphled/flowstate/internal/app"
	"github.com/spf13/cobra"
)

// newConfigCmd returns the config command group.
//
// Expected:
//   - getApp is a function that returns the current App instance.
//
// Returns:
//   - A cobra.Command that groups configuration management subcommands.
//
// Side effects:
//   - None.
func newConfigCmd(_ func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Configuration management commands",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newHarnessConfigCmd())
	return cmd
}

// newHarnessConfigCmd returns the harness config subcommand.
//
// Returns:
//   - A cobra.Command that prints the default harness configuration as YAML.
//
// Side effects:
//   - None.
func newHarnessConfigCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "harness",
		Short: "Print default harness configuration as YAML",
		RunE: func(cmd *cobra.Command, _ []string) error {
			yamlStr, err := app.HarnessConfigYAML()
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), yamlStr)
			return err
		},
	}
}
