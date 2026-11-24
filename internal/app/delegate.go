package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// RunDelegatedChild runs one profile-driven child of req.Parent through the
// existing execution loop and settles it (ADR-009). Order: the parent, depth,
// profile tools and working directory are checked first, with nothing written;
// the grant is then reserved BEFORE the child exists, so a refusal leaves no
// orphan session; the child runs with exactly the profile's tools, prompt and
// turn cap; whatever the outcome, the consumed turns are charged, the
// reservation is settled, the unspent grant returns to req.Pool and a
// delegation.completed receipt lands in the parent's log.
//
// A failed or cancelled child, and a refused budget, come back as a result with
// a nil error so the caller can show them to the model. A refused request (a
// terminal parent, depth, `delegate` among the tools, an unknown tool, a
// working directory outside the parent's) and an infrastructure failure are
// errors. A child that reaches MaxTurns while the model still asks for tools
// completes today (the loop is unchanged; ADR-009 D13 is a later packet).
// The child runs under ctx: a cancelled ctx cancels it with ctx's reason, and
// the settlement runs on a detached context.
func RunDelegatedChild(ctx context.Context, req DelegateRequest) (DelegateResult, error) {
	registry, modelID, err := planDelegation(ctx, &req)
	if err != nil {
		return DelegateResult{}, err
	}
	childID, err := session.NewID()
	if err != nil {
		return DelegateResult{}, fmt.Errorf("generate session id: %w", err)
	}
	grant := int64(req.Profile.MaxTurns)
	if out, rerr := reserveDelegateBudget(ctx, &req, childID, grant); rerr != nil || out.Status != "" {
		return out, rerr
	}
	child, err := openChildSession(context.WithoutCancel(ctx), req.Store, childSpec{
		id: childID, parentID: req.Parent.ID, provider: req.Parent.Provider, modelID: modelID,
		harness: req.Parent.Harness, configSHA: req.Parent.ConfigSHA, task: req.Task,
		profile: profileRef(req.Profile),
	})
	if err != nil {
		unwindDelegateBudget(ctx, &req, childID, grant)
		return DelegateResult{}, fmt.Errorf("app: delegate: create child: %w", err)
	}
	defer settleDelegatedPanic(ctx, &req, child, grant)
	res, runErr := RunExecLoop(ctx, delegateLoopConfig(&req, child, registry, grant))
	return finishDelegatedChild(ctx, &req, child, grant, res, runErr)
}

// errDelegatePanic is the failure recorded for a child whose loop panicked. The
// turns it spent are unknown, so its whole grant is charged.
var errDelegatePanic = errors.New("panic")

// settleDelegatedPanic runs deferred: if the loop panicked it fails the child with
// reason "panic", charges the whole grant, settles it on a detached context and
// re-panics with the value.
func settleDelegatedPanic(ctx context.Context, req *DelegateRequest, child *store.Session, grant int64) {
	if p := recover(); p != nil {
		_, _ = finishDelegatedChild(context.WithoutCancel(ctx), req, child, grant, nil, errDelegatePanic)
		panic(p)
	}
}

// planDelegation validates the request without writing anything and returns the
// child's registry and model.
func planDelegation(ctx context.Context, req *DelegateRequest) (*tool.Registry, string, error) {
	if err := validateDelegateRequest(req); err != nil {
		return nil, "", err
	}
	modelID := req.Parent.ModelID
	if m := req.Profile.Model; m != "" && m != agent.InheritModel {
		modelID = m // sent as ChatRequest.Model, which every provider passes through
	}
	if err := checkChildCreation(ctx, req.Store, req.Parent.ID, req.Parent.Provider, modelID, req.Task); err != nil {
		return nil, "", err
	}
	workdir, err := confineChildWorkdir(req.ParentWorkdir, req.Workdir)
	if err != nil {
		return nil, "", err
	}
	build := req.NewRegistry
	if build == nil {
		build = NewSessionToolRegistry
	}
	registry, err := build(workdir)
	if err != nil {
		return nil, "", fmt.Errorf("app: delegate: child registry: %w", err)
	}
	if _, derr := SessionToolDefinitions(registry, req.Profile.Tools); derr != nil {
		return nil, "", fmt.Errorf("%w: profile %s tools: %v", ErrUsage, req.Profile.Name, derr)
	}
	return registry, modelID, nil
}

