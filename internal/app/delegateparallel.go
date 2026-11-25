package app

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/tool"
)

// Limits of one parallel delegate call (ADR-009 D1).
const (
	// delegateTasksMax is how many tasks one call may hold.
	delegateTasksMax = 8
	// delegateParallelMax is how many children one call runs at once.
	delegateParallelMax = 4
)

// Append serialises the write: the parent's per-call writer is one writer, and
// up to delegateParallelMax children record their receipts through it.
func (s *lockedSink) Append(ctx context.Context, actor, typ string, payload any) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.Append(ctx, actor, typ, payload)
}

// invokeParallel runs the validated entries, at most delegateParallelMax at a
// time, each through RunDelegatedChild exactly as the single form does, and
// returns one DelegateParallelResult in input order. Every agent is resolved
// before the first child starts. A child that fails, is cancelled or is refused
// (a budget, a working directory outside the parent's) is that entry's result and
// does not touch its siblings. The call fails only for an infrastructure failure
// that must end the run (tool.ErrEventAppend). A child panic is settled by
// RunDelegatedChild, recorded as a failed entry, and re-raised here once every
// sibling has finished. No goroutine outlives the call. A cancelled parent
// cancels the running children (with its reason) and the entries not yet
// started, which create no session; a deadline is not a cancellation.
func (t *DelegateTool) invokeParallel(ctx context.Context, entries []delegateTaskArg) (any, error) {
	profiles := make([]agent.Profile, len(entries))
	for i, e := range entries {
		p, err := t.profile(e.Agent)
		if err != nil {
			return nil, fmt.Errorf("tasks[%d]: %w", i, err)
		}
		profiles[i] = p
	}
	var sink tool.EventSink
	if s, ok := ctx.Value(delegateParentEventsKey{}).(tool.EventSink); ok && s != nil {
		sink = appendFailureSink{inner: &lockedSink{inner: s}}
	}
	slots := make([]delegateSlot, len(entries))
	sem := make(chan struct{}, delegateParallelMax)
	var wg sync.WaitGroup
	for i := range entries {
		if !acquireDelegateSlot(ctx, sem) {
			slots[i].result = DelegateTaskResult{
				Index: i, Agent: entries[i].Agent, Status: DelegateStatusCancelled, Reason: CancelReason(ctx),
			}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			slots[i] = t.runEntry(ctx, i, entries[i], profiles[i], sink)
		}()
	}
	wg.Wait()
	return collectDelegateSlots(slots)
}

// acquireDelegateSlot waits for a running slot. It reports false, holding
// nothing, once ctx is cancelled; an expired deadline is not a cancellation, so
// the entry still waits for its turn.
func acquireDelegateSlot(ctx context.Context, sem chan struct{}) bool {
	select {
	case sem <- struct{}{}:
		if errors.Is(ctx.Err(), context.Canceled) {
			<-sem
			return false
		}
		return true
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.Canceled) {
			return false
		}
		sem <- struct{}{}
		return true
	}
}

// runEntry runs one child and shapes its outcome as an entry. A panic is
// recovered so it cannot end the process from a goroutine; the caller re-raises it.
func (t *DelegateTool) runEntry(ctx context.Context, i int, e delegateTaskArg, profile agent.Profile,
	sink tool.EventSink) (slot delegateSlot) {
	slot.result = DelegateTaskResult{Index: i, Agent: e.Agent}
	defer func() {
		if p := recover(); p != nil {
			slot.panicked, slot.panicValue = true, p
			slot.result.Status, slot.result.Reason = DelegateStatusFailed, errDelegatePanic.Error()
		}
	}()
	req := t.baseRequest()
	req.Profile, req.Task, req.Workdir, req.ParentEvents, req.BatchIndex = profile, e.Task, e.Cwd, sink, &i
	res, err := RunDelegatedChild(ctx, req)
	slot.result = delegateEntryResult(i, e.Agent, res, err)
	if errors.Is(err, tool.ErrEventAppend) {
		slot.fatal = err
	}
	return slot
}

// delegateEntryResult shapes a child's outcome as an entry. An error alongside a
// result that is not cancelled makes the entry failed with the error as its
// reason, unless the child already carries one.
func delegateEntryResult(i int, agentName string, res DelegateResult, err error) DelegateTaskResult {
	r := DelegateTaskResult{
		Index: i, Agent: agentName, Child: res.ChildID, Status: res.Status, Turns: res.Turns,
		Text: capDelegateText(res.FinalContent),
	}
	if res.Status != DelegateStatusCompleted {
		r.Reason = capDelegateText(res.Reason)
	}
	if err != nil && r.Status != DelegateStatusCancelled {
		r.Status = DelegateStatusFailed
		if r.Reason == "" {
			r.Reason = capDelegateText(err.Error())
		}
	}
	return r
}

// collectDelegateSlots turns the finished entries into the call's outcome: a
// recovered panic is re-raised first (the lowest index), then an infrastructure
// failure is returned, else the results in input order.
func collectDelegateSlots(slots []delegateSlot) (any, error) {
	out := DelegateParallelResult{Results: make([]DelegateTaskResult, len(slots))}
	var fatal error
	for i, s := range slots {
		if s.panicked {
			panic(s.panicValue)
		}
		if fatal == nil {
			fatal = s.fatal
		}
		out.Results[i] = s.result
	}
	if fatal != nil {
		return nil, fatal
	}
	return out, nil
}
