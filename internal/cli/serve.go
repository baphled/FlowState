package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/baphled/flowstate/internal/app"
	"github.com/spf13/cobra"
)

// ServeOptions configures the HTTP API server.
type ServeOptions struct {
	Port int
	Host string
}

// newServeCmd creates the serve command for starting the HTTP API server.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command with serve options.
//
// Side effects:
//   - Registers serve command flags.
func newServeCmd(getApp func() *app.App) *cobra.Command {
	opts := &ServeOptions{
		Port: 8080,
		Host: "localhost",
	}

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the FlowState HTTP API server",
		Long:  "Start the FlowState HTTP API server.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServe(cmd, getApp(), opts)
		},
	}

	flags := cmd.Flags()
	flags.IntVar(&opts.Port, "port", opts.Port, "Port to bind the HTTP server to")
	flags.StringVar(&opts.Host, "host", opts.Host, "Host interface to bind the HTTP server to")

	return cmd
}

// runServe starts the HTTP API server and handles graceful shutdown.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - application is a non-nil App instance with a configured API handler.
//   - opts is a non-nil ServeOptions with valid port and host.
//
// Returns:
//   - nil on successful shutdown, or an error if server startup or shutdown fails.
//
// Side effects:
//   - Starts HTTP server, listens for interrupt signals, performs graceful shutdown.
func runServe(cmd *cobra.Command, application *app.App, opts *ServeOptions) error {
	addr := fmt.Sprintf("%s:%d", opts.Host, opts.Port)

	server := &http.Server{
		Addr:              addr,
		Handler:           application.API.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errChan := make(chan error, 1)
	go func() {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Starting server on %s\n", addr)
		errChan <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Shutting down server...")
		if err := server.Shutdown(context.Background()); err != nil {
			return err
		}
		// H3: drain engine-owned background work before returning.
		// http.Server.Shutdown only waits for HTTP handlers; without
		// this call, session splitters' persist workers and L3
		// knowledge-extraction goroutines get killed at process exit,
		// orphaning `.tmp` files on disk.
		if application.Engine != nil {
			drainCtx, drainCancel := context.WithTimeout(context.Background(), engineShutdownTimeout)
			defer drainCancel()
			if err := application.Engine.Shutdown(drainCtx); err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"warning: engine shutdown did not complete within %s: %v\n",
					engineShutdownTimeout, err,
				)
			}
		}
		return nil
	case err := <-errChan:
		return err
	}
}

// engineShutdownTimeout bounds the wait for engine-owned background
// goroutines (splitter persist workers + L3 extractions) to drain
// after http.Server.Shutdown returns. 30s matches the L3
// extractor's per-run LLM deadline so an extraction in flight at
// SIGTERM has headroom to finish; persist workers complete in
// sub-second for realistic channel depths.
const engineShutdownTimeout = 30 * time.Second
