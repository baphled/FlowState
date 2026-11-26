package app

import (
	"context"
	"fmt"
	"slices"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/tool"
)

// newDescendantLimiter returns a limiter of n slots, DefaultDescendantLimit when
// n is not positive.
func newDescendantLimiter(n int) *descendantLimiter {
	if n <= 0 {
		n = DefaultDescendantLimit
	}
	return &descendantLimiter{sem: make(chan struct{}, n)}
}

// acquire waits for a slot and returns the run's hold on it. It reports false,
// holding nothing, once ctx is cancelled; a nil limiter (no cap) always succeeds
// with a nil slot, whose methods do nothing.
func (l *descendantLimiter) acquire(ctx context.Context) (*runSlot, bool) {
	if l == nil {
		return nil, true
	}
	if !acquireDelegateSlot(ctx, l.sem) {
		return nil, false
	}
	return &runSlot{limiter: l, held: true}, true
}

// yield gives the slot back while the run waits for its own delegated children.
// This is what prevents the classic deadlock: with every slot held by a child
// that needs one more for a grandchild, nobody could ever run.
func (s *runSlot) yield() {
	if s != nil && s.held {
		s.held = false
		<-s.limiter.sem
	}
}

// reclaim waits for a slot again after the children returned. A cancelled ctx
// leaves the run without one: it only has to end, which needs no slot.
func (s *runSlot) reclaim(ctx context.Context) {
	if s != nil && !s.held && acquireDelegateSlot(ctx, s.limiter.sem) {
		s.held = true
	}
}

// release gives the slot back when the run ends, if it holds one.
func (s *runSlot) release() { s.yield() }

// checkNestDepth refuses, before anything is written, a profile that lists
// `delegate` for a child that would sit at the nesting limit: a child's depth is
// its parent's chain plus one, and only a child with a level below it may delegate.
// CreateChildSession's own depth check stays as the backstop.
func checkNestDepth(ctx context.Context, st *Store, parentID string, profile agent.Profile) error {
	if !slices.Contains(profile.Tools, agent.DelegateTool) {
		return nil
	}
	chain, err := st.DelegationChain(ctx, parentID)
	if err != nil {
		return fmt.Errorf("%w: delegation chain: %v", ErrUsage, err)
	}
	if depth := len(chain) + 1; depth >= MaxDelegationDepth {
		return fmt.Errorf("%w: profile %s: delegate is not available at depth %d: the nesting limit is %d",
			ErrUsage, profile.Name, depth, MaxDelegationDepth)
	}
	return nil
}

// check refuses a profile name the delegating profile does not list. It is
// fail-closed: a profile that lists `delegate` but declares no `delegates`
// may delegate to nothing. A nil allowance allows every name.
func (a *profileAllowance) check(name string) error {
	switch {
	case a == nil, slices.Contains(a.names, name):
		return nil
	case len(a.names) == 0:
		return fmt.Errorf("%w: profile %s lists delegate but declares no delegates", ErrUsage, a.delegator)
	}
	return fmt.Errorf("%w: profile %s may not delegate to %s: it is not in %s's delegates", ErrUsage,
		a.delegator, clipRunes(name, delegateNameMaxChars), a.delegator)
}

// allowance is the restriction this tool applies, nil for the root's.
func (t *DelegateTool) allowance() *profileAllowance {
	if t.cfg.DelegatorProfile == "" {
		return nil
	}
	return &profileAllowance{delegator: t.cfg.DelegatorProfile, names: t.cfg.AllowedProfiles}
}

// offered is the loaded profiles this tool may spawn: all of them for the
// root, only the delegator's `delegates` for a nested tool.
func (t *DelegateTool) offered() []agent.Profile {
	a := t.allowance()
	if a == nil {
		return t.cfg.Profiles
	}
	return slices.DeleteFunc(slices.Clone(t.cfg.Profiles), func(p agent.Profile) bool { return !slices.Contains(a.names, p.Name) })
}

// baseRequest is the request every child of this tool's parent starts from.
func (t *DelegateTool) baseRequest() DelegateRequest {
	return DelegateRequest{
		Store: t.cfg.Store, Parent: t.cfg.Parent, Streamer: t.cfg.Streamer, Pool: t.cfg.Pool,
		ParentWorkdir: t.cfg.Workdir, Profiles: t.cfg.Profiles, limiter: t.cfg.limiter, allow: t.allowance(),
		Compaction: t.cfg.Compaction, Hooks: emptyHookSet(t.cfg.Hooks),
	}
}

// bindNestedDelegate gives a child whose profile lists `delegate` its own delegate
// tool, bound to the CHILD as the parent: its session, the streamer it runs on,
// the pool built from its grant (so the grant bounds the whole subtree: a
// grandchild's grant is carved out of it and the unspent part returns to it),
// its own directory (confinement is relative to it), the loaded profiles of the
// root run and the root's descendant cap. The tool may spawn only the profiles
// the child's own `delegates` list names, so a read-only child cannot reach a
// more privileged profile (ADR-009 D14). Receipts of a grandchild land in the
// child's log through the per-call writer the child's loop passes down. A child
// with a pool also anchors that pool in the store, where the grandchildren's
// reservations are checked against it.
func bindNestedDelegate(ctx context.Context, req *DelegateRequest, run *delegateRun, registry *tool.Registry) error {
	if run.pool != nil {
		if err := req.Store.SetBudgetPool(ctx, run.child.ID, run.grant+run.prior); err != nil {
			return fmt.Errorf("app: delegate: anchor the child's pool: %w", err)
		}
	}
	return registry.Register(NewDelegateTool(DelegateToolConfig{
		Store: req.Store, Parent: run.child, Streamer: req.Streamer, Pool: run.pool, Workdir: run.workdir,
		Profiles: req.Profiles, limiter: req.limiter, slot: run.slot, Compaction: req.Compaction, Hooks: req.Hooks,
		DelegatorProfile: req.Profile.Name, AllowedProfiles: req.Profile.Delegates,
	}))
}