// validateDelegateRequest rejects missing collaborators and a profile that asks
// for `delegate`, which children cannot use yet.
func validateDelegateRequest(req *DelegateRequest) error {
	switch {
	case req.Store == nil, req.Parent == nil, req.Streamer == nil:
		return fmt.Errorf("%w: delegate: store, parent and streamer are required", ErrUsage)
	case req.Profile.Name == "" || req.Profile.MaxTurns <= 0:
		return fmt.Errorf("%w: delegate: a validated profile is required", ErrUsage)
	case req.ParentWorkdir == "":
		return fmt.Errorf("%w: delegate: the parent working directory is required", ErrUsage)
	case delegatedParentTerminal[req.Parent.Status]:
		return fmt.Errorf("%w: parent session %s is %s", ErrUsage, req.Parent.ID, req.Parent.Status)
	case slices.Contains(req.Profile.Tools, agent.DelegateTool):
		return fmt.Errorf("%w: profile %s: delegate is not available to children yet", ErrUsage, req.Profile.Name)
	}
	return nil
}

// confineChildWorkdir resolves the child's directory: the parent's when child is
// empty, else child (relative to the parent's) cleaned and then resolved through
// symlinks. A path outside the parent's directory is refused, so a child never
// widens the parent's filesystem reach. The path is cleaned before anything
// touches the disk, so a `..` never makes the call stat a component that the
// cleaned path does not contain. Each refusal of the child's directory is the one
// errWorkdirRefused message and each refusal of the parent's directory is the one
// errParentWorkdirRefused message; neither carries an OS path or an OS error. The
// resolved directory is returned for the caller to use, not to show the model.
func confineChildWorkdir(parent, child string) (string, error) {
	root, err := filepath.EvalSymlinks(parent)
	if err != nil || !isDirectory(root) {
		return "", errParentWorkdirRefused()
	}
	if child == "" {
		return root, nil
	}
	target := child
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	target = filepath.Clean(target)
	if !pathWithin(root, target) && !pathWithin(filepath.Clean(parent), target) {
		return "", errWorkdirRefused()
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil || !pathWithin(root, resolved) || !isDirectory(resolved) {
		return "", errWorkdirRefused()
	}
	return resolved, nil
}

// errWorkdirRefused is the one error for every refused child working directory.
func errWorkdirRefused() error {
	return fmt.Errorf("%w: delegate: the working directory must be an existing directory inside the parent's", ErrUsage)
}

// errParentWorkdirRefused is the one error for a parent working directory that
// is missing, unreadable or not a directory.
func errParentWorkdirRefused() error {
	return fmt.Errorf("%w: delegate: the parent working directory must be an existing directory", ErrUsage)
}

// pathWithin reports whether path is root or lies below it, by name only.
func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// isDirectory reports whether path is an existing directory.
func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// profileRef is the identity of a profile as recorded in events.
func profileRef(p agent.Profile) *DelegateProfileRef {
	return &DelegateProfileRef{Name: p.Name, Path: p.Path, SHA256: p.SHA256}
}

// reserveDelegateBudget draws grant from the parent's pool and the store. A
// refused grant returns a failed result and a cancellation a cancelled one, with
// nothing held; a zero Status means the grant is held.
func reserveDelegateBudget(ctx context.Context, req *DelegateRequest, childID string, grant int64) (DelegateResult, error) {
	if req.Pool == nil {
		return DelegateResult{}, nil
	}
	failed := func(why string) DelegateResult {
		return DelegateResult{Status: DelegateStatusFailed, Reason: "budget_exhausted: " + why}
	}
	if got := req.Pool.Reserve(grant); got != grant {
		req.Pool.Release(got)
		return failed(fmt.Sprintf("the parent pool cannot cover %d turns", grant)), nil
	}
	err := ReserveChildBudget(ctx, req.Store, req.Parent.ID, childID, grant)
	if err == nil {
		return DelegateResult{}, nil
	}
	req.Pool.Release(grant)
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		return DelegateResult{Status: DelegateStatusCancelled, Reason: CancelReason(ctx)}, nil
	case errors.Is(err, store.ErrBudgetExceeded), errors.Is(err, store.ErrBudgetPoolMissing):
		return failed(err.Error()), nil
	}
	return DelegateResult{}, fmt.Errorf("app: delegate: reserve budget: %w", err)
}

