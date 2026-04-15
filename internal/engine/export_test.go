package engine

import (
	"context"
	"time"

	ctxstore "github.com/baphled/flowstate/internal/context"
	pluginpkg "github.com/baphled/flowstate/internal/plugin"
	"github.com/baphled/flowstate/internal/provider"
)

// CollectWithProgressForTest exposes collectWithProgress for white-box testing of goroutine lifecycle.
func CollectWithProgressForTest(ctx context.Context, d *DelegateTool, chunks <-chan provider.StreamChunk, startedAt time.Time) (delegationResult, error) {
	return d.collectWithProgress(ctx, chunks, startedAt)
}

// BuildContextWindowForTest exposes buildContextWindow for white-box testing of context assembly with RecallBroker.
func (e *Engine) BuildContextWindowForTest(ctx context.Context, sessionID string, userMessage string) []provider.Message {
	return e.buildContextWindow(ctx, sessionID, userMessage)
}

// ContextAssemblyHooksForTest exposes the engine's configured context assembly hooks for white-box testing.
func (e *Engine) ContextAssemblyHooksForTest() []pluginpkg.ContextAssemblyHook {
	return e.contextAssemblyHooks
}

// LastCompactionSummaryForTest exposes the engine's most recent auto-
// compaction summary for assertion in T10 trigger tests. It is a thin
// re-export of the public LastCompactionSummary method so that test
// naming stays consistent with other For-Test helpers.
func (e *Engine) LastCompactionSummaryForTest() *ctxstore.CompactionSummary {
	return e.LastCompactionSummary()
}

// SessionSplitterForTest exposes the engine's lazily-cached per-session
// HotColdSplitter to wiring tests. Returns nil when MicroCompaction is
// disabled or the session has not built a window yet. Internal
// test-only accessor — production code MUST route splitter lookup
// through buildContextWindow.
func (e *Engine) SessionSplitterForTest(sessionID string) *ctxstore.HotColdSplitter {
	e.splitterMu.Lock()
	defer e.splitterMu.Unlock()
	return e.sessionSplitters[sessionID]
}
