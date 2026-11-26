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
	"strings"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
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
	return crashedFromEvents(events), nil
}

// crashedFromEvents applies the crash predicate to an already
// replayed event log so callers that need several facts replay once.
func crashedFromEvents(events []store.RecordedEvent) bool {
	// A freshly created session has only the initial turn.start from
	// session.Create — it has not been run yet. Crash recovery requires
	// evidence that a run was actually in progress: at least one
	// provider request, tool call or streamed content-delta event. The
	// delta matters because the run path persists deltas for every
	// turn, including tool-less ones (ADR-006 run-activity evidence).
	hasRunActivity := false
	for _, e := range events {
		if e.Type == "provider.request" || e.Type == "tool.call" || e.Type == TypeAssistantDelta {
			hasRunActivity = true
		}
	}
	if !hasRunActivity {
		return false
	}
	for i := len(events) - 1; i >= 0; i-- {
		switch events[i].Type {
		case "turn.start":
			return true
		case "turn.end", "session.cancelled", "session.paused", "session.interrupted":
			return false
		}
	}
	return false
}

// RecoverCrashedSession records what a crashed run left undone before the
// resumed run proceeds, with the store's event writer in the same atomic
// pattern as ResumePausedSession. First it answers every `delegate` call that
// has no result (see answerDelegateCalls), then, if the log ends in a crashed
// turn, appends session.interrupted (actor engine, reason process_lost). The
// order makes a retry safe: a crash between the two leaves the session still
// detected as crashed, and the answered calls are not answered again.
//
// The answers do not depend on the crash marker. A resumed parent writes no
// new turn.start, so a second crash leaves a new dangling call behind the
// last session.interrupted that the marker predicate never looks at. Callers
// therefore call this on every run of a session instead of gating it on
// DetectCrashedSession.
//
// The answer lives here, not in the loop's replay, because ADR-006 puts every
// write that records a crash in this step: it runs once, after the run lease
// (AcquireSessionRunLease) is held and before the first provider request, and
// the replay stays a read-only rebuild of the history that skips a dangling
// call and never re-issues it.
//
// Limit (adjacency): an answer is appended at the end of the log, whenever the
// call happened. A call left unanswered by an earlier build, or behind a paused
// session, is answered late, after unrelated events, so the rebuilt history can
// be assistant{d1} "a", assistant{e1} "b", tool(e1), tool(d1). That passes
// ChatRequest.Validate, which checks pairing and not adjacency, but a provider
// that requires a tool result right after its tool call may reject the request.
// Nothing here reorders the log, so a re-request is not always valid.
//
// Limit (liveness): this function does not judge liveness; its callers do.
// Every writer that calls it holds a judged lease (ADR-011): `session run`,
// serve and `delegate resume` (AcquireSessionRunLease, AcquireRunLease) and
// `session compact` (owner compact:<uuid>), so a live run elsewhere is refused
// before anything is written here. It does not touch the session's own lease
// row (and no longer clears compact: rows): a crashed judged row of this
// machine is taken over by the next acquirer, and a legacy row (one the older
// acquire wrote, which records no machine) is refused by every acquirer as
// unjudgeable until `session unlock <id> --force`. Known limit L1 (ADR-011): it
// still clears the unfinished delegate children's delegate: rows
// (Store.ClearLeaseOwnedBy) without judging them, which can fence a live
// resumed child.
func RecoverCrashedSession(ctx context.Context, st *store.Store, sessionID string) error {
	events, rerr := st.Replay(ctx, sessionID)
	if rerr != nil {
		return fmt.Errorf("crash recovery detection: crash detection replay: %w", rerr)
	}
	crashed := crashedFromEvents(events)
	// Only an active session is answered: a paused session is resumed before its calls are, and a crash
	// answer belongs to a run that was active. (EventWriter.Append has no paused check: hook events are
	// written to a paused session before the resume, ADR-012.)
	sess, gerr := st.GetSession(ctx, sessionID)
	if gerr != nil {
		return fmt.Errorf("crash recovery session: %w", gerr)
	}
	answer := sess.Status == "active" && len(unansweredDelegateCalls(events)) > 0
	if !crashed && !answer {
		return nil
	}
	w, err := st.NewEventWriter(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("crash recovery writer: %w", err)
	}
	defer w.Close()
	if answer {
		if derr := answerDelegateCalls(ctx, st, w, events); derr != nil {
			return fmt.Errorf("crash recovery delegate calls: %w", derr)
		}
	}
	if !crashed {
		return nil
	}
	if _, aerr := w.Append(ctx, "engine", "session.interrupted", map[string]any{
		"reason": "process_lost",
	}); aerr != nil {
		return fmt.Errorf("crash recovery append: %w", aerr)
	}
	return nil
}

// unansweredDelegateCalls returns the index in events of each `delegate`
// tool.call that has no correlated tool.result, in log order.
func unansweredDelegateCalls(events []store.RecordedEvent) []int {
	answered := answeredResultIDs(events)
	var out []int
	for i, e := range events {
		id := payloadFieldText(e.PayloadJSON, "tool_call_id")
		if e.Type == tool.TypeToolCall && payloadFieldText(e.PayloadJSON, "tool") == agent.DelegateTool &&
			id != "" && !answered[id] {
			out = append(out, i)
		}
	}
	return out
}

