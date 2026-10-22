package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/app"
	"github.com/spf13/cobra"
)

// defaultOllamaHost is used when neither config nor flags provide a host.
const defaultOllamaHost = "http://localhost:11434"

// ollamaProbeTimeout caps the HTTP probe so the subcommand cannot hang on
// a stalled local server.
const ollamaProbeTimeout = 3 * time.Second

// ollamaProbe is overridden in tests to avoid making real HTTP requests.
// It returns an error when the host is unreachable.
var ollamaProbe = probeOllamaHost

// newAuthOllamaCmd creates the Ollama reachability subcommand.
//
// Ollama does not require authentication; the subcommand only confirms that
// the host is reachable. It is registered for parity with the other
// `flowstate auth <provider>` subcommands so users can discover their
// status via `flowstate auth --help`.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command for the Ollama reachability check.
//
// Side effects:
//   - Registers the ollama subcommand.
func newAuthOllamaCmd(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ollama",
		Short: "Confirm the Ollama host is reachable",
		Long:  "Ollama does not require authentication. This subcommand confirms the configured Ollama host is reachable.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuthOllama(cmd, getApp())
		},
	}
	return cmd
}

// runAuthOllama executes the Ollama reachability probe.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - application is a non-nil App instance.
//
// Returns:
//   - An error if the host cannot be reached, nil otherwise.
//
// Side effects:
//   - Issues a GET request to <host>/api/tags with a short timeout.
//   - Outputs status to stdout/stderr.
func runAuthOllama(cmd *cobra.Command, application *app.App) error {
	host := application.Config.Providers.Ollama.Host
	if host == "" {
		host = defaultOllamaHost
	}

	if err := ollamaProbe(host); err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "✗ Ollama host %s is not reachable: %v\n", host, err)
		return fmt.Errorf("ollama host %s not reachable: %w", host, err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Ollama doesn't require authentication. Confirmed reachable at %s.\n", host)
	return nil
}

// probeOllamaHost issues a short GET request to <host>/api/tags and returns
// nil when the host responds with any HTTP status code. A non-nil error
// means the request could not complete (DNS failure, refused connection,
// timeout, etc).
//
// Expected:
//   - host is a base URL like "http://localhost:11434"; trailing slashes
//     are tolerated.
//
// Returns:
//   - nil when the probe succeeds, an error describing the failure
//     otherwise.
//
// Side effects:
//   - Issues an outbound HTTP GET request.
func probeOllamaHost(host string) error {
	endpoint := strings.TrimRight(host, "/") + "/api/tags"

	ctx, cancel := context.WithTimeout(context.Background(), ollamaProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return fmt.Errorf("building probe request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("probe failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= http.StatusInternalServerError {
		return errors.New("probe returned " + resp.Status)
	}
	return nil
}
