package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

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
//   - Registers anthropic, github-copilot, ollama, openai, openzen, and zai
//     subcommands.
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
		newAuthOpenAICmd(getApp),
		newAuthZAICmd(getApp),
		newAuthOpenZenCmd(getApp),
		newAuthOllamaCmd(getApp),
		newAuthOllamaCloudCmd(getApp),
	)

	return cmd
}

// promptLine writes prompt to cmd's stdout and reads a single trimmed line
// from os.Stdin. An empty string is returned on read failure, which the
// caller is expected to treat as "no key supplied" and surface as a format
// error to keep the UX consistent with the existing Anthropic flow.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - prompt is a human-readable message ending in a separator (e.g. ": ").
//
// Returns:
//   - The trimmed line read from stdin, or an empty string on error.
//
// Side effects:
//   - Writes prompt to cmd.OutOrStdout.
//   - Reads a line from os.Stdin.
func promptLine(cmd *cobra.Command, prompt string) string {
	fmt.Fprint(cmd.OutOrStdout(), prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return ""
	}
	return trimSpace(scanner.Text())
}

// confirmOverwrite asks the user whether to replace an existing credential
// for the named provider. Returns true when the user answers `y`/`yes`
// (case-insensitive), false otherwise.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - providerName is the human-readable provider label (e.g. "openai").
//
// Returns:
//   - true when the user explicitly confirms the overwrite.
//
// Side effects:
//   - Writes a confirmation prompt to cmd.OutOrStdout.
//   - Reads a line from os.Stdin.
func confirmOverwrite(cmd *cobra.Command, providerName string) bool {
	prompt := fmt.Sprintf("A %s credential is already configured. Overwrite? [y/N]: ", providerName)
	answer := strings.ToLower(promptLine(cmd, prompt))
	return answer == "y" || answer == "yes"
}

// trimSpace returns s with leading and trailing whitespace removed.
//
// Expected:
//   - s may be empty.
//
// Returns:
//   - The trimmed string.
//
// Side effects:
//   - None.
func trimSpace(s string) string {
	return strings.TrimSpace(s)
}

// readAPIKey returns the API key from the named environment variable when
// set, otherwise it prompts the user via stdin using the supplied message.
//
// Expected:
//   - cmd is a non-nil cobra.Command used for prompt I/O.
//   - envVar is the environment variable name to consult first.
//   - prompt is the message displayed when falling back to stdin.
//
// Returns:
//   - The trimmed API key, or an empty string when the user supplied
//     nothing.
//
// Side effects:
//   - Reads the environment.
//   - Reads from os.Stdin when the env var is unset.
//   - Writes the prompt to cmd.OutOrStdout.
func readAPIKey(cmd *cobra.Command, envVar, prompt string) string {
	if v := os.Getenv(envVar); v != "" {
		return trimSpace(v)
	}
	return promptLine(cmd, prompt)
}
