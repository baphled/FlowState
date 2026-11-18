// Package app — session_recovery.go owns the crash-recovery detection
// step of the session run lifecycle: a rerun of an active session whose
// last turn was left incomplete (turn.start without a matching
// turn.end) is detected as a crash-recovery scenario and the session
// is marked with a correlated session.interrupted event before any
// further bounded turn work.
package app

import (
	"context"
	"fmt"

	"github.com/baphled/flowstate/internal/store"
)

// DetectCrashedSession reports whether the session's event log ends
// with a dangling turn.start (a turn that was started but never
// completed with turn.end, session.cancelled, session.paused or
// session.resumed). This is the crash-recovery predicate: true means
// the process died mid-turn without emitting a terminal event.
func DetectCrashedSession(ctx context.Context, st *store.Store, sessionID string) (bool, error) {
	events, err := st.Replay(ctx, sessionID)
	if err != nil {
		return false, fmt.Errorf("crash detection replay: %w", err)
	}
	// A freshly created session has only the initial turn.start from
	// session.Create — it has not been run yet. Crash recovery requires
	// evidence that a run was actually in progress: at least one
	// provider request or tool call event.
	hasRunActivity := false
	for _, e := range events {
		if e.Type == "provider.request" || e.Type == "tool.call" {
			hasRunActivity = true
		}
	}
	if !hasRunActivity {
		return false, nil
	}
	for i := len(events) - 1; i >= 0; i-- {
		switch events[i].Type {
		case "turn.start":
			return true, nil
		case "turn.end", "session.cancelled", "session.paused", "session.interrupted":
			return false, nil
		}
	}
	return false, nil
}

// RecoverCrashedSession appends a session.interrupted event (actor
// engine, reason process_lost) to mark the detected crash before the
// resumed run proceeds. Uses the store's event writer in the same
// atomic pattern as ResumePausedSession.
func RecoverCrashedSession(ctx context.Context, st *store.Store, sessionID string) error {
	crashed, derr := DetectCrashedSession(ctx, st, sessionID)
	if derr != nil {
		return fmt.Errorf("crash recovery detection: %w", derr)
	}
	if !crashed {
		return nil
	}
	w, err := st.NewEventWriter(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("crash recovery writer: %w", err)
	}
	defer w.Close()
	if _, aerr := w.Append(ctx, "engine", "session.interrupted", map[string]any{
		"reason": "process_lost",
	}); aerr != nil {
		return fmt.Errorf("crash recovery append: %w", aerr)
	}
	return nil
}
