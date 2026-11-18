package main

import (
	"context"
	"fmt"
	"os"

	"github.com/baphled/flowstate/internal/app"
)

// routeSwarm routes the `swarm` command surface.
func routeSwarm(ctx context.Context, args []string, stdout, stderr *os.File) int {
	if len(args) < 2 {
		writeError(stderr, fmt.Errorf("%w: missing swarm subcommand", errUsageCmd))
		return exitUsage
	}
	fl, rest, err := parseFlags(args[2:])
	if err != nil {
		writeError(stderr, fmt.Errorf("%w: %v", errUsageCmd, err))
		return exitUsage
	}
	dbPath := fl.db
	if args[1] != "run" {
		writeError(stderr, fmt.Errorf("%w: unknown swarm subcommand %q", errUsageCmd, args[1]))
		return exitUsage
	}
	if len(rest) != 1 {
		writeError(stderr, fmt.Errorf("%w: swarm run takes one manifest path", errUsageCmd))
		return exitUsage
	}
	res, err := app.SwarmRun(ctx, dbPath, rest[0])
	if err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	if err := writeJSON(stdout, map[string]any{
		"manifest": res.Manifest.Name,
		"admitted": res.Admitted,
		"accepted": res.Gates.Accepted,
	}); err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	return exitOK
}
