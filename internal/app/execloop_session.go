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
	"github.com/baphled/flowstate/internal/tool"
)

// execDefaultMaxTurns bounds one CLI-driven loop run when no explicit
// turn limit is supplied. It matches an agent profile's default
// max_turns, because reaching the limit while the model still asks for
// tools fails the run with max_turns.
const execDefaultMaxTurns int64 = 25

// MaxTurnsLimit is the largest turn limit a run accepts from
// `session run --max-turns` or the run body's max_turns.
const MaxTurnsLimit int64 = 1000

// RunSessionExecLoop wires one CLI-driven provider-driven execution
// loop for sess: it composes the session builtin registry rooted at
// the session workdir, gates tools through the allowlist and runs
// RunExecLoop under the caller-held req.Lease, which is required, must be the handle of
// "exec:"+sess.ID and which the loop only fences. The caller releases the handle after this
// function has returned, that is after the post-loop writers below. On success it appends the
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
	// The protected trees are computed once per run from the inputs the hook loader reads (the project
	// root, never Workdir), and handed to the root's registry and to its delegate tool, so every child's file
	// tools refuse the same hook directories (ADR-012 D20).
	protected := ProtectedHookDirs(HooksProjectRoot(req))
	registry, err := sessionRegistry(workdir, req, protected)
	if err != nil {
		return nil, err
	}
	hooks, err := bindRunHooks(ctx, st, sess.ID, req)
	if err != nil {
		return nil, err
	}
	cfg := ExecLoopConfig{
		Store:      st,
		Session:    sess,
		Streamer:   streamer,
		Registry:   registry,
		Allowlist:  allowlist,
		MaxTurns:   sessionMaxTurns(req),
		MaxTokens:  req.MaxTokens,
		Compaction: req.Compaction,
		Lease:      req.Lease,
		Hooks:      hooks,
		Workdir:    workdir,
	}
	if err := bindRunBudget(ctx, st, sess, req.Budget, &cfg); err != nil {
		return nil, err
	}
	if err := registerSessionDelegate(st, sess, streamer, &cfg, workdir, profiles, req, protected); err != nil {
		return nil, err
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

// sessionRegistry composes the root's registry with the protected hook directories and the skills the request offers,
// so a run with none never has skill.load; an invalid scope is config_invalid before any event is written. Children are
// offered none by this (ADR-013 D12): their registry is built apart, with the protection only.
func sessionRegistry(workdir string, req SessionExecLoopRequest, protected []string) (*tool.Registry, error) {
	offered, err := offeredSkills(req)
	if err != nil {
		return nil, err
	}
	return NewSessionToolRegistry(workdir, tool.WithProtectedDirs(protected...), tool.WithOfferedSkills(offered))
}

// registerSessionDelegate gives a run that offers agents its delegate tool, which hands the run's hook set and
// its protected hook directories to every child.
func registerSessionDelegate(st *store.Store, sess *store.Session, streamer provider.Streamer, cfg *ExecLoopConfig,
	workdir string, profiles []agent.Profile, req SessionExecLoopRequest, protected []string) error {
	if req.Agents == "" {
		return nil
	}
	return cfg.Registry.Register(NewDelegateTool(DelegateToolConfig{
		Store: st, Parent: sess, Streamer: streamer, Pool: cfg.Pool, Workdir: workdir, Profiles: profiles,
		Compaction: req.Compaction, Hooks: cfg.Hooks, ProtectedDirs: protected,
	}))
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
	case errors.Is(err, ErrHookStop):
		if perr := pauseExecLoopRunFor(ctx, st, sess.ID, "hook_stop"); perr != nil {
			return fmt.Errorf("%w: %w", err, perr)
		}
		return err
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
	return pauseExecLoopRunFor(ctx, st, sessionID, "budget_exhausted")
}

// pauseExecLoopRunFor pauses the session with reason in session.paused (budget_exhausted or hook_stop).
func pauseExecLoopRunFor(ctx context.Context, st *store.Store, sessionID, reason string) error {
	w, err := st.NewEventWriter(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("loop pause writer: %w", err)
	}
	defer func() { _ = w.Close() }()
	return w.Pause(ctx, store.PauseInfo{Reason: reason, NextSafeOp: "session run " + sessionID})
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

// checkSessionLease refuses a missing handle, and a handle that is not the one of sess's exec item,
// a mix-up of handles, before anything is written.
func checkSessionLease(sess *store.Session, lease ExecLease) error {
	if lease == nil {
		return fmt.Errorf("%w: a held lease handle is required", ErrExecLoopConfig)
	}
	if lease.Item() != "exec:"+sess.ID {
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

// HooksProjectRoot is the directory the project hook file is found under for a
// loop run: the request's ProjectRoot, where empty is the process current
// directory. It is deliberately not projectRoot: that returns Workdir when given,
// and ADR-012 resolves hooks against the project root, never the per-run workdir.
func HooksProjectRoot(req SessionExecLoopRequest) string {
	return req.ProjectRoot
}
