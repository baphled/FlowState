//go:build e2e

// Package engine exposes e2e-only seams mirroring internal/engine/export_test.go.
//
// This file provides wrappers for features/support specs:
//   - Tool-result cap helpers
//   - The deterministic truncation-marker prefix
package engine

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

// TruncationMarkerForTest exposes the deterministic truncation-marker
// prefix so e2e specs can assert marker presence.
func TruncationMarkerForTest() string {
	return truncationDetector
}
