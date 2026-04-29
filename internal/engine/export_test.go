package engine

import (
	"context"
	"time"

	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/delegation"
	pluginpkg "github.com/baphled/flowstate/internal/plugin"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
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

// SessionCompressionMetricsForTest is a thin re-export of the public
// SessionCompressionMetrics method, matching the naming convention of
// the other For-Test helpers in this file. Tests read the per-session
// compression ledger through this to keep the assertion surface
// consistent.
func (e *Engine) SessionCompressionMetricsForTest(sessionID string) ctxstore.CompressionMetrics {
	return e.SessionCompressionMetrics(sessionID)
}

// SessionSplitterForTest exposes the engine's lazily-cached per-session
// HotColdSplitter to wiring tests. Returns nil when MicroCompaction is
// disabled or the session has not built a window yet. Internal
// test-only accessor — production code MUST route splitter lookup
// through buildContextWindow.
func (e *Engine) SessionSplitterForTest(sessionID string) *ctxstore.HotColdSplitter {
	e.splitterMu.Lock()
	defer e.splitterMu.Unlock()
	entry, ok := e.sessionSplitters[sessionID]
	if !ok {
		return nil
	}
	return entry.splitter
}

// PrimeSessionSplitterForTest exposes ensureSessionSplitter so the
// Item 4 sweeper tests can lazily create a cached splitter without
// having to assemble a full context window. Production code never
// calls this; ensureSessionSplitter is invoked from
// buildContextWindow on the hot path.
func (e *Engine) PrimeSessionSplitterForTest(sessionID string) *ctxstore.HotColdSplitter {
	return e.ensureSessionSplitter(context.Background(), sessionID)
}

// IsIdleSweeperRunningForTest reports whether the Item 4 idle-TTL
// sweeper goroutine is active. Returns false before Engine.New has
// started it (micro-compaction disabled) and after Engine.Shutdown
// has signalled it to stop.
func (e *Engine) IsIdleSweeperRunningForTest() bool {
	e.splitterMu.Lock()
	defer e.splitterMu.Unlock()
	return e.sweeperStop != nil
}

// SanitiseDelegationMessageForTest exposes sanitiseDelegationMessage for white-box testing.
func SanitiseDelegationMessageForTest(msg string) string {
	return sanitiseDelegationMessage(msg)
}

// CheckSpawnLimitsForTest exposes checkSpawnLimits so the depth-
// resolution test can assert the manifest-aware ceiling without
// driving Execute end-to-end.
func (d *DelegateTool) CheckSpawnLimitsForTest(handoff *delegation.Handoff) error {
	return d.checkSpawnLimits(handoff)
}

// DispatchPostMemberGatesForTest exposes dispatchPostMemberGates so
// the ext-gate routing test can confirm the kind:ext:<name> path
// reaches the registered ExtGateFunc.
func (d *DelegateTool) DispatchPostMemberGatesForTest(ctx context.Context, memberID string) error {
	return d.dispatchPostMemberGates(ctx, memberID)
}

// ExecuteToolCallForTest exposes executeToolCall so the gate-error
// halt test can pin the contract that *swarm.GateError surfaces as
// a hard outer error (terminating the stream) while non-gate tool
// errors stay attached to result.Error (soft fail, re-enters the
// agent's tool loop).
func (e *Engine) ExecuteToolCallForTest(ctx context.Context, sessionID string, toolCall *provider.ToolCall) (tool.Result, error) {
	return e.executeToolCall(ctx, sessionID, toolCall)
}

// SetEnginesForTest installs the engines map on a DelegateTool after
// construction so depth-resolution tests can swap in a lead engine
// carrying the swarm context without rebuilding the tool.
func (d *DelegateTool) SetEnginesForTest(engines map[string]*Engine) {
	d.engines = engines
}

// ResolveTargetWithOptionsForTest exposes resolveTargetWithOptions for white-box testing of delegation guards.
func (d *DelegateTool) ResolveTargetWithOptionsForTest(ctx context.Context, params DelegationParamsForTest) (string, error) {
	p := delegationParams{
		category:     params.Category,
		subagentType: params.SubagentType,
		message:      params.Message,
		handoff:      params.Handoff,
	}
	target, err := d.resolveTargetWithOptions(ctx, p)
	if err != nil {
		return "", err
	}
	return target.agentID, nil
}

// DelegationParamsForTest exposes delegationParams fields for external test packages.
type DelegationParamsForTest struct {
	Category     string
	SubagentType string
	Message      string
	Handoff      *delegation.Handoff
}