// unwindDelegateBudget gives back a grant whose child could not be created.
func unwindDelegateBudget(ctx context.Context, req *DelegateRequest, childID string, grant int64) {
	if req.Pool == nil {
		return
	}
	_ = settleChildReservation(context.WithoutCancel(ctx), req.Store, childID)
	req.Pool.Release(grant)
}

// delegateLoopConfig composes the loop for the child: exactly the profile's
// tools, prompt and turn cap, drawing one unit per turn from its grant.
func delegateLoopConfig(req *DelegateRequest, child *store.Session, registry *tool.Registry, grant int64) ExecLoopConfig {
	cfg := ExecLoopConfig{
		Store: req.Store, Session: child, Streamer: req.Streamer, Registry: registry,
		Allowlist: req.Profile.Tools, LeaseItem: "exec:" + child.ID, LeaseAgent: delegateLeaseAgent,
		MaxTurns: grant, SystemPrompt: req.Profile.Body,
	}
	if req.Pool != nil {
		cfg.Pool, cfg.TurnBudget = NewBudgetPool(grant), 1
	}
	return cfg
}

// finishDelegatedChild ends the child in the status the run implies and settles
// it on a detached context: consumption charged, reservation settled, the unspent
// grant released, the receipt appended. Cancellation wins over any loop error.
func finishDelegatedChild(ctx context.Context, req *DelegateRequest, child *store.Session, grant int64,
	res *ExecLoopResult, runErr error) (DelegateResult, error) {
	reason := CancelReason(ctx) // read before detaching: WithoutCancel drops the cause
	dctx := context.WithoutCancel(ctx)
	out := DelegateResult{ChildID: child.ID}
	if res != nil {
		out.Turns, out.ToolCalls, out.FinalContent = res.Turns, res.ToolCalls, res.FinalContent
	}
	consumed := out.Turns
	if req.Pool != nil && consumed > grant || errors.Is(runErr, errDelegatePanic) {
		consumed = grant
	}
	var errs []error
	if req.Pool != nil {
		errs = append(errs, chargeDelegation(dctx, req.Store, child.ID, consumed))
	}
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		out.Status, out.Reason = DelegateStatusCancelled, reason
		errs = append(errs, cancelSwarmChild(dctx, req.Store, child.ID, reason))
	case runErr == nil:
		out.Status = DelegateStatusCompleted
		errs = append(errs, completeExecLoopRun(dctx, req.Store, child.ID, res.FinishReason))
	default:
		out.Status, out.Reason = DelegateStatusFailed, runErr.Error()
		errs = append(errs, failDelegatedChild(dctx, req.Store, child.ID, runErr))
		if !delegateFailureIsChild(ctx, runErr) {
			errs = append(errs, runErr)
		}
	}
	if req.Pool != nil {
		req.Pool.Release(grant - consumed)
	}
	out.Partial = out.Status != DelegateStatusCompleted && out.FinalContent != ""
	errs = append(errs, appendDelegationReceipt(dctx, req, out, consumed))
	return out, errors.Join(errs...)
}

// delegateFailureIsChild reports whether a loop error is the child's own failure
// (a provider failure, an exhausted budget or an expired deadline), which the
// model sees, rather than an infrastructure failure, which the caller sees. A
// deadline can expire inside a store read before the provider is reached, so an
// expired ctx counts even when the error does not wrap the deadline.
func delegateFailureIsChild(ctx context.Context, err error) bool {
	return errors.Is(err, ErrProvider) || errors.Is(err, ErrExecLoopBudget) ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)
}

