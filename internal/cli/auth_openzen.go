package cli

import (
	"github.com/baphled/flowstate/internal/app"
	"github.com/spf13/cobra"
)

// envOpenZenAPIKey is the environment variable consulted before prompting
// the user for an OpenZen API key.
const envOpenZenAPIKey = "OPENZEN_API_KEY"

// newAuthOpenZenCmd creates the OpenZen API key authentication command.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command for OpenZen API key authentication.
//
// Side effects:
//   - Registers the openzen subcommand.
func newAuthOpenZenCmd(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "openzen",
		Short: "Authenticate with OpenZen via API key",
		Long:  "Authenticate with OpenZen by providing your API key (read from OPENZEN_API_KEY when set, otherwise prompted).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuthOpenZen(cmd, getApp())
		},
	}
	return cmd
}

// runAuthOpenZen executes the OpenZen API key authentication.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - application is a non-nil App instance.
//
// Returns:
//   - An error if authentication fails or the input is invalid, nil otherwise.
//
// Side effects:
//   - Reads OPENZEN_API_KEY env var or prompts the user via stdin.
//   - Asks for confirmation before overwriting an existing key.
//   - Updates config with API key and saves to config.yaml.
//   - Outputs success/error message to stdout/stderr.
func runAuthOpenZen(cmd *cobra.Command, application *app.App) error {
	cfg := application.Config
	return runAPIKeyAuth(
		cmd,
		envOpenZenAPIKey,
		"OpenZen",
		"Enter your OpenZen API key: ",
		cfg.Providers.OpenZen.APIKey,
		func(key string) error {
			cfg.Providers.OpenZen.APIKey = key
			return writeConfig(cfg)
		},
		isValidOpenZenKey,
		"Expected a non-empty OpenZen key (typically a 16+ character string)",
	)
}

// isValidOpenZenKey checks that an OpenZen credential is plausibly
// well-formed. OpenZen keys do not have a published canonical prefix, so
// the validator only ensures the key is non-empty and at least 16
// characters — enough to reject pasted whitespace or obvious typos without
// locking out future formats.
//
// Expected:
//   - credential is the credential string to validate.
//
// Returns:
//   - true when the credential is plausibly an OpenZen key, false otherwise.
//
// Side effects:
//   - None.
func isValidOpenZenKey(credential string) bool {
	return len(credential) >= 16
}
