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
	sess, err := st.GetSession(ctx, sessionID)
	if err != nil {
		return SessionDiagnosis{}, err
	}
	crashed, err := DetectCrashedSession(ctx, st, sessionID)
	if err != nil {
		return SessionDiagnosis{}, err
	}
	activity, err := sessionRunActivity(ctx, st, sessionID)
	if err != nil {
		return SessionDiagnosis{}, err
	}
	snap, err := st.BudgetSnapshot(ctx, sessionID)
	if err != nil {
		return SessionDiagnosis{}, fmt.Errorf("doctor budget snapshot: %w", err)
	}
	pending, err := st.PendingSteeringMessages(ctx, sessionID)
	if err != nil {
		return SessionDiagnosis{}, fmt.Errorf("doctor pending steering messages: %w", err)
	}
	finish, err := lastFinishReason(ctx, st, sessionID)
	if err != nil {
		return SessionDiagnosis{}, err
	}
	liveCrash := crashed && sess.Status == "active"
	return SessionDiagnosis{
		SessionID:       sess.ID,
		Status:          sess.Status,
		Crashed:         liveCrash,
		FinishReason:    finish,
		Truncated:       finish == provider.FinishLength && !liveCrash && truncationStatus(sess.Status),
		RunActivity:     activity,
		PendingMessages: len(pending),
		Reserved:        snap.Reserved,
		Pool:            snap.Pool,
		Available:       snap.Available,
	}, nil
}

// sessionRunActivity reports whether the session's event log holds
// provider.request, tool.call or assistant.delta events — the same
// run-activity evidence DetectCrashedSession applies to tell a
// crashed run from a freshly created session.
func sessionRunActivity(ctx context.Context, st *Store, sessionID string) (bool, error) {
	events, err := st.Replay(ctx, sessionID)
	if err != nil {
		return false, fmt.Errorf("run-activity replay: %w", err)
	}
	for _, e := range events {
		if e.Type == "provider.request" || e.Type == "tool.call" || e.Type == TypeAssistantDelta {
			return true, nil
		}
	}
	return false, nil
}

// truncationStatus reports whether a row status may carry a truncated
// final turn: failed, cancelled and paused sessions never do.
func truncationStatus(status string) bool {
	return status == "active" || status == "completed"
}

// lastFinishReason returns the finish_reason of the session's LAST
// turn.end event, "" when there is none, the reason is absent, or the
// payload is not a JSON object with a string reason — the doctor is
// read-only and must not fail on old or odd logs.
func lastFinishReason(ctx context.Context, st *Store, sessionID string) (string, error) {
	events, err := st.Replay(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("finish-reason replay: %w", err)
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == "turn.end" {
			return payloadFieldText(events[i].PayloadJSON, "finish_reason"), nil
		}
	}
	return "", nil
}
