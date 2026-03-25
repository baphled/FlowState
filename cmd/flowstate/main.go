// Package main is the entry point for the FlowState CLI.
package main

import (
	"fmt"
	"os"
)

// Version is set at build time via -ldflags "-X main.Version=x.y.z".
var Version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Printf("flowstate %s\n", Version)
		os.Exit(0)
	}

	fmt.Println("FlowState — AI assistant TUI")
	fmt.Println("Run with --version for version information.")
}
