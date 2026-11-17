// Package app — session_resume.go owns the explicit paused-session
// resume step of the session run lifecycle (CLI-AND-API-CONTRACT §3):
// a rerun of a paused session resumes it durably before any further
// bounded turn work.
package app

import (
	"context"
	"fmt"

	"github.com/baphled/flowstate/internal/store"
)

// ResumePausedSession explicitly resumes a paused session before any
// further run work: it acquires the session's single event writer
// through the event-log port (mirroring how the pause path consumes
// it) and calls the writer's atomic Resume, which appends the
// correlated session.resumed event (actor user, from_status paused)
// and restores sessions.status='active' in one transaction. A session
// whose status is not paused is left untouched and no event is
// appended, so ordinary reruns never emit session.resumed. On success
// the passed session's Status field is refreshed in place to active.
// A resume failure is returned with the session left unresumed; the
// caller must block turn execution and surface the failure rather
// than treat the session as acknowledged active.
func ResumePausedSession(ctx context.Context, st *store.Store, sess *store.Session) error {
	if sess == nil {
		return fmt.Errorf("app: session must not be nil")
	}
	if sess.Status != "paused" {
		return nil
	}
	w, err := st.NewEventWriter(ctx, sess.ID)
	if err != nil {
		return fmt.Errorf("resume writer: %w", err)
	}
	defer w.Close()
	if err := w.Resume(ctx); err != nil {
		return fmt.Errorf("resume session %s: %w", sess.ID, err)
	}
	sess.Status = "active"
	return nil
}
