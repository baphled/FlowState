package cli

import (
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/app"
	"github.com/spf13/cobra"
)

// envZAIAPIKey is the environment variable consulted before prompting
// the user for a Z.AI API key.
const envZAIAPIKey = "ZAI_API_KEY"

// newAuthZAICmd creates the Z.AI API key authentication command.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command for Z.AI API key authentication.
//
// Side effects:
//   - Registers the zai subcommand.
func newAuthZAICmd(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "zai",
		Short: "Authenticate with Z.AI via API key",
		Long:  "Authenticate with Z.AI by providing your API key (read from ZAI_API_KEY when set, otherwise prompted).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuthZAI(cmd, getApp())
		},
	}
	return cmd
}

// runAuthZAI executes the Z.AI API key authentication.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - application is a non-nil App instance.
//
// Returns:
//   - An error if authentication fails or the input is invalid, nil otherwise.
//
// Side effects:
//   - Reads ZAI_API_KEY env var or prompts the user via stdin.
//   - Asks for confirmation before overwriting an existing key.
//   - Updates config with API key and saves to config.yaml.
//   - Outputs success/error message to stdout/stderr.
func runAuthZAI(cmd *cobra.Command, application *app.App) error {
	cfg := application.Config

	if cfg.Providers.ZAI.APIKey != "" {
		if !confirmOverwrite(cmd, "zai") {
			fmt.Fprintln(cmd.OutOrStdout(), "Aborted; existing Z.AI API key kept.")
			return nil
		}
	}

	apiKey := readAPIKey(cmd, envZAIAPIKey, "Enter your Z.AI API key: ")
	if apiKey == "" {
		return errors.New("reading zai api key")
	}
	if !isValidZAIKey(apiKey) {
		fmt.Fprintln(cmd.OutOrStderr(), "✗ Invalid API key format")
		fmt.Fprintln(cmd.OutOrStderr(), "Expected a non-empty Z.AI key (typically a 32+ character string)")
		return errors.New("invalid zai api key format")
	}

	cfg.Providers.ZAI.APIKey = apiKey
	if err := writeConfig(cfg); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	fmt.Fprintln(cmd.OutOrStdout(), "✓ Z.AI API key saved")
	return nil
}

// isValidZAIKey checks that a Z.AI credential is plausibly well-formed.
// Z.AI keys do not have a published canonical prefix, so the validator only
// ensures the key is non-empty and at least 16 characters — enough to reject
// pasted whitespace or obvious typos without locking out future formats.
//
// Expected:
//   - credential is the credential string to validate.
//
// Returns:
//   - true when the credential is plausibly a Z.AI key, false otherwise.
//
// Side effects:
//   - None.
func isValidZAIKey(credential string) bool {
	return len(credential) >= 16
}
