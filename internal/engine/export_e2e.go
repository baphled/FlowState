//go:build e2e

// Package engine exposes e2e-only seams mirroring internal/engine/export_test.go.
//
// This file provides wrappers for features/support specs:
//   - Tool-result cap helpers
//   - The deterministic truncation-marker prefix
package engine

import (
	"context"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

// CapPersistedToolResultForTest exposes capPersistedToolResult so e2e
// persistence-cap specs can pin the truncation contract.
func CapPersistedToolResultForTest(content string) string {
	return capPersistedToolResult(content)
}

// CapInFlightToolResultForTest exposes capInFlightToolResult so e2e
// in-flight cap specs can pin the truncation contract.
func CapInFlightToolResultForTest(content string) string {
	return capInFlightToolResult(content)
}

// SetMaxIdenticalToolCallsForTestE2E exposes the repeat-detection override
// so e2e loop-guard specs can isolate the cumulative rejection guard.
func (e *Engine) SetMaxIdenticalToolCallsForTestE2E(n int) {
	e.maxIdenticalToolCalls = n
}

// SetMaxToolLoopIterationsForTestE2E exposes the iteration-budget override
// so e2e loop-guard specs can isolate the cumulative rejection guard.
func (e *Engine) SetMaxToolLoopIterationsForTestE2E(n int) {
	e.maxToolLoopIterations = n
}

// SetMaxToolLoopDurationForTestE2E exposes the duration-budget override so
// e2e loop-guard specs can isolate the cumulative rejection guard.
func (e *Engine) SetMaxToolLoopDurationForTestE2E(d time.Duration) {
	e.maxToolLoopDuration = d
}

// SetMaxSameToolPatternCallsForTestE2E exposes the same-tool-pattern
// override so e2e loop-guard specs can isolate the cumulative rejection
// guard.
func (e *Engine) SetMaxSameToolPatternCallsForTestE2E(n int) {
	e.maxSameToolPatternCalls = n
}

// TruncationMarkerForTest exposes the deterministic truncation-marker
// prefix so e2e specs can assert marker presence.
func TruncationMarkerForTest() string {
	return truncationDetector
}

// OverflowRecoveryCompactionsForTestingE2E exposes the per-session
// Stream-level overflow-recovery compaction counter so the ctx-loss
// BDD scenarios can pin the exactly-once compact-and-retry contract
// (child session refused by the context gate compacts once before the
// gate-skipped retry).
//
// Expected: sessionID identifies the session.
// Returns: The overflow-recovery compaction count.
// Side effects: None.
func (e *Engine) OverflowRecoveryCompactionsForTestingE2E(sessionID string) int {
	e.buildStateMu.Lock()
	defer e.buildStateMu.Unlock()
	return e.sessionOverflowRecoveryCompactions[sessionID]
}

// CheckContextWindowOverflowForTestingE2E exposes the pre-flight gate
// verdict so scenarios can seed a transcript that provably overflows
// the LOCAL gate (the gate-refusal seam the ctx-loss bug exercised).
//
// Expected: req carries the assembled request.
// Returns: A non-nil *provider.Error when the gate refuses the request.
// Side effects: None.
func (e *Engine) CheckContextWindowOverflowForTestingE2E(req *provider.ChatRequest) *provider.Error {
	return e.checkContextWindowOverflow(req)
}

// CompactSessionForOverflowRecoveryForTestingE2E exposes the
// Stream-level overflow-recovery compaction so the storm-cap scenario
// can assert compaction fires for the child session.
//
// Expected: ctx carries provider/model resolution; sessionID is the
// child session; req carries the overflowing messages.
// Returns: None.
// Side effects: Same as compactSessionForOverflowRecovery.
func (e *Engine) CompactSessionForOverflowRecoveryForTestingE2E(ctx context.Context, sessionID string, req *provider.ChatRequest) {
	e.compactSessionForOverflowRecovery(ctx, sessionID, req)
}

// ErrCompactionInsufficientForE2E exposes the terminal compaction
// error text so e2e step definitions can match the surfaced chunk.
func ErrCompactionInsufficientForE2E() error {
	return ErrCompactionInsufficient
}

// ToolLoopRetryStormThresholdForE2E exposes the tool_loop_retry storm
// compaction threshold helper (fix 3, ctx-loss) so e2e scenarios can
// drive the mid-loop compaction seam deterministically.
func ToolLoopRetryStormThresholdForE2E() ToolLoopRetryStormThreshold {
	return ToolLoopRetryStormThreshold{threshold: toolLoopRetryStormThreshold}
}
