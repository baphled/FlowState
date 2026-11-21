package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

// execDefaultMaxTurns bounds one CLI-driven loop run when no explicit
// turn limit is supplied.
const execDefaultMaxTurns int64 = 8

// execLeaseAgent is the lease owner recorded for CLI-driven loop runs.
const execLeaseAgent = "cli"

// RunSessionExecLoop wires one CLI-driven provider-driven execution
// loop for sess: it composes the session builtin registry rooted at
// the session workdir, gates tools through the allowlist and runs
// RunExecLoop under a per-session lease. On success it appends the
// terminal turn.end event and marks the session completed; on budget
// exhaustion it pauses the session; on interruption it cancels the
// session and returns ErrSessionCancelled. Provider failures keep the
// loop's own failed-status recording.
func RunSessionExecLoop(ctx context.Context, st *store.Store, dbPath string, sess *store.Session, streamer provider.Streamer, req SessionExecLoopRequest) (*ExecLoopResult, error) {
	allowlist, err := loadToolAllowlist(ToolRequest{Allow: req.Allow, AllowFile: req.AllowFile})
	if err != nil {
		return nil, err
	}
	workdir := req.Workdir
	if workdir == "" {
		workdir = sessionWorkdir(dbPath, sess.ID)
	}
	registry, err := NewSessionToolRegistry(workdir)
	if err != nil {
		return nil, err
	}
	maxTurns := req.MaxTurns
	if maxTurns <= 0 {
		maxTurns = execDefaultMaxTurns
	}
	cfg := ExecLoopConfig{
		Store:      st,
		Session:    sess,
		Streamer:   streamer,
		Registry:   registry,
		Allowlist:  allowlist,
		LeaseItem:  "exec:" + sess.ID,
		LeaseAgent: execLeaseAgent,
		MaxTurns:   maxTurns,
		MaxTokens:  req.MaxTokens,
	}
	switch {
	case req.Budget > 0:
		// An explicit budget anchors the session's shared pool: child
		// sessions may reserve against it (M2 hierarchical budgets).
		if serr := st.SetBudgetPool(ctx, sess.ID, req.Budget); serr != nil {
			return nil, serr
		}
		cfg.Pool = NewBudgetPool(req.Budget)
		cfg.TurnBudget = 1
	default:
		// Without an explicit budget a delegated child draws from its
		// reservation: the pool is the grant minus what it has spent.
		if res, rerr := st.ReservationFor(ctx, sess.ID); rerr == nil && res.Status == store.ReservationStatusReserved {
			grant := res.Granted - res.Consumed
			if grant < 0 {
				grant = 0
			}
			cfg.Pool = NewBudgetPool(grant)
			cfg.TurnBudget = 1
		}
	}
	res, err := RunExecLoop(ctx, cfg)
	if err != nil {
		return res, finishExecLoopState(ctx, st, sess, err)
	}
	if err := completeExecLoopRun(ctx, st, sess.ID, res.FinishReason); err != nil {
		return res, err
	}
	return res, nil
}

// sessionWorkdir defaults the per-session tool workdir beside the db.
func sessionWorkdir(dbPath, sessionID string) string {
	return filepath.Join(SessionWorkdirRoot(dbPath), sessionID)
}

// finishExecLoopState records the contract terminal state for a
// stopped loop run and returns the error the CLI should observe.
func finishExecLoopState(ctx context.Context, st *store.Store, sess *store.Session, err error) error {
	switch {
	case errors.Is(err, ErrExecLoopBudget):
		if perr := pauseExecLoopRun(ctx, st, sess.ID); perr != nil {
			return fmt.Errorf("%w: %w", err, perr)
		}
		return fmt.Errorf("%w: loop budget exhausted", ErrBudgetExceeded)
	case errors.Is(err, context.Canceled), ctx.Err() != nil:
		if _, cerr := CancelSession(context.WithoutCancel(ctx), st, sess.ID); cerr != nil {
			return fmt.Errorf("%w: %w", ErrSessionCancelled, cerr)
		}
		return ErrSessionCancelled
	default:
		return err
	}
}

// pauseExecLoopRun pauses the session after a budget stop so a later
// `session run` explicitly resumes (CLI-AND-API-CONTRACT §3).
func pauseExecLoopRun(ctx context.Context, st *store.Store, sessionID string) error {
	w, err := st.NewEventWriter(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("loop pause writer: %w", err)
	}
	defer func() { _ = w.Close() }()
	return w.Pause(ctx, store.PauseInfo{Reason: "budget_exhausted", NextSafeOp: "session run " + sessionID})
}

// completeExecLoopRun appends the terminal turn.end event and marks
// the session completed exactly once after a successful loop run;
// finishReason is the final provider turn's reason, empty if none.
func completeExecLoopRun(ctx context.Context, st *store.Store, sessionID, finishReason string) error {
	w, err := st.NewEventWriter(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("loop completion writer: %w", err)
	}
	defer func() { _ = w.Close() }()
	if _, err := w.Append(ctx, session.ActorEngine, TypeTurnEnd, turnEndPayload(finishReason)); err != nil {
		return fmt.Errorf("record loop completion: %w", err)
	}
	settleReservation(ctx, st, sessionID)
	if err := st.UpdateSessionStatus(ctx, sessionID, "completed"); err != nil {
		return fmt.Errorf("mark session completed: %w", err)
	}
	return nil
}
