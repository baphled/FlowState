package engine

import (
	"context"

	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/provider"
)

// BuildContextWindowForTesting is an exported thin wrapper over the
// unexported buildContextWindow entry point so integration tests in
// sibling packages (notably internal/app compression-wiring tests) can
// exercise the assembly path without duplicating the app-level bootstrap.
//
// The "ForTesting" suffix mirrors export_test.go's BuildContextWindowForTest
// but is visible to external packages — export_test symbols only live in
// the same package's test binary, so they cannot be used from an
// internal/app test that drives the engine through the same wiring the
// live binary uses. Keep production code away from this entry point;
// Stream remains the right seam for real traffic.
//
// Expected:
//   - ctx is a valid context.
//   - sessionID and userMessage have the same contract as Stream.
//
// Returns:
//   - The assembled context window as delivered to the chat provider on
//     the next turn. Same value as the one internally threaded into
//     ChatRequest.Messages.
//
// Side effects:
//   - Invokes the full context-assembly pipeline including L1 cold-spill,
//     L2 auto-compaction when enabled, and L3 session-memory recall.
//   - Emits context-window metrics via the configured Recorder.
//   - Publishes ContextCompactedEvent on the engine bus when compaction
//     fires.
func (e *Engine) BuildContextWindowForTesting(ctx context.Context, sessionID string, userMessage string) []provider.Message {
	return e.buildContextWindow(ctx, sessionID, userMessage)
}

// MaybeAutoCompactExplicitForTesting exposes the explicit-messages
// compaction path (with an empty forceTrigger for the non-forced
// ratio tier) to the BDD harness, mirroring
// BuildContextWindowForTesting. Production code MUST NOT call this.
//
// Expected:
//   - ctx carries a live context for summariser calls.
//   - sessionID identifies a session with prior messages to compact.
//   - forceTrigger is "" for the non-forced ratio tier, or a named
//     trigger (e.g. "manual") for forced compaction.
//   - messages is the explicit message slice to evaluate.
//
// Returns:
//   - the summary text produced (or the truncation fallback), "" when
//     no compaction fired.
//
// Side effects:
//   - May mutate the session's memoised summary cache and compacted
//     context store, exactly like the production compaction path.
func (e *Engine) MaybeAutoCompactExplicitForTesting(ctx context.Context, sessionID, forceTrigger string, messages []provider.Message) string {
	return e.maybeAutoCompactExplicit(ctx, sessionID, &e.manifest, e.ModelContextLimit(), forceTrigger, messages)
}

// StopSessionSplitterForTesting flushes and shuts down the
// HotColdSplitter cached for the given sessionID so integration tests
// in sibling packages can make deterministic filesystem assertions
// against the L1 spillover directory.
//
// Production code MUST NOT call this — splitters own their own
// lifecycle tied to the engine's lifetime. The helper exists only
// because the persist worker drains asynchronously and a test that
// reads the spillover directory immediately after Build would
// otherwise race the worker goroutine.
//
// Expected:
//   - sessionID identifies a session that has previously invoked
//     buildContextWindow. If no splitter is cached, the call is a
//     no-op.
//
// Returns:
//   - true when a cached splitter was found and stopped; false when
//     the session had no splitter (micro-compaction disabled or the
//     session never built a window).
//
// Side effects:
//   - Blocks until the worker goroutine exits.
//   - Removes the splitter from the per-session cache so subsequent
//     builds for the same session would rebuild a new splitter. Tests
//     using this helper must not reuse the sessionID afterwards.
func (e *Engine) StopSessionSplitterForTesting(sessionID string) bool {
	// C2: Stop+delete under the same critical section so this helper
	// and the session.ended handler cannot interleave a mid-close
	// map read with a mid-Stop channel close. Stop is idempotent via
	// sync.Once, so the lock is about map-invariant clarity rather
	// than Stop correctness.
	e.splitterMu.Lock()
	defer e.splitterMu.Unlock()

	entry, ok := e.sessionSplitters[sessionID]
	if !ok {
		return false
	}
	delete(e.sessionSplitters, sessionID)
	entry.splitter.Stop()
	return true
}

// HookChainForTesting exposes the engine's installed hook chain so
// integration tests in sibling packages (notably internal/app delegate
// engine wiring tests) can assert hook composition without duplicating
// the buildHookChain construction. The chain itself is already a public
// type (hook.Chain) — this seam only widens the field-private
// e.hookChain to test code, mirroring BuildContextWindowForTesting and
// SessionSplitterForTesting in this same file.
//
// Production code MUST NOT depend on this accessor; the hook chain is an
// implementation detail of how Engine.Stream wraps the base provider
// handler. Tests use it to verify wiring (chain length, hook order)
// where the alternative would be exercising a full streaming round-trip
// against a mock provider — which is heavier and less precise.
//
// Expected:
//   - The receiver is a constructed *Engine. Returns nil when the engine
//     was built with neither an explicit HookChain nor a FailoverManager
//     (the resolveHookChain branch that returns nil at engine.go:485).
//
// Returns:
//   - The installed hook.Chain or nil.
//
// Side effects:
//   - None.
func (e *Engine) HookChainForTesting() *hook.Chain {
	return e.hookChain
}

// SelectHotTailBudgetedForTesting exposes the Phase 2 token-bounded
// hot tail selection (floor of recent messages grown backwards until
// the hot-tail token budget is reached) so cross-package BDD wiring
// can pin the selection contract without a provider stack. The
// budget and floor fall back to the engine's CompactionConfig unless
// test overrides were supplied at construction.
//
// Expected:
//   - messages is a non-empty live slice.
//
// Returns:
//   - The bounded tail, anchored at the newest message.
//   - errNoLiveMessages when messages is empty.
//
// Side effects: None.
func (e *Engine) SelectHotTailBudgetedForTesting(ctx context.Context, messages []provider.Message) ([]provider.Message, error) {
	return e.selectHotTailBudgetedResolving(ctx, messages)
}

// RebuildHotTailBudgetedForTesting exposes the Phase 2 budgeted
// post-compaction rebuild (system + todo + summary + token-bounded
// hot tail, with drop-oldest retry) so cross-package BDD wiring can
// pin the fits-budget and loud-failure contracts. Returns an error
// wrapping ErrCompactionInsufficient when no single-message window
// fits.
//
// Expected:
//   - messages is a non-empty live slice; summary may be "".
//
// Returns:
//   - The rebuilt, budget-fitting window.
//   - A non-nil error wrapping ErrCompactionInsufficient on terminal
//     overflow.
//
// Side effects:
//   - Logs a loud error with token counts on terminal overflow.
func (e *Engine) RebuildHotTailBudgetedForTesting(ctx context.Context, messages []provider.Message, summary string) ([]provider.Message, error) {
	return e.rebuildHotTailBudgetedResolving(ctx, messages, summary)
}

// ContextEstimateOverBudgetForTesting exposes the usable-budget
// overflow estimate so cross-package assertions can verify a rebuilt
// window genuinely fits before dispatch.
//
// Expected:
//   - ctx carries the provider/model resolution keys.
//
// Returns:
//   - true when the estimated request exceeds the usable budget.
//
// Side effects: None.
func (e *Engine) ContextEstimateOverBudgetForTesting(ctx context.Context, messages []provider.Message) bool {
	return e.contextEstimateOverBudget(ctx, messages)
}
