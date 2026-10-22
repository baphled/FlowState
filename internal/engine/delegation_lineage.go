package engine

import (
	"context"
	"fmt"
)

// DelegationLineageKey is the private context key carrying the
// engine-owned delegation lineage across nested delegate calls.
type delegationLineageKey struct{}

// delegationLineage is the engine-owned record of the delegation
// chain that produced the currently-executing turn. It is derived
// exclusively from context — NEVER from model-supplied handoff
// metadata — so depth, cycle and fan-out enforcement cannot be
// reset or bypassed by a model forging (or omitting) handoff
// fields. Each DelegateTool hop stamps a derived lineage onto the
// child turn's context before the child engine streams, so a
// permitted nested child consults the same immutable chain its
// parent built.
//
// Nested-Delegation Repair (Oct 2026): before this type, depth was
// read from handoff.Metadata["depth"] (defaulting to 0) and the
// visited-agent cycle chain from handoff.Metadata["visited_agents"]
// — both model-controlled. The runtime gates were advisory at best.
type delegationLineage struct {
	// chainID is the delegation chain identifier. Copied from the
	// parent lineage when present so one chain spans the whole tree.
	chainID string
	// depth counts delegate hops from the root coordinator. The root
	// turn runs with no lineage (depth implicitly 0); each delegate
	// call stamps depth+1.
	depth int
	// visited lists every agent visited on this chain, in order,
	// starting with the delegating source at the root hop. Used for
	// cycle detection in addition to the self-delegation check.
	visited map[string]bool
}

// lineageFromContext returns the engine-owned lineage on ctx, if any.
// Absence means the turn is a root turn (not itself delegated).
func lineageFromContext(ctx context.Context) (*delegationLineage, bool) {
	l, ok := ctx.Value(delegationLineageKey{}).(*delegationLineage)
	return l, ok && l != nil
}

// childLineage derives the lineage to stamp onto a delegated child
// turn: depth+1, the source agent appended to the visited set, and
// the authoritative chainID carried across. The receiver is never
// mutated (immutable per-hop derivation).
func (l *delegationLineage) childLineage(sourceAgentID, chainID string) *delegationLineage {
	next := &delegationLineage{
		chainID: chainID,
		depth:   l.depth + 1,
		visited: make(map[string]bool, len(l.visited)+1),
	}
	for id := range l.visited {
		next.visited[id] = true
	}
	next.visited[sourceAgentID] = true
	return next
}

// rootLineage builds the lineage for the FIRST delegate hop off a
// root (non-delegated) turn.
func rootLineage(sourceAgentID, chainID string) *delegationLineage {
	return &delegationLineage{
		chainID: chainID,
		depth:   1,
		visited: map[string]bool{sourceAgentID: true},
	}
}

// contains reports whether agentID is on the visited chain.
func (l *delegationLineage) contains(agentID string) bool {
	return l != nil && l.visited[agentID]
}

// WithDelegationLineage stamps a derived child lineage onto ctx.
func WithDelegationLineage(ctx context.Context, l *delegationLineage) context.Context {
	return context.WithValue(ctx, delegationLineageKey{}, l)
}

// engineOwnedDepth resolves the authoritative depth for a delegate
// call being made from ctx: the engine-owned lineage depth, ignoring
// handoff metadata entirely.
func engineOwnedDepth(ctx context.Context) int {
	if l, ok := lineageFromContext(ctx); ok {
		return l.depth
	}
	return 0
}

// engineOwnedCycleCheck rejects a target that is already on the
// engine-owned visited chain. Self-delegation is rejected here too.
func engineOwnedCycleCheck(ctx context.Context, sourceAgentID, targetAgentID string) error {
	if targetAgentID == sourceAgentID {
		return fmt.Errorf("self-delegation not allowed: agent %q cannot delegate to itself", targetAgentID)
	}
	if l, ok := lineageFromContext(ctx); ok && l.contains(targetAgentID) {
		return fmt.Errorf("delegation cycle detected: agent %q already visited in engine-owned chain (depth %d)", targetAgentID, l.depth)
	}
	return nil
}

// isNestedDelegation reports whether the turn on ctx is itself a
// delegated child. Nested background delegation is rejected
// pre-spawn on every path when this returns true.
func isNestedDelegation(ctx context.Context) bool {
	_, ok := lineageFromContext(ctx)
	return ok
}
