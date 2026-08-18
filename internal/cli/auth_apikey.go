package cli

import (
	"fmt"

	"github.com/baphled/flowstate/internal/app"
	"github.com/spf13/cobra"
)

// runAPIKeyAuth is the shared body of the per-provider API-key auth
// commands. Each provider file keeps only its command wiring, env-var
// constant, and credential policy; this helper owns the
// read-validate-persist flow so sibling provider files don't duplicate it.
//
// Expected:
//   - cmd is the invoking cobra command (used for I/O streams).
//   - envVar names the environment variable consulted before prompting.
//   - providerLabel names the provider for prompts and error text.
//   - prompt is the stdin prompt when the env var is unset.
//   - existingKey is the currently configured key ("" when unset).
//   - setKey persists the validated key into the application config.
//   - validateKey reports whether the credential is plausibly well-formed.
//   - keyFormatHint is printed when validation fails.
//
// Returns:
//   - An error when the key cannot be read or fails validation; nil on
//     success or an aborted overwrite.
//
// Side effects:
//   - Prompts the user via stdin when the env var is unset.
//   - Persists the config via setKey on success.
func runAPIKeyAuth(
	cmd *cobra.Command,
	envVar, providerLabel, prompt string,
	existingKey string,
	setKey func(string) error,
	validateKey func(string) bool,
	keyFormatHint string,
) error {
	if existingKey != "" {
		if !confirmOverwrite(cmd, providerLabel) {
			fmt.Fprintf(cmd.OutOrStdout(), "Aborted; existing %s API key kept.\n", providerLabel)
			return nil
		}
	}

	apiKey := readAPIKey(cmd, envVar, prompt)
	if apiKey == "" {
		return fmt.Errorf("reading %s api key", providerLabel)
	}
	if !validateKey(apiKey) {
		fmt.Fprintln(cmd.OutOrStderr(), "✗ Invalid API key format")
		fmt.Fprintln(cmd.OutOrStderr(), keyFormatHint)
		return fmt.Errorf("invalid %s api key format", providerLabel)
	}

	if err := setKey(apiKey); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "✓ %s API key saved\n", providerLabel)
	return nil
}

var _ app.App
