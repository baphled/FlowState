package app

import (
	"fmt"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/streaming"
)

// wireScopedDelegateTool installs a DelegateTool on a delegate (child)
// engine whose manifest explicitly permits delegation, using LAZY target
// resolution.
//
// Why this exists (DELEGATION-RUNTIME-BLOCKER-2026-10-21): the child
// factory (createDelegateEngine) historically returned engines with no
// delegate tool at all, so a delegated Team-Lead could never spawn
// workers even with can_delegate: true in its manifest. Naively calling
// wireDelegateToolIfEnabled from createDelegateEngine would fix that
// but eagerly re-enter buildDelegateMaps → createDelegateEngine for
// every target, recursively preconstructing the whole delegation graph
// before any useful work runs (and diverging per branch, so the "same"
// grandchild engine would differ by parent).
//
// Lazy contract instead:
//   - The child's DelegateTool is created with an empty engines map and
//     a targetFactory closure that constructs the grandchild engine via
//     the SAME createDelegateEngine path the root tool uses, on first
//     use per target id.
//   - Targets are never constructed at wiring time; the graph expands
//     only along delegation edges actually exercised.
//   - Reconstructed grandchild engines are cached per tool instance so
//     one delegation chain reuses one engine per target.
//   - Guards (can_delegate, allowlists, cycle, depth, budget,
//     background rejection) all run inside DelegateTool.Execute /
//     resolveTargetWithOptions BEFORE the factory is consulted for any
//     spawn — the factory fires only after a target id has passed the
//     registry/allowlist gates, and never at all when those gates
//     reject.
//   - Leaf manifests (CanDelegate false) get no tool, exactly as
//     before — "leaves stay leaves".
//
// Expected:
//   - eng is the child engine just returned by createDelegateEngine.
//   - manifest is that child's manifest (the delegating agent).
//   - store is the shared coordination store for the chain.
//   - bus is the parent engine's event bus (may be nil in tests).
//
// Side effects:
//   - Appends a scoped DelegateTool to eng when manifest permits
//     delegation; no-op otherwise.
func (a *App) wireScopedDelegateTool(eng *engine.Engine, manifest agent.Manifest, store coordination.Store, bus *eventbus.EventBus) {
	if eng == nil || !manifest.Delegation.CanDelegate {
		// Leaf role: deliberately no delegate tool. The factory comment's
		// "prevents recursive delegation" intent is preserved for leaves.
		return
	}

	if a.backgroundManager == nil {
		a.backgroundManager = engine.NewBackgroundTaskManager()
		a.backgroundManager.WithSessionManager(a.sessionManager)
	}

	// One lazily-populated engines/streamers pair shared by this tool.
	// Access is serialised: DelegateTool.Execute runs on the owning
	// session's turn, and nested dispatches execute synchronously under
	// the same call stack (background delegation is rejected), so a
	// plain map with the engine's own lock is sufficient.
	lazyEngines := make(map[string]*engine.Engine)
	lazyStreamers := make(map[string]streaming.Streamer)

	targetFactory := func(agentID string) (*engine.Engine, streaming.Streamer, error) {
		if eng, ok := lazyEngines[agentID]; ok {
			return eng, lazyStreamers[agentID], nil
		}
		targetManifest, ok := a.Registry.Get(agentID)
		if !ok {
			return nil, nil, fmt.Errorf("target agent engine not available: %s", agentID)
		}
		if bus == nil && a.Engine != nil {
			bus = a.Engine.EventBus()
		}
		targetEngine, str := a.createDelegateEngine(*targetManifest, store, bus)
		// Do NOT recurse: createDelegateEngine returns a leaf-style
		// engine; if that target itself may delegate, wire ITS scoped
		// tool lazily too (one level of recursion at a time, still lazy
		// — no graph preconstruction).
		a.wireScopedDelegateTool(targetEngine, *targetManifest, store, bus)
		lazyEngines[agentID] = targetEngine
		lazyStreamers[agentID] = str
		return targetEngine, str, nil
	}

	delegateTool := engine.NewLazyDelegateTool(
		targetFactory,
		manifest.Delegation,
		manifest.ID,
		a.backgroundManager,
		a.sharedCoordinationStore(),
	)
	a.configureDelegateTool(delegateTool, eng)
	eng.AddTool(delegateTool)
}
