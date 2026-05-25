// Package main provides the CLI entry point for the FlowState AI assistant application.
package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/cli"
	"github.com/baphled/flowstate/internal/config"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
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
	logWriter := io.Discard
	logPath := filepath.Join(config.DataDir(), "flowstate.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err == nil {
		if f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			logWriter = f
			log.SetOutput(f)
			defer f.Close()
		}
	}
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Printf("using default config: %v", err)
		cfg = config.DefaultConfig()
	}

	app.ConfigureLogging(cfg.LogLevel, logWriter)

	// SkipBootstrap=true defers the eight first-run XDG-mutating side
	// effects (agent/skill/swarm/gate migration + seeding, mem0 wrapper
	// materialisation, permissions.yaml write) until after Cobra has
	// resolved the subcommand. The cli root's PersistentPreRunE then
	// invokes app.Bootstrap explicitly for commands annotated via
	// cli.AnnotationBootstrap. Pre-this flip, every invocation —
	// including `flowstate --version`, `flowstate help`, and `flowstate
	// <typo>` — materialised seven directories plus permissions.yaml
	// under whichever XDG_CONFIG_HOME resolved. See internal/app.Bootstrap
	// godoc + internal/cli/bootstrap_annotation.go for the gating
	// contract.
	application, err := app.NewWithOptions(cfg, app.NewOptions{SkipBootstrap: true})
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
	cli.SetVersion(rootCmd, version, commit, date)

	if err := rootCmd.Execute(); err != nil {
		return 1
	}
	return 0
}
