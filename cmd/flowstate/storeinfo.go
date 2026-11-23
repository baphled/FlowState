package main

import (
	"fmt"
	"os"

	"github.com/baphled/flowstate/internal/app"
)

// routeStore handles `flowstate store info [--db path] [--config path]`;
// routed reports whether the invocation belonged to this route.
func routeStore(args []string, stdout, stderr *os.File) (int, bool) {
	if len(args) < 1 || args[0] != "store" {
		return exitOK, false
	}
	if len(args) < 2 || args[1] != "info" {
		writeError(stderr, fmt.Errorf("%w: expected store info", errUsageCmd))
		return exitUsage, true
	}
	fl, rest, err := parseFlags(args[2:])
	if err != nil {
		writeError(stderr, fmt.Errorf("%w: %v", errUsageCmd, err))
		return exitUsage, true
	}
	if len(rest) > 0 {
		writeError(stderr, fmt.Errorf("%w: unexpected argument %q", errUsageCmd, rest[0]))
		return exitUsage, true
	}
	if code, done := applySessionConfig(fl, stderr); done {
		return code, true
	}
	return cmdStoreInfo(fl.db, stdout, stderr), true
}

// cmdStoreInfo prints one JSON object describing the database at path.
// It is a diagnostic: the database is opened read-only and never migrated
// or created; the database file is not modified (SQLite may create or
// update -wal/-shm sidecars). Exit 0; 2 missing database or usage; 4
// unreadable.
func cmdStoreInfo(path string, stdout, stderr *os.File) int {
	info, err := app.ReadStoreInfo(path)
	if err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	if err := writeJSON(stdout, info); err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	return exitOK
}
