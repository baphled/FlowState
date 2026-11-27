package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// ResumeDelegatedChild continues an interrupted child of rr.Parent (ADR-009 D8)
// through the same run-and-settle tail as RunDelegatedChild.
//
// Nothing is written or claimed until the request is authorised: the id must be a
// plausible session id (see validResumeID), and the child must be this parent's,
// resumable and under its recorded, unchanged profile with turns left (see
// inspectResume). Only then is the child's exec lease claimed, under an owner
// unique to this run ("delegate:<run id>"), and everything is read again under
// the claim; the claim is released on every refusal and failure. A held lease
// means the child is running, or its runner was killed with its process and left
// a stale lease (nothing clears it yet): "child is running". The loop re-grants
// the same owner and its release ends the claim.
//
// A request is refused, as an ErrUsage error with nothing written or held, when
// the id is malformed, not a child of this parent (unknown, another parent's, a
// root), the child is not active or paused, it has no recorded profile, its
// loaded profile is gone or changed, it used all its turns, or it is running.
//
// The budget follows the fresh path. A child has one reservation row, so the
// "old reservation charged, new one granted" step is: the row is charged for the
// turns the log shows (see countTurns), and the grant is max_turns minus those,
// also the loop's MaxTurns. The parent's pool is asked for it first; a pool that
// cannot cover it, or a reservation no longer live, is a failed budget_exhausted
// result that changes nothing.
//
// A crashed child gets session.interrupted, a paused one session.resumed, and a
// task becomes a turn.start user message, before the loop rebuilds the history
// from the log. The child runs in the parent's working directory. Its recovery
// is a crashed root's (RecoverCrashedSession): a dangling delegate call of the
// child is answered with ITS grandchildren, whose stale leases are cleared, and a
// profile that lists `delegate` gets its own tool again (see bindNestedDelegate).
func ResumeDelegatedChild(ctx context.Context, rr DelegateResumeRequest) (DelegateResult, error) {
	req := rr.DelegateRequest
	if req.Store == nil || req.Parent == nil || req.Streamer == nil {
		return DelegateResult{}, fmt.Errorf("%w: delegate: store, parent and streamer are required", ErrUsage)
	}
	if !validResumeID(rr.ChildID) {
		return DelegateResult{}, fmt.Errorf("%w: %q is not a child of this session", ErrUsage, capDelegateText(rr.ChildID))
	}
	req.Profiles = rr.Profiles
	if _, _, _, err := inspectResume(ctx, req.Store, req.Parent.ID, rr); err != nil {
		return DelegateResult{}, err
	}
	slot, ok := req.limiter.acquire(ctx)
	if !ok {
		return DelegateResult{ChildID: rr.ChildID, Status: DelegateStatusCancelled, Reason: CancelReason(ctx)}, nil
	}
	defer slot.release()
	owner, err := newDelegateOwner()
	if err != nil {
		return DelegateResult{}, err
	}
	item := "exec:" + rr.ChildID
	lease, err := req.Store.AcquireRunLease(ctx, item, owner)
	if errors.Is(err, store.ErrSessionBusy) {
		return DelegateResult{}, fmt.Errorf("%w: child %s is running", ErrUsage, rr.ChildID)
	} else if err != nil {
		return DelegateResult{}, fmt.Errorf("app: delegate: child lease: %w", err)
	}
	defer func() { _ = lease.Release(context.WithoutCancel(ctx)) }()
	run, registry, err := planResume(ctx, &req, rr)
	if err != nil {
		return DelegateResult{}, err
	}
	run.owner, run.slot, run.lease = owner, slot, lease
	if out, berr := reserveResumeBudget(ctx, &req, run); berr != nil || out.Status != "" {
		return out, berr
	}
	if err = prepareResumedChild(ctx, req.Store, run.child, req.Task); err != nil {
		if req.Pool != nil {
			req.Pool.Release(run.grant)
		}
		return DelegateResult{}, err
	}
	return runAndSettleDelegated(ctx, &req, run, registry)
}

// resumeIDMax is the longest child id a resume accepts; generated ids are 36.
const resumeIDMax = 64

