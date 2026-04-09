package engine

import (
	"context"
	"time"

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
