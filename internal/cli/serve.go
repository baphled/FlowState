package cli

import "github.com/spf13/cobra"

// ServeOptions stores serve flag values.
type ServeOptions struct {
	Port int
	Host string
}

func newServeCmd() *cobra.Command {
	opts := &ServeOptions{
		Port: 8080,
		Host: "localhost",
	}

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the FlowState HTTP API server",
		Long:  "Start the FlowState HTTP API server. This stub reports the selected host and port.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd, opts)
		},
	}

	flags := cmd.Flags()
	flags.IntVar(&opts.Port, "port", opts.Port, "Port to bind the HTTP server to")
	flags.StringVar(&opts.Host, "host", opts.Host, "Host interface to bind the HTTP server to")

	return cmd
}

func runServe(cmd *cobra.Command, opts *ServeOptions) error {
	return writePlaceholder(cmd, "serve stub: host=%q port=%d\n", opts.Host, opts.Port)
}