// chargeDelegation records the turns the child spent against its reservation.
func chargeDelegation(ctx context.Context, st *store.Store, childID string, consumed int64) error {
	if err := st.ChargeReservation(ctx, childID, consumed); err != nil {
		return fmt.Errorf("app: delegate: charge reservation: %w", err)
	}
	return nil
}

// failDelegatedChild makes the child failed, final event first, unless the loop
// already did (a provider failure), and settles its reservation.
func failDelegatedChild(ctx context.Context, st *store.Store, id string, cause error) error {
	cur, err := st.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if cur.Status != "failed" {
		w, werr := st.NewEventWriter(ctx, id)
		if werr != nil {
			return werr
		}
		defer func() { _ = w.Close() }()
		if _, aerr := w.Append(ctx, session.ActorEngine, TypeDelegationFailed, map[string]any{"error": cause.Error()}); aerr != nil {
			return aerr
		}
		if uerr := st.UpdateSessionStatus(ctx, id, "failed"); uerr != nil {
			return uerr
		}
	}
	return settleChildReservation(ctx, st, id)
}

// appendDelegationReceipt writes the delegation.completed receipt to the
// parent's log, through req.ParentEvents when given. Otherwise it opens a writer
// on the parent, serialised in-process per parent and retried briefly while
// another holder (the parent loop) has the one writer the store allows.
func appendDelegationReceipt(ctx context.Context, req *DelegateRequest, out DelegateResult, consumed int64) error {
	payload := map[string]any{
		"child_id": out.ChildID, "profile": profileRef(req.Profile), "status": out.Status,
		"turns": out.Turns, "tool_calls": out.ToolCalls, "reason": capDelegateText(out.Reason), "consumed": consumed,
	}
	if req.ParentEvents != nil {
		return recordReceipt(ctx, req.ParentEvents, payload)
	}
	var err error
	for attempt := 0; attempt < receiptWriterAttempts; attempt++ {
		if attempt > 0 {
			t := time.NewTimer(receiptWriterBackoff)
			select {
			case <-ctx.Done():
				t.Stop()
				return err
			case <-t.C:
			}
		}
		if err = appendParentReceipt(ctx, req, payload); !errors.Is(err, store.ErrWriterExists) {
			return err
		}
	}
	return err
}

// appendParentReceipt opens the parent's writer under the per-parent lock,
// appends the receipt and closes the writer.
func appendParentReceipt(ctx context.Context, req *DelegateRequest, payload map[string]any) error {
	defer receiptLocks.acquire(req.Parent.ID)()
	w, err := req.Store.NewEventWriter(ctx, req.Parent.ID)
	if err != nil {
		return fmt.Errorf("app: delegate: receipt writer: %w", err)
	}
	defer func() { _ = w.Close() }()
	return recordReceipt(ctx, w, payload)
}

// recordReceipt appends the receipt to sink.
func recordReceipt(ctx context.Context, sink tool.EventSink, payload map[string]any) error {
	if _, err := sink.Append(ctx, session.ActorEngine, TypeDelegationCompleted, payload); err != nil {
		return fmt.Errorf("app: delegate: record receipt: %w", err)
	}
	return nil
}

// receiptLocks serialises the fallback receipt writers by parent session id.
var receiptLocks = &receiptLockTable{locks: map[string]*receiptLock{}}

// receiptWriterAttempts and receiptWriterBackoff bound the wait for a parent
// writer held elsewhere.
const (
	receiptWriterAttempts = 5
	receiptWriterBackoff  = 20 * time.Millisecond
)

// acquire locks id and returns the unlock; the entry is dropped with its last user.
func (t *receiptLockTable) acquire(id string) func() {
	t.mu.Lock()
	l := t.locks[id]
	if l == nil {
		l = &receiptLock{}
		t.locks[id] = l
	}
	l.users++
	t.mu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		t.mu.Lock()
		l.users--
		if l.users == 0 {
			delete(t.locks, id)
		}
		t.mu.Unlock()
	}
}
