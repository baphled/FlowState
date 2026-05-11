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

// EmitMidToolLoopRefreshForTest exposes the Phase-5 Slice γ post-tool-
// batch hook so the gate-proximity test suite can pin its two
// affordances (context_usage emission + tool_result_wave force-fire)
// without standing up a full multi-batch tool-loop end-to-end.
//
// Discards the compacted-bool return so the original cadence specs
// keep their `(ctx, sessionID, outChan)` call shape. Bug #35's new
// specs use EmitMidToolLoopRefreshReportForTest to read the signal.
func (e *Engine) EmitMidToolLoopRefreshForTest(ctx context.Context, sessionID string, outChan chan<- provider.StreamChunk) {
	_ = e.emitMidToolLoopRefresh(ctx, sessionID, outChan)
}

// EmitMidToolLoopRefreshReportForTest exposes the same hook with the
// compacted-bool return surfaced. Bug #35's reload-signal specs use
// this variant to pin that the helper reports compaction status so
// the streamWithToolLoop caller can decide whether to rebuild
// messages from the post-compaction view.
func (e *Engine) EmitMidToolLoopRefreshReportForTest(ctx context.Context, sessionID string, outChan chan<- provider.StreamChunk) bool {
	return e.emitMidToolLoopRefresh(ctx, sessionID, outChan)
}

// RebuildMessagesAfterCompactionForTest exposes the Bug #35
// post-compaction message-reload helper so specs can pin the
// post-fix invariant — the rebuilt slice carries the auto-compacted
// summary marker — without driving a full multi-batch tool loop
// end-to-end.
func (e *Engine) RebuildMessagesAfterCompactionForTest(ctx context.Context, sessionID string) []provider.Message {
	return e.rebuildContextWindowAfterMidLoopCompaction(ctx, sessionID)
}

// EmitPostRetryContextUsageForTest exposes the Bug #36 post-retry
// context_usage emission so specs can pin the cadence (one fresh
// chunk per real change) and the per-session double-emission guard
// (identical payload coalesces). The messages slice defaults to the
// engine's persisted store; tests asserting payload-byte equality
// pass the same slice the production callsite would.
func (e *Engine) EmitPostRetryContextUsageForTest(ctx context.Context, sessionID string, outChan chan<- provider.StreamChunk) {
	var msgs []provider.Message
	if e.store != nil {
		msgs = e.store.AllMessages()
	}
	e.emitPostRetryContextUsage(ctx, sessionID, msgs, outChan)
}

// SetHeartbeatIntervalForTest overrides the default 15s streaming
// heartbeat cadence so specs can drive the ticker at sub-second
// intervals without sleeping. Setting zero disables emission entirely
// (matching the hibernate semantics of the production gate).
func (e *Engine) SetHeartbeatIntervalForTest(d time.Duration) {
	e.heartbeatInterval = d
}

// PublishStreamingHeartbeatForTest exposes the heartbeat publish helper
// so specs can pin the bus payload shape without standing up a full
// Stream goroutine + ticker. Mirrors the production publishStreamingHeartbeat
// callsite with a fixed Phase argument.
func (e *Engine) PublishStreamingHeartbeatForTest(sessionID, agentID, phase string) {
	e.publishStreamingHeartbeat(sessionID, agentID, phase)
}

// AppendToolResultsBatchToMessagesForTest exposes appendToolResultsBatchToMessages
// for the context-anchoring spec. The fix injects a system-role re-anchor
// reminder after non-trivial tool-result batches so the model does not drift
// onto trailing tool output and lose the user's original prompt.
func (e *Engine) AppendToolResultsBatchToMessagesForTest(
	messages []provider.Message, toolCalls []*provider.ToolCall, results []tool.Result,
) []provider.Message {
	return e.appendToolResultsBatchToMessages(messages, toolCalls, results)
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

// SetRePromptTimeout overrides the per-re-prompt deadline used by the
// orchestrator's triggerRePrompt. Tests use this to drive H4-style hung-
// provider scenarios without waiting on the production-grade timeout.
func SetRePromptTimeout(o *CompletionOrchestrator, d time.Duration) {
	o.rePromptTimeout = d
}

// SetRePromptConcurrency overrides the bound on concurrent re-prompts. Tests
// use this to validate semaphore behaviour without waiting on production
// settings.
func SetRePromptConcurrency(o *CompletionOrchestrator, n int) {
	o.rePromptSem = make(chan struct{}, n)
}
