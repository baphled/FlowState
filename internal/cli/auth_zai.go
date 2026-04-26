package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/provider/zai"
	"github.com/spf13/cobra"
)

// envZAIAPIKey is the environment variable consulted before prompting
// the user for a Z.AI API key.
const envZAIAPIKey = "ZAI_API_KEY"

// flagZAIPlan is the cobra flag name used to pick the Z.AI subscription
// endpoint when running `flowstate auth zai --plan=...`.
const flagZAIPlan = "plan"

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
		Long: "Authenticate with Z.AI by providing your API key (read from ZAI_API_KEY when set, otherwise prompted).\n\n" +
			"Use --plan to pick the subscription endpoint:\n" +
			"  --plan=general   pay-per-token (https://api.z.ai/api/paas/v4)\n" +
			"  --plan=coding    coding-plan subscription (https://api.z.ai/api/coding/paas/v4)\n\n" +
			"Mixing keys with the wrong plan returns HTTP 429 / billing code 1113.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuthZAI(cmd, getApp())
		},
	}
	cmd.Flags().String(flagZAIPlan, "", "Z.AI subscription endpoint: 'general' (pay-per-token) or 'coding' (coding-plan)")
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
//   - Updates config with API key (and plan, when --plan is set) and saves
//     to config.yaml.
//   - Outputs success/error message to stdout/stderr.
func runAuthZAI(cmd *cobra.Command, application *app.App) error {
	cfg := application.Config

	if cfg.Providers.ZAI.APIKey != "" {
		if !confirmOverwrite(cmd, "zai") {
			fmt.Fprintln(cmd.OutOrStdout(), "Aborted; existing Z.AI API key kept.")
			return nil
		}
	}

	plan, err := readZAIPlanFlag(cmd)
	if err != nil {
		fmt.Fprintln(cmd.OutOrStderr(), "✗", err.Error())
		return err
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
	if plan != "" {
		cfg.Providers.ZAI.Plan = plan
	}
	if err := writeConfig(cfg); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	fmt.Fprintln(cmd.OutOrStdout(), "✓ Z.AI API key saved")
	if plan != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "✓ Z.AI plan set to %q\n", plan)
	}
	return nil
}

// readZAIPlanFlag returns the normalised --plan flag value.
//
// Expected:
//   - cmd is a non-nil cobra.Command with the "plan" flag registered.
//
// Returns:
//   - "" when the flag is unset.
//   - zai.PlanCoding or zai.PlanGeneral when set to a recognised value
//     (case-insensitive trim).
//   - An error when the flag is set to an unrecognised value.
//
// Side effects:
//   - None.
func readZAIPlanFlag(cmd *cobra.Command) (string, error) {
	raw, err := cmd.Flags().GetString(flagZAIPlan)
	if err != nil {
		return "", fmt.Errorf("reading --%s flag: %w", flagZAIPlan, err)
	}
	plan := strings.ToLower(strings.TrimSpace(raw))
	switch plan {
	case "":
		return "", nil
	case zai.PlanCoding, zai.PlanGeneral:
		return plan, nil
	default:
		return "", fmt.Errorf("invalid --%s value %q: must be %q or %q",
			flagZAIPlan, raw, zai.PlanGeneral, zai.PlanCoding)
	}
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
