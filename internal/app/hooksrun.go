package app

// The run-level hook points of ADR-012 packet 4a: user.prompt, session.start and session.end, and the
// hooks.loaded record they follow. They belong to a root run on both of its paths, so the callers of the
// preamble (session run, serve) drive them; the loop's own tool points live in hooksgate.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/baphled/flowstate/internal/store"
)

// textOnlyInert are the points that exist only in the exec loop.
var textOnlyInert = []HookPoint{HookToolBefore, HookToolAfter, HookTurnAfter, HookCompactionBefore, HookCompactionAfter}

// BeginRootRunHooks resolves the hook set of a root run, records hooks.loaded and fires user.prompt with the
// run's stored task. Call it after the run lease and crash recovery and before the resume writes: a refusal
// returns ErrHookRefused naming the hook, and the log then holds only hooks.loaded, hook.invoked and
// hook.result for this run, so a paused session stays paused. The returned value is also owed Around.
func BeginRootRunHooks(ctx context.Context, st *store.Store, sess *store.Session, req RootRunHooksRequest) (*RootRunHooks, error) {
	set, loaded, err := loadRunHooks(nil, req.Scope, req.ProjectRoot)
	if err != nil {
		return nil, err
	}
	h := &RootRunHooks{st: st, session: sess.ID, set: set, input: hookInput{
		session: sess.ID, cwd: req.Workdir, runID: strconv.FormatInt(req.Lease.Token(), 10)}}
	if loaded {
		if err := recordHooksLoaded(ctx, st, sess.ID, set, inertHooks(set, req.TextOnly)); err != nil {
			return nil, err
		}
	}
	events, err := st.Replay(ctx, sess.ID)
	if err != nil {
		return nil, fmt.Errorf("user.prompt: load session log: %w", err)
	}
	id, err := h.fire(ctx, HookUserPrompt, map[string]any{"prompt": taskFromEvents(events)})
	if err != nil {
		return nil, err
	}
	if id != "" {
		return nil, fmt.Errorf("%w: the user.prompt hook %s refused the run", ErrHookRefused, id)
	}
	return h, nil
}

// Set is the loaded hook set the run carries into its exec loop, so the loop does not load or record it again.
func (h *RootRunHooks) Set() HookSet {
	if h == nil {
		return nil
	}
	return h.set
}

// Around fires session.start, runs the turn or the loop and fires session.end once the run's terminal or
// paused state is written. A run whose session.start failed to record never ends; an error of session.end
// is reported only when the run itself succeeded, so it never hides the run's own classification.
func (h *RootRunHooks) Around(ctx context.Context, run func() (*TurnResult, error)) (*TurnResult, error) {
	if h == nil {
		return run()
	}
	h.started = true
	if _, err := h.fire(ctx, HookSessionStart, nil); err != nil {
		return nil, err
	}
	res, err := run()
	if _, eerr := h.fire(context.WithoutCancel(ctx), HookSessionEnd, nil); err == nil {
		err = eerr
	}
	return res, err
}

// fire runs the hooks of one run-level point in order and returns the id of the hook that refused, if any.
// Only user.prompt can refuse: a deny or a failing fail-closed hook at another point is recorded and the
// chain goes on. Its events go through a writer held for the point alone.
func (h *RootRunHooks) fire(ctx context.Context, point HookPoint, extra map[string]any) (string, error) {
	var hooks []Hook
	for _, hk := range hookSetHooks(h.set) {
		if hk.Point == point {
			hooks = append(hooks, hk)
		}
	}
	if len(hooks) == 0 {
		return "", nil
	}
	w, err := h.st.NewEventWriter(ctx, h.session)
	if err != nil {
		return "", fmt.Errorf("%s writer: %w", point, err)
	}
	defer func() { _ = w.Close() }()
	g := &hookGate{hooks: hooks, events: w, input: h.input}
	stdin, ierr := g.pointDocument(point, extra)
	for _, hk := range hooks {
		step, err := g.invoke(ctx, hk, stdin, ierr, nil)
		if err != nil {
			return "", err
		}
		if step.deny && point == HookUserPrompt {
			return hk.ID, nil
		}
	}
	return "", nil
}

// inertHooks lists the ids of the hooks whose point does not exist on the run's path.
func inertHooks(set HookSet, textOnly bool) []string {
	var ids []string
	for _, hk := range hookSetHooks(set) {
		if textOnly && slices.Contains(textOnlyInert, hk.Point) {
			ids = append(ids, hk.ID)
		}
	}
	return ids
}

// pointDocument is the JSON a run-level hook reads on stdin: the common fields of ADR-012, Handler kinds,
// plus extra (the run's prompt at user.prompt). A root run has agent "" and depth 0.
func (g *hookGate) pointDocument(point HookPoint, extra map[string]any) ([]byte, error) {
	doc := map[string]any{
		"version": 1, "point": string(point), "session_id": g.input.session, "run_id": g.input.runID,
		"agent": g.input.agent, "depth": g.input.depth, "cwd": g.input.cwd,
	}
	for k, v := range extra {
		doc[k] = v
	}
	return json.Marshal(doc)
}
