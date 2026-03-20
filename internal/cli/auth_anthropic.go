package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/baphled/flowstate/internal/app"
	"github.com/spf13/cobra"
)

// newAuthAnthropicCmd creates the Anthropic API key authentication command.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command for Anthropic API key authentication.
//
// Side effects:
//   - Registers the anthropic subcommand.
func newAuthAnthropicCmd(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "anthropic",
		Short: "Authenticate with Anthropic via API key",
		Long:  "Authenticate with Anthropic by providing your API key.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuthAnthropic(cmd, getApp())
		},
	}
	return cmd
}

// runAuthAnthropic executes the Anthropic API key authentication.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - application is a non-nil App instance.
//
// Returns:
//   - An error if authentication fails or the input is invalid, nil otherwise.
//
// Side effects:
//   - Prompts user for API key via stdin.
//   - Validates credential format against Anthropic patterns (sk-ant-api03-*, sk-ant-oat01-*).
//   - Updates config with API key and saves to config.yaml.
//   - Outputs success/error message to stdout/stderr.
func runAuthAnthropic(cmd *cobra.Command, application *app.App) error {
	fmt.Fprint(cmd.OutOrStdout(), "Enter your Anthropic API key: ")

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return errors.New("reading api key from stdin")
	}

	apiKey := strings.TrimSpace(scanner.Text())

	if !isValidAnthropicKey(apiKey) {
		fmt.Fprintln(cmd.OutOrStderr(), "✗ Invalid API key format")
		fmt.Fprintln(cmd.OutOrStderr(), "Expected format: sk-ant-api03-...")
		return errors.New("invalid anthropic api key format")
	}

	cfg := application.Config
	cfg.Providers.Anthropic.APIKey = apiKey

	if err := writeConfig(cfg); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	fmt.Fprintln(cmd.OutOrStdout(), "✓ API key saved successfully")
	return nil
}

// isValidAnthropicKey checks if a credential matches the expected format for Anthropic.
//
// Expected:
//   - credential is the credential string to validate.
//
// Returns:
//   - true if the credential format is valid for Anthropic, false otherwise.
//
// Side effects:
//   - None.
func isValidAnthropicKey(credential string) bool {
	if credential == "" {
		return false
	}
	if len(credential) <= 13 {
		return false
	}
	prefix := credential[:13]
	return prefix == "sk-ant-api03-" || prefix == "sk-ant-oat01-"
}
