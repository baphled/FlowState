package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/baphled/flowstate/internal/app"
)

// errLeaseKept reports a lease row that `session unlock` found and did not delete: it is the
// non-zero exit of a report of a row that cannot be judged, without --force (ADR-011). Its code is
// `internal`, exit 1; a live holder is `session_busy`, exit 9 (with or without --force), as for `session run`.
var errLeaseKept = errors.New("lease not deleted")

// cmdUnlock implements `flowstate session unlock <id> [--force]` (ADR-011 D8). Without --force it
// is a report: it prints the holder as one JSON object (session-unlock), deletes nothing and
// exits 1 when a lease row exists (exit 0, outcome no_lease, when there is none). With --force
// it deletes the row it read, appends one session.unlocked event (not for a terminal session),
// writes one stderr line naming what was deleted, with the warning that a legacy row may still
// be running, prints the JSON report and exits 0. A lease whose lock is held by a live process
// on this machine is refused even with --force: the holder is printed and the exit is 9
// (session_busy), as for a held lease elsewhere. Exit 2 usage (no id, or a flag that does not apply); 1 unknown
// session, or a lease that changed between the read and the delete; 4 config_invalid. A no-force
// run writes nothing to the lease row or the log, but may create the lock directory beside the
// database. Owner and host come from the row, so the JSON and the stderr line are escaped for a
// terminal.
func cmdUnlock(ctx context.Context, st *app.Store, id string, fl *flags, stdout, stderr *os.File) int {
	if id == "" {
		writeError(stderr, fmt.Errorf("%w: session id required", app.ErrUsage))
		return exitUsage
	}
	if name, ok := inapplicableUnlockFlag(fl); ok {
		writeError(stderr, fmt.Errorf("%w: %s does not apply to `session unlock`", app.ErrUsage, name))
		return exitUsage
	}
	report, err := app.UnlockSession(ctx, st, id, fl.force)
	if err != nil {
		if report.Outcome != "" { // a refused live holder, or a failure after the delete: show the row
			_ = writeJSON(stdout, unlockOutput(report))
		}
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	if err := writeJSON(stdout, unlockOutput(report)); err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	switch report.Outcome {
	case app.UnlockDeleted:
		writeStderrLine(stderr, "unlock: "+report.Message)
	case app.UnlockReported:
		writeError(stderr, fmt.Errorf("%w: %s", errLeaseKept, report.Message))
		return exitCodeFor(errLeaseKept)
	}
	return exitOK
}

// unlockOutput shapes the report for the session-unlock schema.
func unlockOutput(r app.UnlockReport) unlockResult {
	out := unlockResult{Session: r.SessionID, Item: r.Item, Outcome: string(r.Outcome),
		MachineMatches: r.MachineMatches, LegacyRow: r.LegacyRow, EventAppended: r.EventAppended, Message: r.Message}
	if r.Outcome != app.UnlockNoLease {
		holder := r.Holder
		out.Holder = &holder
	}
	return out
}

// writeStderrLine writes one line with control and format characters escaped, as the JSON output is.
func writeStderrLine(stderr *os.File, line string) {
	b := appendTerminalSafe(make([]byte, 0, len(line)+1), []byte(line), false)
	_, _ = stderr.Write(append(b, '\n'))
}

// inapplicableUnlockFlag names the first flag given that `session unlock` does not take, in a
// fixed order: the session-creation and provider flags, the budget and loop limits, the tool
// flags, and the flags of the other session commands. Only --force and --db apply.
func inapplicableUnlockFlag(fl *flags) (string, bool) {
	for _, f := range []struct {
		name  string
		given bool
	}{
		{"--provider", fl.provider != ""}, {"--model", fl.model != ""}, {"--budget", fl.budgetSet},
		{"--max-tokens", fl.maxTokens != 0}, {"--max-turns", fl.maxTurns != 0}, {"--reserve", fl.reserveSet},
		{"--allow", len(fl.allow) > 0}, {"--allow-file", fl.allowFile != ""}, {"--workdir", fl.workdir != ""},
		{"--agents", fl.agents != ""}, {"--tool", fl.tool != ""}, {"--tool-arg", fl.toolArg != ""},
		{"--task", fl.task != ""}, {"--parent", fl.parent != ""}, {"--status", fl.status != ""},
		{"--follow", fl.follow}, {"--manifest", fl.manifestPath != ""}, {"--from", fl.msgFrom != ""},
		{"--body", fl.msgBody != ""}, {"--instructions", fl.instructions != ""},
	} {
		if f.given {
			return f.name, true
		}
	}
	return "", false
}
