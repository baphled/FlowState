package cli

import (
	"fmt"

	"github.com/baphled/flowstate/internal/app"
	"github.com/spf13/cobra"
)

// newModelsCmd creates the models command for listing available models.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command for listing models.
//
// Side effects:
//   - None.
func newModelsCmd(getApp func() *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "models",
		Short: "List available models",
		Long:  "List models from all configured providers.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runModelsList(cmd, getApp())
		},
	}
}

// runModelsList lists all available models from registered providers.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - application is a non-nil App instance.
//
// Returns:
//   - nil on success, or an error if output fails.
//
// Side effects:
//   - Writes model list to stdout.
func runModelsList(cmd *cobra.Command, application *app.App) error {
	models, err := application.ListModels()
	if err != nil {
		return err
	}

	if len(models) == 0 {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "No models available.")
		return err
	}

	for _, model := range models {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%d\n", model.Provider, model.ID, model.ContextLength)
		if err != nil {
			return err
		}
	}
	return nil
}
