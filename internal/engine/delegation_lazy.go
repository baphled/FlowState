package engine

import (
	"context"
	"fmt"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/delegation"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tool"
)

// DelegateTargetFactory lazily constructs (or returns a cached) engine
// and streamer for a delegation target agent. It is consulted ONLY
// after the delegate tool's pre-spawn gates (can_delegate, allowlist,
// cycle, depth, budget, background rejection) have accepted the call,
// so factory invocations imply a sanctioned spawn.
type DelegateTargetFactory func(agentID string) (*Engine, streaming.Streamer, error)

// NewLazyDelegateTool constructs a DelegateTool whose target engines
// are resolved on demand through targetFactory instead of an eagerly
// built engines map. This is the shared session-engine contract for
// NESTED delegation: a delegated lead receives a scoped delegate tool
// without preconstructing the delegation graph
// (DELEGATION-RUNTIME-BLOCKER-2026-10-21).
//
// The lazy tool behaves identically to NewDelegateToolWithBackground
// after resolution: all guards run inside Execute before any factory
// call, and the background path is refused before spawn (see
// lazyRefuseBackground).
//
// Expected:
//   - targetFactory resolves an agent id to its engine/streamer.
//   - delegationConfig carries the delegating manifest's policy.
//   - sourceAgentID identifies the agent that owns this tool.
//   - backgroundManager and coordinationStore follow the same
//     contract as NewDelegateToolWithBackground.
//
// Returns:
//   - A *DelegateTool with an empty engines map and the factory set.
//
// Side effects:
//   - None at construction; targets are built lazily on Execute.
func NewLazyDelegateTool(
	targetFactory DelegateTargetFactory,
	delegationConfig agent.Delegation,
	sourceAgentID string,
	backgroundManager *BackgroundTaskManager,
	coordinationStore coordination.Store,
) *DelegateTool {
	dt := NewDelegateToolWithBackground(
		nil, delegationConfig, sourceAgentID, backgroundManager, coordinationStore,
	)
	dt.targetFactory = targetFactory
	return dt
}

// lazyEnsureTarget resolves the engine for the parsed target through
// the factory (memoised into d.engines) so the standard
// resolveTargetWithOptions lookup succeeds. Called from Execute AFTER
// the shared pre-flight gates and the allowlist/scope/cycle gate, so
// forbidden targets never construct an engine.
//
// Expected:
//   - ctx is the delegate execution context.
//   - input carries the delegate-tool arguments to parse.
//
// Returns:
//   - nil after the target engine is present in d.engines.
//   - A parse, routing, gate or factory error otherwise.
//
// Side effects:
//   - On first use per target id, invokes targetFactory and caches the
//     constructed engine into d.engines.
func (d *DelegateTool) lazyEnsureTarget(ctx context.Context, input tool.Input) error {
	if d.targetFactory == nil {
		return nil
	}
	if input.Arguments == nil {
		return errRoutingFieldRequired
	}
	params, err := d.parseDelegationParams(input)
	if err != nil {
		return err
	}
	// Guard order matches the eager path: can-delegate and spawn limits
	// already ran in preFlightSharedGates; allowlist/cycle gating runs
	// here on the parsed target BEFORE any factory call so forbidden
	// targets never construct an engine.
	targetAgentID, err := d.resolveAgentID(ctx, params)
	if err != nil {
		return err
	}
	if err := d.lazyCheckTargetGate(ctx, targetAgentID, params); err != nil {
		return err
	}
	if _, ok := d.engines[targetAgentID]; ok {
		return nil
	}
	eng, _, err := d.targetFactory(targetAgentID)
	if err != nil {
		return err
	}
	if d.engines == nil {
		d.engines = make(map[string]*Engine)
	}
	d.engines[targetAgentID] = eng
	return nil
}

// lazyCheckTargetGate applies the same allowlist / permissive-scope /
// cycle gates as resolveTargetWithOptions so a lazy tool never
// constructs (spawns) an engine for a target the eager path would
// have rejected.
//
// Expected:
//   - ctx is the delegate execution context.
//   - targetAgentID is the resolved target agent.
//   - params carries the parsed delegation input (handoff).
//
// Returns:
//   - nil when the target passes scope/roster/allowlist and cycle gates.
//   - A rejection error naming the violated gate otherwise.
//
// Side effects:
//   - None (read-only policy checks).
func (d *DelegateTool) lazyCheckTargetGate(ctx context.Context, targetAgentID string, params delegationParams) error {
	if d.delegation.IsPermissive() {
		if d.isKnownDelegationTarget(targetAgentID) {
			return nil
		}
		return fmt.Errorf("target not found in registries: %s", targetAgentID)
	}
	if swarmCtx, ok := d.activeSwarmContextForCtx(ctx); ok {
		if !containsAgent(swarmCtx.Members, targetAgentID) {
			return fmt.Errorf("agent %s not permitted by active swarm roster", targetAgentID)
		}
	} else if len(d.delegation.DelegationAllowlist) > 0 && !containsAgent(d.delegation.DelegationAllowlist, targetAgentID) {
		return fmt.Errorf("agent %s not in delegation allowlist", targetAgentID)
	}
	return checkDelegationCycle(d.sourceAgentID, targetAgentID, params.handoff)
}

// SessionDepthFor returns the engine-owned lineage depth for a session
// id, derived from the session manager's authoritative ParentID chain —
// never from model-supplied handoff metadata.
//
// Expected:
//   - sessionID identifies a session in the wired session manager.
//
// Returns:
//   - The session's lineage depth (0 for root/unknown sessions).
//
// Side effects:
//   - None (read-only lookup).
func (d *DelegateTool) SessionDepthFor(sessionID string) int {
	if d.sessionManager == nil || sessionID == "" {
		return 0
	}
	sess, err := d.sessionManager.GetSession(sessionID)
	if err != nil || sess == nil {
		return 0
	}
	return sess.Depth
}

// handoffDepthMetadata extracts the model-supplied depth from handoff
// metadata, for use ONLY as a conservative floor when engine-owned
// lineage is shallower (defensive; see checkSpawnLimits).
//
// Expected:
//   - handoff may be nil or carry Metadata with a "depth" entry.
//
// Returns:
//   - The parsed depth and true when present and numeric.
//   - 0 and false otherwise.
//
// Side effects:
//   - None (read-only metadata parse).
func handoffDepthMetadata(handoff *delegation.Handoff) (int, bool) {
	if handoff == nil || handoff.Metadata == nil {
		return 0, false
	}
	depthStr, ok := handoff.Metadata["depth"]
	if !ok {
		return 0, false
	}
	var depthVal int
	if _, err := fmt.Sscanf(depthStr, "%d", &depthVal); err != nil {
		return 0, false
	}
	return depthVal, true
}