// validResumeID reports whether id could be a session id: 1 to resumeIDMax of
// ASCII letters, digits, '-' and '_'. Generated ids are UUIDs. The check runs
// before any store access, so arbitrary model text reaches neither a query nor a
// lease row.
func validResumeID(id string) bool {
	if id == "" || len(id) > resumeIDMax {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

// inspectResume reads, without writing, what decides whether rr may proceed: the
// child (an active or paused child of parentID), its recorded profile (loaded and
// unchanged) and its turns (some left). It returns the child, the profile and the
// turns the child has left.
func inspectResume(ctx context.Context, st *store.Store, parentID string, rr DelegateResumeRequest) (*store.Session, agent.Profile, int64, error) {
	child, log, err := loadResumableChild(ctx, st, parentID, rr.ChildID)
	if err != nil {
		return nil, agent.Profile{}, 0, err
	}
	if aerr := rr.allow.check(log.profile.Name); aerr != nil {
		return nil, agent.Profile{}, 0, aerr
	}
	profile, err := currentProfile(rr.Profiles, log.profile)
	if err != nil {
		return nil, agent.Profile{}, 0, err
	}
	remaining := int64(profile.MaxTurns) - log.turns
	if remaining <= 0 {
		return nil, agent.Profile{}, 0, fmt.Errorf("%w: child %s has used all %d of its turns; start a new one",
			ErrUsage, child.ID, profile.MaxTurns)
	}
	return child, profile, remaining, nil
}

// planResume repeats the inspection under the claim, then makes the refusals that
// need the request: its form and the child's registry. On success req carries the
// loaded profile and the run its grant.
func planResume(ctx context.Context, req *DelegateRequest, rr DelegateResumeRequest) (*delegateRun, *tool.Registry, error) {
	child, profile, remaining, err := inspectResume(ctx, req.Store, req.Parent.ID, rr)
	if err != nil {
		return nil, nil, err
	}
	req.Profile = profile
	req.Workdir = ""
	if verr := validateDelegateRequest(req); verr != nil {
		return nil, nil, verr
	}
	if nerr := checkNestDepth(ctx, req.Store, req.Parent.ID, profile); nerr != nil {
		return nil, nil, nerr
	}
	registry, offered, workdir, err := childRegistry(req)
	if err != nil {
		return nil, nil, err
	}
	run := &delegateRun{child: child, grant: remaining, prior: int64(profile.MaxTurns) - remaining, resumed: true, workdir: workdir, offered: offered}
	return run, registry, nil
}

// loadResumableChild loads the child and its log and refuses anything but an
// active or paused child of parentID that a profile started.
func loadResumableChild(ctx context.Context, st *store.Store, parentID, id string) (*store.Session, resumableLog, error) {
	child, err := st.GetSession(ctx, id)
	switch {
	case errors.Is(err, store.ErrUnknownSession), err == nil && child.ParentID != parentID:
		return nil, resumableLog{}, fmt.Errorf("%w: %s is not a child of this session", ErrUsage, id)
	case err != nil:
		return nil, resumableLog{}, fmt.Errorf("app: delegate: load child: %w", err)
	case child.Status != "active" && child.Status != "paused":
		return nil, resumableLog{}, fmt.Errorf("%w: child %s is %s and cannot be resumed; start a new one",
			ErrUsage, id, child.Status)
	}
	events, err := st.Replay(ctx, id)
	if err != nil {
		return nil, resumableLog{}, fmt.Errorf("app: delegate: replay child: %w", err)
	}
	log := resumableLog{}
	log.turns, _ = countTurns(events)
	for _, e := range events {
		if e.Type == TypeSessionDelegated {
			var p struct {
				Profile *DelegateProfileRef `json:"profile"`
			}
			_ = json.Unmarshal([]byte(e.PayloadJSON), &p)
			log.profile = p.Profile
		}
	}
	if log.profile == nil {
		return nil, resumableLog{}, fmt.Errorf("%w: child %s was not started from a profile", ErrUsage, id)
	}
	return child, log, nil
}

// countTurns is the number of turns a child's log charges it, and whether the log
// ends inside a turn. Every assistant.message is a turn. A turn the process died
// in leaves no message and is charged too, but only when the log shows it in
// flight: an assistant.delta after the last message, or a tool.call without its
// tool.result whose turn wrote no message (the loop writes the message before it
// runs the calls, so a call after a message belongs to a turn already charged).
// A tool.result or turn.start as the last event is not in flight: as far as the log
// can tell the next turn had not begun. A lost turn is charged for good at its
// session.interrupted marker (see markLostTurn), so a child never exceeds max_turns
// across crashes.
//
// The count errs the other way too: a provider request that was in flight but
// wrote nothing is not counted, so one provider turn per crash can go uncharged.
func countTurns(events []store.RecordedEvent) (turns int64, inflight bool) {
	open := map[string]bool{}
	messaged := false
	for _, e := range events {
		switch e.Type {
		case TypeAssistantMessage:
			turns, inflight, messaged = turns+1, false, true
		case TypeAssistantDelta:
			inflight, messaged = true, false
		case tool.TypeToolCall:
			if !messaged {
				open[payloadFieldText(e.PayloadJSON, "tool_call_id")] = true
			}
		case tool.TypeToolResult:
			delete(open, payloadFieldText(e.PayloadJSON, "tool_call_id"))
		case session.TypeTurnStart:
			messaged = false
		case TypeTurnEnd, "session.interrupted":
			if e.Type == "session.interrupted" && (inflight || len(open) > 0) {
				turns++
			}
			inflight, messaged, open = false, false, map[string]bool{}
		}
	}
	if inflight = inflight || len(open) > 0; inflight {
		turns++
	}
	return turns, inflight
}

// currentProfile finds the loaded profile the child recorded: it must still be
// loaded and byte-identical, so a child never continues under a changed prompt.
func currentProfile(profiles []agent.Profile, ref *DelegateProfileRef) (agent.Profile, error) {
	for _, p := range profiles {
		if p.Name != ref.Name {
			continue
		}
		if p.SHA256 != ref.SHA256 {
			return agent.Profile{}, fmt.Errorf("%w: profile %s changed since the child started; start a new one",
				ErrUsage, ref.Name)
		}
		return p, nil
	}
	return agent.Profile{}, fmt.Errorf("%w: profile %s is not loaded; start a new one", ErrUsage, ref.Name)
}

// reserveResumeBudget draws the remainder from the parent's pool and charges the
// child's reservation for the turns already used. Nothing is held on a refusal:
// the pool is asked first, and the reservation is untouched until it is covered.
func reserveResumeBudget(ctx context.Context, req *DelegateRequest, run *delegateRun) (DelegateResult, error) {
	childID, remaining, used := run.child.ID, run.grant, run.prior
	if req.Pool == nil {
		return DelegateResult{}, nil
	}
	failed := func(why string) DelegateResult {
		return DelegateResult{ChildID: childID, Status: DelegateStatusFailed, Reason: "budget_exhausted: " + why}
	}
	res, err := req.Store.ReservationFor(ctx, childID)
	if err != nil && !errors.Is(err, store.ErrUnknownReservation) {
		return DelegateResult{}, fmt.Errorf("app: delegate: read reservation: %w", err)
	}
	if err != nil || res.Status != store.ReservationStatusReserved {
		return failed("the child's reservation is not live"), nil
	}
	if got := req.Pool.Reserve(remaining); got != remaining {
		req.Pool.Release(got)
		return failed(fmt.Sprintf("the parent pool cannot cover %d turns", remaining)), nil
	}
	if err := chargeDelegation(ctx, req.Store, childID, used); err != nil {
		req.Pool.Release(remaining)
		return DelegateResult{}, err
	}
	return DelegateResult{}, nil
}

// markLostTurn appends session.interrupted when the log still ends in a turn after
// crash recovery, so each lost turn is charged once.
func markLostTurn(ctx context.Context, st *store.Store, id string) error {
	events, err := st.Replay(ctx, id)
	if err != nil {
		return fmt.Errorf("app: delegate: replay child: %w", err)
	}
	if _, inflight := countTurns(events); !inflight {
		return nil
	}
	w, err := st.NewEventWriter(ctx, id)
	if err != nil {
		return fmt.Errorf("app: delegate: interrupt writer: %w", err)
	}
	defer func() { _ = w.Close() }()
	_, err = w.Append(ctx, session.ActorEngine, "session.interrupted", map[string]any{"reason": "process_lost"})
	return err
}

// prepareResumedChild brings the child's log to a state the loop can continue:
// a crash is marked, a pause resumed and a follow-up task recorded as the next
// user message.
func prepareResumedChild(ctx context.Context, st *store.Store, child *store.Session, task string) error {
	if err := RecoverCrashedSession(ctx, st, child.ID); err != nil {
		return err
	}
	if err := markLostTurn(ctx, st, child.ID); err != nil {
		return err
	}
	if err := ResumePausedSession(ctx, st, child); err != nil {
		return err
	}
	if task == "" {
		return nil
	}
	w, err := st.NewEventWriter(ctx, child.ID)
	if err != nil {
		return fmt.Errorf("app: delegate: follow-up writer: %w", err)
	}
	defer func() { _ = w.Close() }()
	if _, err := w.Append(ctx, session.ActorUser, session.TypeTurnStart, map[string]any{"task": task}); err != nil {
		return fmt.Errorf("app: delegate: record follow-up: %w", err)
	}
	return nil
}
