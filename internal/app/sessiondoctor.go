// Package app — sessiondoctor.go owns the read-only session doctor
// use case: one aggregation of existing primitives into a
// SessionDiagnosis for the CLI surface. It performs no writes and no
// recovery; the crash-recovery mark stays in the run preamble
// (ADR-006).
package app

import (
	"context"
	"fmt"

	"github.com/baphled/flowstate/internal/provider"
)

// DiagnoseSession aggregates existing primitives into one read-only
// diagnosis: st.GetSession (raw status; errors pass through
// unchanged so an unknown id stays session_not_found, exit 1 — it
// must NOT be wrapped in ErrUsage, which ErrorCode matches first),
// DetectCrashedSession, the run-activity predicate, the
// BudgetOverview snapshot, the pending steering-message count and the
// last turn.end's finish reason.
// Crashed is additionally gated on an active row status: only a
// session the engine could still resume is reported as crashed.
func DiagnoseSession(ctx context.Context, st *Store, sessionID string) (SessionDiagnosis, error) {
	return DiagnoseSessionWith(ctx, st, sessionID, CompactionConfig{})
}

// DiagnoseSessionWith is DiagnoseSession that also resolves the session's
// context window with cfg (ADR-010, D3): a root session has no profile, and the
// swarm coordinator's profile is not known here, so the window comes from
// cfg.ContextWindow, else the registry table. An unknown window adds a warning.
func DiagnoseSessionWith(ctx context.Context, st *Store, sessionID string, cfg CompactionConfig) (SessionDiagnosis, error) {
	sess, err := st.GetSession(ctx, sessionID)
	if err != nil {
		return SessionDiagnosis{}, err
	}
	events, err := st.Replay(ctx, sessionID)
	if err != nil {
		return SessionDiagnosis{}, fmt.Errorf("doctor replay: %w", err)
	}
	crashed := crashedFromEvents(events)
	activity := hasRunActivity(events)
	snap, err := st.BudgetSnapshot(ctx, sessionID)
	if err != nil {
		return SessionDiagnosis{}, fmt.Errorf("doctor budget snapshot: %w", err)
	}
	pending, err := st.PendingSteeringMessages(ctx, sessionID)
	if err != nil {
		return SessionDiagnosis{}, fmt.Errorf("doctor pending steering messages: %w", err)
	}
	finish := lastFinishReason(events)
	liveCrash := crashed && sess.Status == "active"
	tokens, source := ResolveContextWindow(sess.Provider, sess.ModelID, 0, cfg)
	return SessionDiagnosis{
		ContextWindow:   ContextWindow{Tokens: tokens, Source: source},
		Warnings:        windowWarnings(source),
		SessionID:       sess.ID,
		Status:          sess.Status,
		Crashed:         liveCrash,
		FinishReason:    finish,
		Truncated:       FinishTruncated(finish) && !liveCrash && truncationStatus(sess.Status),
		RunActivity:     activity,
		PendingMessages: len(pending),
		Reserved:        snap.Reserved,
		Pool:            snap.Pool,
		Available:       snap.Available,
		Usage:           summariseUsage(events),
		Compactions:     summariseCompactions(events),
	}, nil
}

// hasRunActivity reports whether the events hold provider.request,
// tool.call or assistant.delta events — the same run-activity
// evidence the crash predicate applies to tell a crashed run from a
// freshly created session.
func hasRunActivity(events []RecordedEvent) bool {
	for _, e := range events {
		if e.Type == "provider.request" || e.Type == "tool.call" || e.Type == TypeAssistantDelta {
			return true
		}
	}
	return false
}

// FinishTruncated reports whether a turn's finish reason says the
// answer was cut off at the token limit (provider.FinishLength).
func FinishTruncated(reason string) bool {
	return reason == provider.FinishLength
}

// truncationStatus reports whether a row status may carry a truncated
// final turn: failed, cancelled and paused sessions never do.
func truncationStatus(status string) bool {
	return status == "active" || status == "completed"
}

// lastFinishReason returns the finish_reason of the LAST turn.end
// event, "" when there is none, the reason is absent, or the payload
// is not a JSON object with a string reason — the doctor is
// read-only and must not fail on old or odd logs.
func lastFinishReason(events []RecordedEvent) string {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == "turn.end" {
			return payloadFieldText(events[i].PayloadJSON, "finish_reason")
		}
	}
	return ""
}

// windowWarnings returns the doctor warnings for a window source: only an
// unknown window warns, and the text names the fix.
func windowWarnings(source string) []string {
	if source != WindowSourceUnknown {
		return nil
	}
	return []string{"no context window is known for this model; set compaction.context_window in the config " +
		"(automatic compaction will need it)"}
}
