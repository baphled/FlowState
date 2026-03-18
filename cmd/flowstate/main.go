// Package main provides the CLI entry point for the FlowState AI assistant application.
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/cli"
	"github.com/baphled/flowstate/internal/config"
)

func main() {
	os.Exit(run())
}

// run initialises and executes the FlowState application, returning an exit code.
//
// Returns:
//   - 0 on success, 1 on failure.
//
// Side effects:
//   - Loads configuration, initialises the application, and runs the CLI.
//   - Defers MCP server disconnection for graceful shutdown.
func run() int {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Printf("using default config: %v", err)
		cfg = config.DefaultConfig()
	}

	application, err := app.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer func() {
		if disconnectErr := application.DisconnectAll(); disconnectErr != nil {
			log.Printf("warning: MCP disconnect: %v", disconnectErr)
		}
	}()

	rootCmd := cli.NewRootCmd(application)

	if err := rootCmd.Execute(); err != nil {
		return 1
	}
	return 0
}
