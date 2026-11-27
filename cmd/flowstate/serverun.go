package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/server"
)

// serveEventsAfter wires the snapshot/stream cursor onto
// app.SessionEventsAfter, whose existence check keeps unknown ids
// on the shared session_not_found mapping.
func serveEventsAfter(ctx context.Context, st *app.Store, id string, afterSeq int64) ([]server.Event, error) {
	events, err := app.SessionEventsAfter(ctx, st, id, afterSeq)
	if err != nil {
		return nil, err
	}
	out := make([]server.Event, len(events))
	for i, e := range events {
		out[i] = server.Event{SessionID: e.SessionID, Seq: e.Seq, CreatedAt: e.CreatedAt, Actor: e.Actor,
			Type: e.Type, Payload: json.RawMessage(e.PayloadJSON), PayloadSHA: e.PayloadSHA}
	}
	return out, nil
}

// serveSessionStatus wires the stream loop's terminal probe onto the
// store session read; an unknown id keeps session_not_found.
func serveSessionStatus(ctx context.Context, st *app.Store, id string) (string, error) {
	sess, err := st.GetSession(ctx, id)
	if err != nil {
		return "", err
	}
	return sess.Status, nil
}

// serveRun wires POST /sessions/{id}/run onto the `session run <id>`
// machinery: the identical preamble (load-runnable, run lease, crash
// recovery, pause resume, steering drain, budget resolve), turn loop and replay
// projection; the CLI's exit classes ride the shared code table.
func serveRun(ctx context.Context, st *app.Store, fl *flags, id string, opts server.RunOptions) ([]server.Event, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: session id required", app.ErrUsage)
	}
	if opts.Budget < 0 || opts.MaxTokens < 0 {
		return nil, fmt.Errorf("%w: budget and max-tokens must not be negative", app.ErrUsage)
	}
	runFl := *fl
	runFl.budget, runFl.budgetSet, runFl.maxTokens = opts.Budget, opts.Budget > 0, opts.MaxTokens
	runFl.maxTurns = opts.MaxTurns
	applyRunDefaults(&runFl)
	sess, err := loadRunnableSession(ctx, st, id)
	if err != nil {
		return nil, err
	}
	lease, sess, err := app.AcquireSessionRunLease(ctx, st, sess)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lease.Release(context.WithoutCancel(ctx)) }()
	if rerr := app.RecoverCrashedSession(ctx, st, id); rerr != nil {
		return nil, recoveryFailure(rerr)
	}
	hk, herr := app.BeginRootRunHooks(ctx, st, sess, rootHooksRequest(&runFl, lease))
	if herr != nil {
		return nil, herr
	}
	if sess.Status == "paused" {
		if rerr := app.ResumePausedSession(ctx, st, sess); rerr != nil {
			return nil, rerr
		}
	}
	if _, derr := app.DrainSteeringMessages(ctx, st, sess.ID); derr != nil {
		return nil, fmt.Errorf("%w: steering drain: %v", app.ErrConfigInvalid, derr)
	}
	budget, berr := app.ResolveRunBudget(ctx, st, sess, runFl.budget)
	if berr != nil {
		return nil, berr
	}
	runFl.budget = budget
	return serveRunTurn(ctx, st, sess, lease, hk, &runFl)
}

// serveRunTurn mirrors runSessionTurn for the transport: provider
// resolution, one bounded turn, exit-class mapping and replay
// projection; a client disconnect surfaces as session_cancelled (the
// request context plays the signal's role; the engine records
// client_disconnected or shutdown).
func serveRunTurn(ctx context.Context, st *app.Store, sess *app.Session, lease app.ExecLease, hk *app.RootRunHooks, runFl *flags) ([]server.Event, error) {
	p, perr := providerResolver(sess.Provider)
	if perr != nil {
		return nil, providerFailure(perr)
	}
	s, ok := p.(app.Streamer)
	if !ok {
		return nil, providerFailure(fmt.Errorf("provider %s does not support streaming turns", sess.Provider))
	}
	res, terr := hk.Around(ctx, func() (*app.TurnResult, error) { return runTurnOrLoop(ctx, st, sess, s, runFl, lease, hk) })
	if terr == nil && res != nil && res.BudgetExceeded {
		return nil, fmt.Errorf("%w: budget of %d exceeded after %d tokens; session paused",
			app.ErrBudgetExceeded, runFl.budget, res.ConsumedBudget)
	}
	if terr != nil {
		// The engine already wraps provider failures in app.ErrProvider;
		// every other error keeps the class app.ErrorCode gives it.
		return nil, terr
	}
	evts, rerr := st.Replay(context.WithoutCancel(ctx), sess.ID)
	if rerr != nil {
		return nil, rerr
	}
	out := make([]server.Event, len(evts))
	for i, e := range evts {
		out[i] = server.Event{SessionID: e.SessionID, Seq: e.Seq, CreatedAt: e.CreatedAt, Actor: e.Actor,
			Type: e.Type, Payload: json.RawMessage(e.PayloadJSON), PayloadSHA: e.PayloadSHA}
	}
	return out, nil
}

// serveCreate wires POST /sessions onto the `session new` composition:
// body fields override the serve config's defaults as CLI flags do,
// the composition hash pins the serve invocation's db/config source
// and the default harness; child creation (--parent) stays CLI-only.
func serveCreate(ctx context.Context, st *app.Store, fl *flags, req server.CreateRequest) (server.Session, error) {
	cfl := *fl
	cfl.provider, cfl.model, cfl.task = req.Provider, req.Model, req.Task
	provider, model := effectiveProvider(&cfl), effectiveModel(&cfl)
	if provider == "" || model == "" {
		return server.Session{}, fmt.Errorf("%w: provider and model are required", app.ErrUsage)
	}
	if strings.TrimSpace(req.Task) == "" {
		return server.Session{}, fmt.Errorf("%w: task must not be empty", app.ErrUsage)
	}
	sha, cerr := compositionSHA256(provider, model, cfl.db, cfl.configSource, effectiveAllowlist(&cfl), effectiveSkills(&cfl))
	if cerr != nil {
		return server.Session{}, cerr
	}
	sess, err := app.CreateSession(ctx, st, provider, model, app.DefaultHarness, sha, req.Task)
	if err != nil {
		return server.Session{}, err
	}
	return serveSession(*sess), nil
}
