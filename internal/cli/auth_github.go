package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/oauth"
	"github.com/spf13/cobra"
)

const defaultGitHubClientID = "Ov23liZeaYyl1NyI50K1"

// resolveGitHubClientID returns the OAuth client ID from config, falling back
// to the default when the config value is empty.
//
// Expected:
//   - cfg may be nil or contain an empty ClientID field.
//
// Returns:
//   - The configured ClientID when non-empty, or the default GitHub client ID.
//
// Side effects:
//   - None.
func resolveGitHubClientID(cfg *config.AppConfig) string {
	if cfg != nil && cfg.Providers.GitHub.OAuth.ClientID != "" {
		return cfg.Providers.GitHub.OAuth.ClientID
	}
	return defaultGitHubClientID
}

// newAuthGitHubCmd creates the GitHub Copilot authentication command via OAuth Device Flow.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command for GitHub OAuth authentication.
//
// Side effects:
//   - Registers the github-copilot subcommand.
func newAuthGitHubCmd(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "github-copilot",
		Short: "Authenticate with GitHub Copilot via OAuth",
		Long:  "Authenticate with GitHub Copilot using OAuth 2.0 Device Flow.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuthGitHub(cmd, getApp())
		},
	}
	return cmd
}

// runAuthGitHub executes the GitHub OAuth Device Flow authentication.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - application is a non-nil App instance.
//
// Returns:
//   - An error if authentication fails, nil otherwise.
//
// Side effects:
//   - Initiates GitHub OAuth device flow.
//   - Polls for user authorization.
//   - Stores encrypted token in ~/.local/share/flowstate/tokens/
//   - Updates config with OAuth settings and saves to config.yaml.
//   - Outputs authentication status and instructions to stdout.
func runAuthGitHub(cmd *cobra.Command, application *app.App) error {
	fmt.Fprintln(cmd.OutOrStdout(), "Starting GitHub OAuth authentication...")
	fmt.Fprintln(cmd.OutOrStdout())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	clientID := resolveGitHubClientID(application.Config)
	ghProvider := oauth.NewGitHub(clientID)

	dcResp, err := ghProvider.InitiateFlow(ctx)
	if err != nil {
		return fmt.Errorf("initiating github oauth flow: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Device code: %s\n", dcResp.DeviceCode)
	fmt.Fprintf(cmd.OutOrStdout(), "User code: %s\n", dcResp.UserCode)
	fmt.Fprintf(cmd.OutOrStdout(), "Verification URL: %s\n", dcResp.VerificationURI)
	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintln(cmd.OutOrStdout(), "Waiting for authorization...")

	flowResult, err := ghProvider.PollToken(ctx, dcResp.DeviceCode, dcResp.Interval)
	if err != nil {
		return fmt.Errorf("polling github token: %w", err)
	}

	return handleGitHubFlowResult(cmd, application, flowResult)
}

// handleGitHubFlowResult processes the GitHub OAuth flow result.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - application is a non-nil App instance.
//   - flowResult is a non-nil FlowResult from PollToken.
//
// Returns:
//   - An error if the flow failed or state is invalid, nil on success.
//
// Side effects:
//   - Stores encrypted token for approved flows.
//   - Updates config and writes to config.yaml.
//   - Outputs result message to stdout/stderr.
func handleGitHubFlowResult(cmd *cobra.Command, application *app.App, flowResult *oauth.FlowResult) error {
	switch flowResult.State {
	case oauth.StateApproved:
		return handleGitHubApproved(cmd, application, flowResult)

	case oauth.StateExpired:
		fmt.Fprintln(cmd.OutOrStderr(), "✗ Authorization expired")
		fmt.Fprintln(cmd.OutOrStderr(), "Please restart the authentication flow")
		return errors.New("authorization expired")

	case oauth.StateError:
		fmt.Fprintf(cmd.OutOrStderr(), "✗ Authorization error: %s\n", flowResult.ErrorMessage)
		return fmt.Errorf("authorization error: %s", flowResult.ErrorMessage)

	default:
		return fmt.Errorf("unexpected oauth state: %d", flowResult.State)
	}
}

// handleGitHubApproved completes the GitHub OAuth flow after user approval.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - application is a non-nil App instance.
//   - flowResult is a non-nil FlowResult with StateApproved and a valid token.
//
// Returns:
//   - An error if token storage or config writing fails, nil on success.
//
// Side effects:
//   - Stores encrypted token in the data directory.
//   - Updates application config with token and OAuth settings.
//   - Writes config to config.yaml.
//   - Outputs success message to stdout.
func handleGitHubApproved(cmd *cobra.Command, application *app.App, flowResult *oauth.FlowResult) error {
	if flowResult.Token == nil {
		return errors.New("approved but no token received")
	}

	tokenStore, err := oauth.NewEncryptedStore(application.Config.DataDir)
	if err != nil {
		return fmt.Errorf("creating token store: %w", err)
	}

	if err := tokenStore.Store("github-copilot", flowResult.Token); err != nil {
		return fmt.Errorf("storing github token: %w", err)
	}

	cfg := application.Config
	cfg.Providers.GitHub.APIKey = flowResult.Token.AccessToken
	cfg.Providers.GitHub.OAuth.Enabled = true
	cfg.Providers.GitHub.OAuth.UseOAuth = true
	cfg.Providers.GitHub.OAuth.ClientID = resolveGitHubClientID(cfg)

	if err := writeConfig(cfg); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	fmt.Fprintln(cmd.OutOrStdout(), "✓ Authentication successful")
	fmt.Fprintln(cmd.OutOrStdout(), "Token stored securely")
	return nil
}

// writeConfig persists the given configuration to the config file.
//
// Expected:
//   - cfg is a non-nil AppConfig.
//
// Returns:
//   - An error if marshalling or writing fails, nil otherwise.
//
// Side effects:
//   - Writes configuration to ~/.config/flowstate/config.yaml.
func writeConfig(cfg *config.AppConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}
	path := filepath.Join(config.Dir(), "config.yaml")
	return os.WriteFile(path, data, 0o600)
}
