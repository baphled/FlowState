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

type ServeOptions struct {
	Port int
	Host string
}

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
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd, getApp(), opts)
		},
	}

	flags := cmd.Flags()
	flags.IntVar(&opts.Port, "port", opts.Port, "Port to bind the HTTP server to")
	flags.StringVar(&opts.Host, "host", opts.Host, "Host interface to bind the HTTP server to")

	return cmd
}

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
		return server.Shutdown(context.Background())
	case err := <-errChan:
		return err
	}
}
