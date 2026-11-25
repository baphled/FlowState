package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/baphled/flowstate/internal/app"
)

// cmdCompact implements `flowstate session compact <id> [--instructions text]`: one manual
// context compaction (ADR-010). The summary is written by the session's own provider and
// model, so --provider and --model are refused, as is every flag that does not apply (exit 2).
// It takes the lease through the judged acquire (`compact:<uuid>`); a live holder or an
// unjudgeable (legacy or other-machine) row is refused with nothing written; a crashed judged
// `run:`/`compact:` row of this machine is taken over; it runs crash recovery under the lease.
// It is refused unless the session is active or paused, and leaves a paused session paused. No budget pool applies: a manual compaction has no loop and draws nothing. It prints one
// JSON object (session-compact) and exits 0. Exit 2 usage, including no_cut (nothing new to
// compact; a request that cannot be met, with no new error code); 1 unknown session or a
// held lease, as `session run` reports them; 6 terminal session; 3 a provider failure, or a
// summary that is empty, cut off at the token cap or invalid; 4 config_invalid (the
// database or crash recovery failed); 7 replay_mismatch; 130 cancelled by a signal, with no
// compaction event written: a compaction is not a run, so the session is not cancelled. The exec
// loop honours the compaction on its next run; the plain text-turn path never replays, so
// it is unaffected.
func cmdCompact(ctx context.Context, st *app.Store, id string, fl *flags, stdout, stderr *os.File) int {
	if id == "" {
		writeError(stderr, fmt.Errorf("%w: session id required", app.ErrUsage))
		return exitUsage
	}
	if name, ok := inapplicableCompactFlag(fl); ok {
		writeError(stderr, fmt.Errorf("%w: %s does not apply to `session compact`", app.ErrUsage, name))
		return exitUsage
	}
	if err := app.ValidateInstructions(fl.instructions); err != nil {
		writeError(stderr, err)
		return exitUsage
	}
	sess, err := loadRunnableSession(ctx, st, id)
	if err != nil {
		return compactExit(ctx, err, stderr)
	}
	p, perr := providerResolver(sess.Provider)
	if perr != nil {
		writeError(stderr, providerFailure(perr))
		return exitProviderError
	}
	s, ok := p.(app.Streamer)
	if !ok {
		writeError(stderr, providerFailure(fmt.Errorf("provider %s does not support streaming turns", sess.Provider)))
		return exitProviderError
	}
	res, err := app.CompactSession(ctx, st, id, app.CompactOptions{Streamer: s, Instructions: fl.instructions})
	if err != nil {
		return compactExit(ctx, err, stderr)
	}
	out := map[string]any{"session": res.SessionID, "event_seq": res.EventSeq, "first_kept_seq": res.FirstKeptSeq,
		"tokens_before": res.TokensBefore, "tokens_after": res.TokensAfter, "reason": res.Reason, "model": res.Model,
		"usage": res.Usage}
	if err := writeJSON(stdout, out); err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	return exitOK
}

// inapplicableCompactFlag names the first flag given that `session compact` does not take, in a
// fixed order: --provider and --model (the summary is written by the session's own), the
// budget and loop limits (a compaction has no loop) and the tool, session-creation and message flags.
func inapplicableCompactFlag(fl *flags) (string, bool) {
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
		{"--body", fl.msgBody != ""},
	} {
		if f.given {
			return f.name, true
		}
	}
	return "", false
}

// compactExit prints a failed compaction and maps it onto its exit code. A context that is
// done is a cancellation wherever it was noticed.
func compactExit(ctx context.Context, err error, stderr *os.File) int {
	if errors.Is(err, app.ErrSessionCancelled) || ctx.Err() != nil {
		writeError(stderr, fmt.Errorf("%w: interrupted; no compaction event was written", app.ErrSessionCancelled))
		return exitSigint
	}
	writeError(stderr, err)
	if errors.Is(err, app.ErrProvider) {
		return exitProviderError
	}
	return exitCodeFor(err)
}
