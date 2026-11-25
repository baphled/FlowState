package main

import (
	"context"
	"fmt"
	"os"

	"github.com/baphled/flowstate/internal/app"
)

// cmdDoctor implements `flowstate session doctor <id>`: a read-only
// diagnosis of one session's durable state through the owning layer
// (ADR-006). Diagnosis never mutates the session and never recovers
// it — recovery stays in the run preamble. Exit 0 for any
// successfully diagnosed session (including a crashed one); 1
// unknown session; 2 usage; 4 configuration/database failure.
func cmdDoctor(ctx context.Context, st *app.Store, id string, fl *flags, stdout, stderr *os.File) int {
	if id == "" {
		writeError(stderr, fmt.Errorf("%w: session id required", app.ErrUsage))
		return exitUsage
	}
	d, err := app.DiagnoseSessionWith(ctx, st, id, fl.config.Compaction)
	if err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	out := map[string]any{
		"session_id":       d.SessionID,
		"status":           d.Status,
		"crashed":          d.Crashed,
		"run_activity":     d.RunActivity,
		"pending_messages": d.PendingMessages,
		"reserved":         d.Reserved,
	}
	if d.Pool != nil {
		out["budget_pool"] = *d.Pool
		out["available"] = *d.Available
	} else {
		out["budget_pool"] = nil
		out["available"] = nil
	}
	out["usage"] = doctorUsage(d.Usage)
	out["compactions"] = doctorCompactions(d.Compactions)
	out["context_window"] = doctorWindow(d.ContextWindow)
	if len(d.Warnings) > 0 {
		out["warnings"] = d.Warnings
	}
	if d.FinishReason != "" {
		out["finish_reason"] = d.FinishReason
	}
	if d.Truncated {
		out["truncated"] = true
	}
	if err := writeJSON(stdout, out); err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	return exitOK
}

// doctorUsage is the usage object of the doctor output. The totals and the
// last context size are null when no turn reported usage (turns_reported 0), so
// "not reported" is never read as zero tokens used.
func doctorUsage(u app.SessionUsage) map[string]any {
	out := map[string]any{"turns_reported": u.Totals.UsageTurns, "turns_total": u.TurnsTotal}
	keys := []string{"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "last_context_tokens"}
	values := []int64{u.Totals.InputTokens, u.Totals.OutputTokens, u.Totals.CacheReadTokens,
		u.Totals.CacheWriteTokens, u.LastContextTokens}
	for i, key := range keys {
		if u.Totals.UsageTurns > 0 {
			out[key] = values[i]
		} else {
			out[key] = nil
		}
	}
	return out
}

// doctorCompactions is the compactions object of the doctor output. last_tokens_after is
// null without a compaction and the summary token totals are null until a compaction
// reported usage (summaries_reported 0), as the usage object does for turns. invalid counts
// the events the rebuild would refuse; above 0, the exec-loop `session run` exits 7 replay_mismatch
// (the plain text turn never replays the log).
func doctorCompactions(c app.CompactionStats) map[string]any {
	out := map[string]any{"count": c.Count, "invalid": c.Invalid, "summaries_reported": c.SummariesReported,
		"last_tokens_after": nil, "summary_input_tokens": nil, "summary_output_tokens": nil}
	if c.LastTokensAfter != nil {
		out["last_tokens_after"] = *c.LastTokensAfter
	}
	if c.SummariesReported > 0 {
		out["summary_input_tokens"], out["summary_output_tokens"] = c.SummaryInputTokens, c.SummaryOutputTokens
	}
	return out
}

// doctorWindow is the context_window object of the doctor output: tokens is
// null when the window is unknown, never 0.
func doctorWindow(w app.ContextWindow) map[string]any {
	if w.Tokens == 0 {
		return map[string]any{"tokens": nil, "source": w.Source}
	}
	return map[string]any{"tokens": w.Tokens, "source": w.Source}
}
