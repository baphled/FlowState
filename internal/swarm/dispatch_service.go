package swarm

import (
	"context"
	"fmt"

	"github.com/baphled/flowstate/internal/streaming"
)

// DispatchEngine is the narrow surface DispatchSwarm needs. *engine.Engine
// already implements this — we declare it locally rather than importing
// the engine package to avoid the cycle that would otherwise form
// (engine already imports swarm).
//
// ManifestSnapshot returns the engine's current agent manifest as an
// opaque value the caller can pass to RestoreManifest after the
// dispatch finishes. The shape is intentionally opaque to avoid
// dragging the agent.Manifest type into this package's import graph;
// implementations round-trip a deep copy of the manifest internally.
type DispatchEngine interface {
	SetSwarmContext(*Context)
	FlushSwarmLifecycle(ctx context.Context) error
	ManifestSnapshot() any
	RestoreManifest(snapshot any)
}

// DispatchSwarm runs an end-to-end swarm dispatch: install the swarm
// context, stream from the lead via the provided streamer + consumer,
// flush the post-swarm gate lifecycle. Returns the first error it hits;
// FlushSwarmLifecycle still runs after a streaming error so the post
// gates can record cleanup state, but the streaming error is what the
// caller sees.
//
// This is the shared service both CLI (`flowstate run --agent <swarm>`)
// and TUI chat (`@<swarm-id> <scope>` in the chat input) call. Keeping
// the dispatch shape in one place is the explicit contract from
// `ADR - Swarm Dispatch Across Access Methods`: access methods are
// thin wrappers around a single service, never re-implementations.
//
// Expected:
//   - ctx is a valid context controlling the streamed run.
//   - eng may be nil — preserves the CLI's historical "bare engine"
//     test contract where SetSwarmContext / FlushSwarmLifecycle are
//     skipped silently. Production wiring always passes a non-nil
//     engine; tests sometimes drive streaming without one.
//   - swarmCtx may be nil; nil installs an empty envelope which the
//     engine treats as "single-agent shape", consistent with how the
//     CLI's resolveAgentOrSwarm hands back nil for non-swarm targets.
//   - streamer drives provider streaming; consumer collects chunks.
//   - leadID is the agent that receives the message — for swarm
//     dispatches this is swarmCtx.LeadAgent; for an agent target the
//     CLI passes the agent id directly and swarmCtx is nil.
//   - message is the user prompt / scope text.
//
// Returns:
//   - nil when streaming and post-flush both succeed.
//   - The streaming error wrapped with context when streaming fails.
//     FlushSwarmLifecycle still runs first so gate side-effects record;
//     the flush error is suppressed in that path because callers cannot
//     act on two errors at once.
//   - The flush error when streaming succeeded but a post-swarm gate
//     reported a failure.
//
// Side effects:
//   - Calls eng.SetSwarmContext, eng.FlushSwarmLifecycle when eng != nil.
//   - Drives the streamer + consumer to completion.
func DispatchSwarm(
	ctx context.Context,
	eng DispatchEngine,
	swarmCtx *Context,
	streamer streaming.Streamer,
	consumer streaming.StreamConsumer,
	leadID, message string,
) error {
	// Snapshot the engine's pre-dispatch manifest so we can restore it
	// after the stream completes. Engine.Stream auto-swaps the active
	// manifest to whatever agent_id the caller passes (a long-standing
	// engine behaviour that supports `flowstate run --agent <id>`),
	// which means a swarm dispatch leaves the engine permanently
	// re-identified as the swarm's lead. For one-shot CLI runs this
	// is harmless because the process exits; for the TUI's continuing
	// chat session it leaks the lead's identity into every subsequent
	// turn. CLI/TUI parity (per ADR - Swarm Dispatch Across Access
	// Methods + the user's "CLI and TUI should act the same"
	// directive) requires both surfaces to revert the engine to its
	// pre-dispatch manifest after the flush — restore is a no-op in
	// the CLI process-exit case but load-bearing for the TUI.
	var preDispatch any
	if eng != nil {
		preDispatch = eng.ManifestSnapshot()
		eng.SetSwarmContext(swarmCtx)
	}

	streamErr := streaming.Run(ctx, streamer, consumer, leadID, message)

	var flushErr error
	if eng != nil {
		flushErr = eng.FlushSwarmLifecycle(ctx)
		// Restore the engine's pre-dispatch manifest. We deliberately
		// leave the swarm context in place: clearing it would surface
		// as a state regression in tests pinning swarm-context
		// installation, and a stale context is harmless once the
		// manifest is restored — appendSwarmLeadSection compares
		// e.manifest.ID against swarmCtx.LeadAgent, so a manifest
		// reverted to the pre-dispatch agent makes the lead-section
		// emitter inert. The next dispatch overwrites the context.
		eng.RestoreManifest(preDispatch)
	}

	if streamErr != nil {
		return fmt.Errorf("streaming response: %w", streamErr)
	}
	return flushErr
}
