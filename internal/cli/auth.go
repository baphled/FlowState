package cli

import (
	"github.com/baphled/flowstate/internal/app"
	"github.com/spf13/cobra"
)

// newAuthCmd creates the auth command group for provider authentication.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command with authentication subcommands.
//
// Side effects:
//   - Registers github-copilot and anthropic subcommands.
func newAuthCmd(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate with AI providers",
		Long:  "Authenticate with AI providers using OAuth or API keys.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(
		newAuthGitHubCmd(getApp),
		newAuthAnthropicCmd(getApp),
	)

	return cmd
}
