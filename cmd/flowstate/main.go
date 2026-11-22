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
	// SIGINT, SIGTERM and SIGHUP (terminal closed) cancel the context and
	// flag it so `session run` cancels its session and exits 130 per the
	// CLI contract. A swarm gate runs in its own process group, so it never
	// sees the terminal's signals: the CLI must cancel on them.
	ctx, stop := signal.NotifyContext(context.Background(), cancelSignals(signal.Ignored)...)
	code := dispatchContext(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// cancelSignals lists the signals that cancel a run. A hang-up is left out
// when the process was started with it ignored (nohup, a job scheduler):
// registering a handler would un-ignore it and cancel a run that was meant
// to outlive its terminal. It must be called before any handler is
// installed, because installing one changes what ignored reports.
func cancelSignals(ignored func(os.Signal) bool) []os.Signal {
	sigs := []os.Signal{os.Interrupt, syscall.SIGTERM}
	if !ignored(syscall.SIGHUP) {
		sigs = append(sigs, syscall.SIGHUP)
	}
	return sigs
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
