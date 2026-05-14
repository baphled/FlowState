/*
Package carbonyl provides a bridge between the FlowState Vue web frontend
and the terminal via the Carbonyl browser engine.

Carbonyl renders a standard web page inside a terminal window by running
a headless Chromium instance and converting its output to ANSI escape
sequences. This package manages the Carbonyl subprocess lifecycle,
including startup validation, health monitoring via a watchdog goroutine,
graceful shutdown with SIGTERM→SIGKILL escalation, and crash reporting.

# Quick start

	cfg := carbonyl.DefaultConfig().
	    WithBinary("/usr/local/bin/carbonyl").
	    WithURL("http://localhost:5173")

	bridge := carbonyl.New(cfg)

	if err := bridge.Start(ctx); err != nil {
	    log.Fatal(err)
	}

	defer bridge.Stop()

	if err := bridge.Wait(); err != nil {
	    log.Printf("bridge exited: %v", err)
	}

# Error types

The package defines three sentinel error types for callers to inspect:

  - StartError: the Carbonyl process could not be started (missing binary,
    invalid configuration, immediate process exit).
  - ProcessCrashError: the process died unexpectedly after a successful
    start. Carries the PID for diagnostics.
  - NotRunningError: an operation was attempted on a bridge that is not
    currently running (double Stop, Wait before Start, etc.).

# Watchdog

A watchdog goroutine probes the subprocess every 5 seconds using signal
zero. If the process has died, the watchdog reports a ProcessCrashError
on the CrashChannel and marks the bridge as stopped. The watchdog
terminates cleanly when Stop is called or the parent context is cancelled.

# Signal handling

The bridge forwards SIGINT and SIGTERM to the Carbonyl subprocess so
that keyboard interrupts behave naturally in a terminal session.
*/
package carbonyl