// answerDelegateCalls appends, for each dangling `delegate` call, a recorded
// error result (actor engine) that says the call was interrupted by a crash and
// lists the children it started, with their status and what the model can do:
// resume an unfinished one, or start anew for one that finished but whose
// result was lost. The engine does not re-issue the call. The leases of those
// children are released first: no live run holds the parent, so their runners
// are assumed gone (not checked: known limit L1, ADR-011) and a lease, which
// never expires, would block their resume. Releasing first keeps a retry safe: a crash before the result leaves
// the call dangling, so it is answered and released again. A call that already
// has a result is skipped, so running recovery again never adds a second one.
// Other dangling tools are left alone.
func answerDelegateCalls(ctx context.Context, st *store.Store, w *store.EventWriter, events []store.RecordedEvent) error {
	for _, i := range unansweredDelegateCalls(events) {
		children := danglingChildren(ctx, st, events, i)
		for _, c := range children {
			if c.completed {
				continue // its runner ended and released its lease
			}
			if err := st.ClearLeaseOwnedBy(ctx, "exec:"+c.id, delegateLeasePrefix, delegateLegacyOwner); err != nil {
				return fmt.Errorf("release child lease: %w", err)
			}
		}
		if _, err := w.Append(ctx, session.ActorEngine, tool.TypeToolResult, map[string]any{
			"tool": agent.DelegateTool, "tool_call_id": payloadFieldText(events[i].PayloadJSON, "tool_call_id"), "ok": false,
			"error": map[string]any{"code": "delegate_interrupted", "message": interruptedDelegateMessage(children)},
		}); err != nil {
			return err
		}
	}
	return nil
}

// danglingChildren lists the children the delegate call at events[call] started,
// in start order, each with the status the store holds now and whether its
// delegation.completed receipt was written (its result was then lost with the
// call's tool.result).
// Its span ends at the next delegate call, so a later call's children are not
// attributed to it. Only well-formed ids are listed, and only ids and statuses
// ever reach the text: the profile name and path in the same events are
// attacker-influenced and never read.
func danglingChildren(ctx context.Context, st *store.Store, events []store.RecordedEvent, call int) []danglingChild {
	span := events[call+1:]
	for j, e := range span {
		if e.Type == tool.TypeToolCall && payloadFieldText(e.PayloadJSON, "tool") == agent.DelegateTool {
			span = span[:j]
			break
		}
	}
	done := map[string]bool{}
	for _, e := range span {
		if e.Type == TypeDelegationCompleted {
			done[payloadFieldText(e.PayloadJSON, "child_id")] = true
		}
	}
	listed := map[string]bool{}
	var out []danglingChild
	for _, e := range span {
		id := payloadFieldText(e.PayloadJSON, "child_id")
		if e.Type != TypeDelegationStarted || listed[id] || !validResumeID(id) {
			continue
		}
		listed[id] = true // a resumed child can start twice
		status := "not found"
		if c, err := st.GetSession(ctx, id); err == nil {
			status = c.Status
		}
		out = append(out, danglingChild{id: id, status: status, completed: done[id]})
	}
	return out
}

// interruptedDelegateMessage is the text the model reads in place of the result
// the crash prevented.
func interruptedDelegateMessage(children []danglingChild) string {
	if len(children) == 0 {
		return "the delegate call was interrupted by a crash; no child had started; call again"
	}
	var b strings.Builder
	b.WriteString("the delegate call was interrupted by a crash; the children it started:")
	for _, c := range children {
		fmt.Fprintf(&b, "\n- %s (%s): ", c.id, c.status)
		switch {
		case c.completed:
			b.WriteString("it finished, but its result was not delivered and cannot be read back; " +
				"it cannot be resumed; start a new one only if you still need that work")
		case c.status == "active", c.status == "paused":
			fmt.Fprintf(&b, "resume it with delegate {resume: %q} or start a new one", c.id)
		case c.status == "not found":
			b.WriteString("its session no longer exists; start a new one")
		default:
			b.WriteString("it already ended and cannot be resumed; start a new one")
		}
	}
	return b.String()
}

// AcquireSessionRunLease takes the exec lease of sess for one `session run` or
// serve run under a fresh owner, run:<uuid>, by the judging acquire (ADR-011
// rules 5 and 7); acquireOwnedRunLease does the work for any owner prefix.
// Callers load the session and reject terminal states first (ADR-002 phase
// 1), then call this, and run RecoverCrashedSession, the resume
// and the steering drain only on success: a refused run writes nothing. The
// status is read again once the lease is held, because it may have changed in
// between; the returned session is that fresh row, and a session that became
// terminal is refused with ErrTerminal and the lease released.
//
// A held or unjudgeable lease is refused with an error that matches
// store.ErrSessionBusy and names the holder. The caller keeps the handle until
// the run's last writer has returned (the loop, the text-only turn, the
// post-loop writers) and then releases it, on every path.
func AcquireSessionRunLease(ctx context.Context, st *store.Store, sess *store.Session) (*store.RunLease, *store.Session, error) {
	return acquireOwnedRunLease(ctx, st, sess, "run:")
}

// acquireOwnedRunLease is AcquireSessionRunLease for the owner prefix (run: or
// compact:), followed by a fresh uuid.
func acquireOwnedRunLease(ctx context.Context, st *store.Store, sess *store.Session, prefix string,
) (*store.RunLease, *store.Session, error) {
	id, err := session.NewID()
	if err != nil {
		return nil, nil, fmt.Errorf("run lease owner: %w", err)
	}
	lease, err := st.AcquireRunLease(ctx, "exec:"+sess.ID, prefix+id)
	if err != nil {
		return nil, nil, fmt.Errorf("run lease: %w", err)
	}
	fresh, err := st.GetSession(ctx, sess.ID)
	if err == nil && fresh.Status != "active" && fresh.Status != "paused" {
		err = fmt.Errorf("%w: session %s is %s", ErrTerminal, sess.ID, fresh.Status)
	}
	if err != nil {
		_ = lease.Release(context.WithoutCancel(ctx))
		return nil, nil, err
	}
	return lease, fresh, nil
}
