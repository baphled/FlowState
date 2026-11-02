// Package main is the entry point for the FlowState CLI.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// Version is set at build time via -ldflags "-X main.Version=x.y.z".
var Version = "dev"

func main() {
	// SIGINT cancels the context and flags it so `session run`
	// cancels its session and exits 130 per the CLI contract.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := dispatchContext(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// dispatchContext routes with a parent context; a SIGINT-cancelled
// context marks the sigintCancelledKey so subcommands can choose the
// exit-130 path.
func dispatchContext(ctx context.Context, args []string, stdout, stderr *os.File) int {
	if ctx.Value(sigintCancelledKey{}) != nil || ctx.Err() != nil {
		ctx = context.WithValue(ctx, sigintCancelledKey{}, true)
	}
	return dispatchContexted(ctx, args, stdout, stderr)
}
