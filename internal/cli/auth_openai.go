package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/oauth"
	"github.com/spf13/cobra"
)

// envOpenAIAPIKey is the environment variable consulted before prompting
// the user for an OpenAI API key.
const envOpenAIAPIKey = "OPENAI_API_KEY"

// defaultOpenAIClientID is FlowState's own OpenAI OAuth App client ID for
// the Device Flow. Operators wanting to point at a different app can
// override via `providers.openai.oauth.client_id` in config.yaml.
const defaultOpenAIClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

// resolveOpenAIClientID returns the OAuth client ID from config, falling back
// to the default when the config value is empty.
//
// Expected:
//   - cfg may be nil or contain an empty ClientID field.
//
// Returns:
//   - The configured ClientID when non-empty, or the default OpenAI client ID.
//
// Side effects:
//   - None.
func resolveOpenAIClientID(cfg *config.AppConfig) string {
	if cfg != nil && cfg.Providers.OpenAI.OAuth.ClientID != "" {
		return cfg.Providers.OpenAI.OAuth.ClientID
	}
	return defaultOpenAIClientID
}

// newAuthOpenAICmd creates the OpenAI authentication command with API key
// (default) and OAuth device flow subcommands.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command for OpenAI authentication.
//
// Side effects:
//   - Registers the openai subcommand and its oauth child.
func newAuthOpenAICmd(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "openai",
		Short: "Authenticate with OpenAI",
		Long:  "Authenticate with OpenAI using an API key (default) or OAuth device flow via `openai oauth`.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuthOpenAIKey(cmd, getApp())
		},
	}

	cmd.AddCommand(
		newAuthOpenAIOAuthCmd(getApp),
	)
	return cmd
}

// runAuthOpenAIKey authenticates with OpenAI using an API key or pasted OAuth token.
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
//   - Updates config with credential and saves to config.yaml.
func runAuthOpenAIKey(cmd *cobra.Command, application *app.App) error {
	cfg := application.Config

	if cfg.Providers.OpenAI.APIKey != "" {
		if !confirmOverwrite(cmd, "openai") {
			fmt.Fprintln(cmd.OutOrStdout(), "Aborted; existing OpenAI credential kept.")
			return nil
		}
	}

	credential := readAPIKey(cmd, envOpenAIAPIKey, "Enter your OpenAI API key or OAuth token: ")
	if credential == "" {
		return errors.New("reading openai credential")
	}
	if !isValidOpenAICredential(credential) {
		fmt.Fprintln(cmd.OutOrStderr(), "✗ Invalid credential format")
		fmt.Fprintln(cmd.OutOrStderr(), "Expected an OpenAI key (e.g. sk-...) or OAuth token (JWT beginning eyJ...)")
		return errors.New("invalid openai credential format")
	}

	cfg.Providers.OpenAI.APIKey = credential
	if err := writeConfig(cfg); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	fmt.Fprintln(cmd.OutOrStdout(), "✓ OpenAI credential saved")
	return nil
}

// newAuthOpenAIOAuthCmd creates the OpenAI OAuth device flow subcommand.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command for OpenAI OAuth device flow authentication.
//
// Side effects:
//   - Registers the oauth subcommand under openai.
func newAuthOpenAIOAuthCmd(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "oauth",
		Short: "Authenticate with OpenAI via OAuth device flow",
		Long:  "Start the OpenAI OAuth 2.0 Device Flow. A browser window will open where you can authorise FlowState.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuthOpenAIOAuth(cmd, getApp())
		},
	}
	return cmd
}

// runAuthOpenAIOAuth executes the OpenAI OAuth PKCE authorization flow.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - application is a non-nil App instance.
//
// Returns:
//   - An error if authentication fails, nil otherwise.
//
// Side effects:
//   - Starts a local HTTP callback server.
//   - Opens the browser for user authorization.
//   - Stores encrypted token in ~/.local/share/flowstate/tokens/
//   - Updates config with OAuth settings and saves to config.yaml.
func runAuthOpenAIOAuth(cmd *cobra.Command, application *app.App) error {
	cfg := application.Config

	if cfg.Providers.OpenAI.APIKey != "" {
		if !confirmOverwrite(cmd, "openai") {
			fmt.Fprintln(cmd.OutOrStdout(), "Aborted; existing OpenAI credential kept.")
			return nil
		}
	}

	fmt.Fprintln(cmd.OutOrStdout(), "Starting OpenAI OAuth authentication...")
	fmt.Fprintln(cmd.OutOrStdout())

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	clientID := resolveOpenAIClientID(cfg)
	authURL, resultCh, errCh, cleanup, err := oauth.StartPKCEFlow(ctx, clientID, 0)
	if err != nil {
		return fmt.Errorf("starting oauth flow: %w", err)
	}
	defer cleanup()

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "URL: %s\n", authURL)
	fmt.Fprintln(out)
	if err := OpenURL(authURL); err != nil {
		fmt.Fprintln(out, "(Could not auto-open the browser — open the URL above manually.)")
	} else {
		fmt.Fprintln(out, "Opening the authorisation page in your browser.")
	}
	fmt.Fprintln(out, "Log in and authorise FlowState when prompted.")
	fmt.Fprintln(out)

	spinner := NewSpinner(out, "Waiting for authorisation...")
	spinner.Start()

	var authResp *oauth.AuthorizationResponse
	select {
	case authResp = <-resultCh:
		spinner.Stop("")
	case err = <-errCh:
		spinner.Stop("")
		return fmt.Errorf("authorization failed: %w", err)
	case <-ctx.Done():
		spinner.Stop("")
		return fmt.Errorf("authorization timed out: %w", ctx.Err())
	}

	if authResp == nil {
		spinner.Stop("")
		return errors.New("no authorization response received")
	}

	tokenStore, err := oauth.NewEncryptedStore(application.Config.DataDir)
	if err != nil {
		return fmt.Errorf("creating token store: %w", err)
	}

	tokenResp := &oauth.TokenResponse{
		AccessToken:  authResp.AccessToken,
		RefreshToken: authResp.RefreshToken,
		TokenType:    authResp.TokenType,
		ExpiresIn:    authResp.ExpiresIn,
		ExpiresAt:    time.Now().Add(time.Duration(authResp.ExpiresIn) * time.Second),
	}

	if err := tokenStore.Store("openai", tokenResp); err != nil {
		return fmt.Errorf("storing openai token: %w", err)
	}

	cfg.Providers.OpenAI.APIKey = authResp.AccessToken
	cfg.Providers.OpenAI.OAuth.Enabled = true
	cfg.Providers.OpenAI.OAuth.UseOAuth = true
	cfg.Providers.OpenAI.OAuth.ClientID = clientID

	if err := writeConfig(cfg); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	fmt.Fprintln(cmd.OutOrStdout(), "✓ Authentication successful")
	fmt.Fprintln(cmd.OutOrStdout(), "OAuth token stored securely")
	return nil
}

// isValidOpenAICredential checks if a credential matches the expected format for
// OpenAI. API keys begin with `sk-`; OAuth tokens are JWTs beginning with `eyJ`.
//
// Expected:
//   - credential is the credential string to validate.
//
// Returns:
//   - true if the credential format is plausible, false otherwise.
//
// Side effects:
//   - None.
func isValidOpenAICredential(credential string) bool {
	if len(credential) < 20 {
		return false
	}
	return strings.HasPrefix(credential, "sk-") || strings.HasPrefix(credential, "eyJ")
}
