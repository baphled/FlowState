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
func cmdDoctor(ctx context.Context, st *app.Store, id string, stdout, stderr *os.File) int {
	if id == "" {
		writeError(stderr, fmt.Errorf("%w: session id required", app.ErrUsage))
		return exitUsage
	}
	d, err := app.DiagnoseSession(ctx, st, id)
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
