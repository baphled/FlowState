package engine

import (
	"context"

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
	e.splitterMu.Lock()
	splitter, ok := e.sessionSplitters[sessionID]
	if ok {
		delete(e.sessionSplitters, sessionID)
	}
	e.splitterMu.Unlock()
	if !ok {
		return false
	}
	splitter.Stop()
	return true
}
