// Package main is the entry point for the FlowState CLI.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/baphled/flowstate/internal/app"
)

// Version is set at build time via -ldflags "-X main.Version=x.y.z".
var Version = "dev"

func main() {
	// SIGINT, SIGTERM and SIGHUP (terminal closed) cancel the context and
	// flag it so `session run` cancels its session and exits 130 per the
	// CLI contract. A swarm gate runs in its own process group, so it never
	// sees the terminal's signals: the CLI must cancel on them. The
	// context's cause names the signal, which the engine records.
	ctx, stop := notifyCancelCause(context.Background(), cancelSignals(signal.Ignored)...)
	code := dispatchContext(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// notifyCancelCause is signal.NotifyContext with a cause: the returned
// context is cancelled with app.SignalCause of the first signal in sigs
// delivered, and stop releases the registration and the watcher.
func notifyCancelCause(parent context.Context, sigs ...os.Signal) (context.Context, context.CancelFunc) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, sigs...)
	return watchSignals(parent, ch, func() { signal.Stop(ch) })
}

// watchSignals cancels the returned context with the cause of the first
// signal received on ch. The returned stop calls release, ends the
// watcher and cancels the context (without a cause, so a signal that
// already fired keeps its own).
func watchSignals(parent context.Context, ch <-chan os.Signal, release func()) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	go func() {
		select {
		case sig := <-ch:
			cancel(app.SignalCause(sig))
		case <-ctx.Done():
		}
	}()
	return ctx, func() { release(); cancel(nil) }
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
