//go:build e2e

// Package engine exposes e2e-only seams mirroring internal/engine/export_test.go.
//
// This file provides wrappers for features/support specs:
//   - Tool-result cap helpers
//   - The deterministic truncation-marker prefix
package engine

import "time"

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
