package cli

import (
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/app"
	"github.com/spf13/cobra"
)

// envOllamaCloudAPIKey is the environment variable consulted before prompting
// the user for an Ollama Cloud API key.
const envOllamaCloudAPIKey = "OLLAMA_CLOUD_API_KEY"

// newAuthOllamaCloudCmd creates the Ollama Cloud API key authentication command.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command for Ollama Cloud API key authentication.
//
// Side effects:
//   - Registers the ollamacloud subcommand.
func newAuthOllamaCloudCmd(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ollamacloud",
		Short: "Authenticate with Ollama Cloud via API key",
		Long:  "Authenticate with Ollama Cloud by providing your API key (read from OLLAMA_CLOUD_API_KEY when set, otherwise prompted).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuthOllamaCloud(cmd, getApp())
		},
	}
	return cmd
}

// runAuthOllamaCloud executes the Ollama Cloud API key authentication.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - application is a non-nil App instance.
//
// Returns:
//   - An error if authentication fails or the input is invalid, nil otherwise.
//
// Side effects:
//   - Reads OLLAMA_CLOUD_API_KEY env var or prompts the user via stdin.
//   - Asks for confirmation before overwriting an existing key.
//   - Updates config with API key and saves to config.yaml.
//   - Outputs success/error message to stdout/stderr.
func runAuthOllamaCloud(cmd *cobra.Command, application *app.App) error {
	cfg := application.Config

	if cfg.Providers.OllamaCloud.APIKey != "" {
		if !confirmOverwrite(cmd, "ollamacloud") {
			fmt.Fprintln(cmd.OutOrStdout(), "Aborted; existing Ollama Cloud API key kept.")
			return nil
		}
	}

	apiKey := readAPIKey(cmd, envOllamaCloudAPIKey, "Enter your Ollama Cloud API key: ")
	if apiKey == "" {
		return errors.New("reading ollama cloud api key")
	}
	if !isValidOllamaCloudKey(apiKey) {
		fmt.Fprintln(cmd.OutOrStderr(), "✗ Invalid API key format")
		fmt.Fprintln(cmd.OutOrStderr(), "Expected a non-empty Ollama Cloud key")
		return errors.New("invalid ollama cloud api key format")
	}

	cfg.Providers.OllamaCloud.APIKey = apiKey
	if err := writeConfig(cfg); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	fmt.Fprintln(cmd.OutOrStdout(), "✓ Ollama Cloud API key saved")
	return nil
}

// isValidOllamaCloudKey checks that an Ollama Cloud credential is plausibly
// well-formed. Ollama Cloud keys do not have a published canonical prefix, so
// the validator only ensures the key is non-empty and at least 8 characters —
// enough to reject pasted whitespace or obvious typos without locking out
// future formats.
//
// Expected:
//   - credential is the credential string to validate.
//
// Returns:
//   - true when the credential is plausibly an Ollama Cloud key, false otherwise.
//
// Side effects:
//   - None.
func isValidOllamaCloudKey(credential string) bool {
	return len(credential) >= 8
}
