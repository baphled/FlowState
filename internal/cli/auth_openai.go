package cli

import (
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/app"
	"github.com/spf13/cobra"
)

// envOpenAIAPIKey is the environment variable consulted before prompting
// the user for an OpenAI API key.
const envOpenAIAPIKey = "OPENAI_API_KEY"

// newAuthOpenAICmd creates the OpenAI API key authentication command.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command for OpenAI API key authentication.
//
// Side effects:
//   - Registers the openai subcommand.
func newAuthOpenAICmd(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "openai",
		Short: "Authenticate with OpenAI via API key",
		Long:  "Authenticate with OpenAI by providing your API key (read from OPENAI_API_KEY when set, otherwise prompted).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuthOpenAI(cmd, getApp())
		},
	}
	return cmd
}

// runAuthOpenAI executes the OpenAI API key authentication.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - application is a non-nil App instance.
//
// Returns:
//   - An error if authentication fails or the input is invalid, nil otherwise.
//
// Side effects:
//   - Reads OPENAI_API_KEY env var or prompts the user via stdin.
//   - Asks for confirmation before overwriting an existing key.
//   - Updates config with API key and saves to config.yaml.
//   - Outputs success/error message to stdout/stderr.
func runAuthOpenAI(cmd *cobra.Command, application *app.App) error {
	cfg := application.Config

	if cfg.Providers.OpenAI.APIKey != "" {
		if !confirmOverwrite(cmd, "openai") {
			fmt.Fprintln(cmd.OutOrStdout(), "Aborted; existing OpenAI API key kept.")
			return nil
		}
	}

	apiKey := readAPIKey(cmd, envOpenAIAPIKey, "Enter your OpenAI API key: ")
	if apiKey == "" {
		return errors.New("reading openai api key")
	}
	if !isValidOpenAIKey(apiKey) {
		fmt.Fprintln(cmd.OutOrStderr(), "✗ Invalid API key format")
		fmt.Fprintln(cmd.OutOrStderr(), "Expected an OpenAI key (e.g. sk-...)")
		return errors.New("invalid openai api key format")
	}

	cfg.Providers.OpenAI.APIKey = apiKey
	if err := writeConfig(cfg); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	fmt.Fprintln(cmd.OutOrStdout(), "✓ OpenAI API key saved")
	return nil
}

// isValidOpenAIKey checks if a credential matches the expected format for
// OpenAI. OpenAI keys begin with `sk-` and are reasonably long; the function
// is deliberately permissive because OpenAI ships several key prefixes
// (`sk-`, `sk-proj-`, `sk-svcacct-`) and we do not want to reject legitimate
// new prefixes.
//
// Expected:
//   - credential is the credential string to validate.
//
// Returns:
//   - true if the credential format is plausible, false otherwise.
//
// Side effects:
//   - None.
func isValidOpenAIKey(credential string) bool {
	if len(credential) < 20 {
		return false
	}
	return credential[:3] == "sk-"
}
