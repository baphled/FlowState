package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

// execDefaultMaxTurns bounds one CLI-driven loop run when no explicit
// turn limit is supplied. It matches an agent profile's default
// max_turns, because reaching the limit while the model still asks for
// tools fails the run with max_turns.
const execDefaultMaxTurns int64 = 25

// MaxTurnsLimit is the largest turn limit a run accepts from
// `session run --max-turns` or the run body's max_turns.
const MaxTurnsLimit int64 = 1000

// execLeaseAgent is the lease owner recorded for CLI-driven loop runs.
const execLeaseAgent = "cli"

// RunSessionExecLoop wires one CLI-driven provider-driven execution
// loop for sess: it composes the session builtin registry rooted at
// the session workdir, gates tools through the allowlist and runs
// RunExecLoop under a per-session lease: the one it acquires itself, or the caller-held
// req.Lease, which must be the handle of "exec:"+sess.ID and which the loop only fences. The
// caller releases a forwarded handle after this function has returned, that is after the
// post-loop writers below. On success it appends the
// terminal turn.end event and marks the session completed; on budget
// exhaustion it pauses the session; on interruption it cancels the
// session and returns ErrSessionCancelled. Provider failures keep the
// loop's own failed-status recording.
func RunSessionExecLoop(ctx context.Context, st *store.Store, dbPath string, sess *store.Session, streamer provider.Streamer, req SessionExecLoopRequest) (*ExecLoopResult, error) {
	if err := checkSessionLease(sess, req.Lease); err != nil {
		return nil, err
	}
	allowlist, err := loadToolAllowlist(ToolRequest{Allow: req.Allow, AllowFile: req.AllowFile})
	if err != nil {
		return nil, err
	}
	workdir := req.Workdir
	if workdir == "" {
		workdir = sessionWorkdir(dbPath, sess.ID)
	}
	profiles, err := runProfiles(req.Agents, projectRoot(req), allowlist)
	if err != nil {
		return nil, err
	}
	if req.RequireAllowlist && len(allowlist) == 0 {
		return nil, fmt.Errorf("%w: the run has no tool allowlist; provide one with --allow, --allow-file or "+
			"tool_allowlist in the config", session.ErrUsage)
	}
	if err := ensureDefaultWorkdir(req, workdir); err != nil {
		return nil, err
	}
	registry, err := NewSessionToolRegistry(workdir)
	if err != nil {
		return nil, err
	}
	cfg := ExecLoopConfig{
		Store:      st,
		Session:    sess,
		Streamer:   streamer,
		Registry:   registry,
		Allowlist:  allowlist,
		LeaseItem:  "exec:" + sess.ID,
		LeaseAgent: execLeaseAgent,
		MaxTurns:   sessionMaxTurns(req),
		MaxTokens:  req.MaxTokens,
		Compaction: req.Compaction,
		Lease:      req.Lease,
	}
	if err := bindRunBudget(ctx, st, sess, req.Budget, &cfg); err != nil {
		return nil, err
	}
	if req.Agents != "" {
		err = registry.Register(NewDelegateTool(DelegateToolConfig{
			Store: st, Parent: sess, Streamer: streamer, Pool: cfg.Pool, Workdir: workdir, Profiles: profiles,
			Compaction: req.Compaction,
		}))
		if err != nil {
			return nil, err
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

// projectRoot is where project profiles are found: --workdir when given,
// otherwise ProjectRoot, where empty is the process current directory (what
// LoadProfiles documents). It is never the per-session default workdir.
func projectRoot(req SessionExecLoopRequest) string {
	if req.Workdir != "" {
		return req.Workdir
	}
	return req.ProjectRoot
}

// ensureDefaultWorkdir creates the default per-session workdir for a run that
// delegates: the child's workdir is confined to the parent's, which must exist.
// An explicit --workdir is the caller's to provide.
func ensureDefaultWorkdir(req SessionExecLoopRequest, workdir string) error {
	if req.Agents == "" || req.Workdir != "" {
		return nil
	}
	if err := os.MkdirAll(workdir, 0o700); err != nil {
		return fmt.Errorf("create the default working directory: %w", err)
	}
	return nil
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

// runProfiles loads the agent profiles a run offers and checks the allowlist
// agrees with the scope: `delegate` and --agents come together or not at all.
// No scope means no delegation and no profiles. A scope that fails to load is a
// usage error (bad value) or config_invalid (a profile that fails validation or
// a name in both directories), before anything has run.
func runProfiles(scope, workdir string, allowlist []string) ([]agent.Profile, error) {
	allowed := slices.Contains(allowlist, agent.DelegateTool)
	switch {
	case scope == "" && allowed:
		return nil, fmt.Errorf("%w: delegate needs --agents", session.ErrUsage)
	case scope != "" && !allowed:
		return nil, fmt.Errorf("%w: --agents loads profiles but delegate is not allowlisted", session.ErrUsage)
	case scope == "":
		return nil, nil
	}
	return LoadProfiles(scope, workdir)
}

// bindRunBudget applies the run's budget to cfg: an explicit budget anchors the
// session's shared pool, so children may reserve against it (M2 hierarchical
// budgets); without one a delegated child draws from its reservation, the grant
// minus what it has spent.
func bindRunBudget(ctx context.Context, st *store.Store, sess *store.Session, budget int64, cfg *ExecLoopConfig) error {
	if budget > 0 {
		if err := st.SetBudgetPool(ctx, sess.ID, budget); err != nil {
			return err
		}
		cfg.Pool, cfg.TurnBudget = NewBudgetPool(budget), 1
		return nil
	}
	if res, rerr := st.ReservationFor(ctx, sess.ID); rerr == nil && res.Status == store.ReservationStatusReserved {
		cfg.Pool, cfg.TurnBudget = NewBudgetPool(max(res.Granted-res.Consumed, 0)), 1
	}
	return nil
}

// checkSessionLease refuses a forwarded handle that is not the one of sess's exec item, a mix-up
// of handles; no handle is fine, the loop then takes its own lease.
func checkSessionLease(sess *store.Session, lease ExecLease) error {
	if lease != nil && lease.Item() != "exec:"+sess.ID {
		return fmt.Errorf("%w: lease handle is for %q, not session %s", ErrExecLoopConfig, lease.Item(), sess.ID)
	}
	return nil
}

// sessionMaxTurns is the run's turn limit: req.MaxTurns, or execDefaultMaxTurns when it is not positive.
func sessionMaxTurns(req SessionExecLoopRequest) int64 {
	if req.MaxTurns <= 0 {
		return execDefaultMaxTurns
	}
	return req.MaxTurns
}
