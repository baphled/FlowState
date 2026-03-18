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
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Printf("using default config: %v", err)
		cfg = config.DefaultConfig()
	}

	application, err := app.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	rootCmd := cli.NewRootCmd(application)
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
