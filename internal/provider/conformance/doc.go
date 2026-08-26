// Package conformance provides cross-provider streaming conformance tests
// that verify each provider.Provider implementation emits StreamChunk values
// matching the contract the engine depends on (tool-call shape, EventType
// tagging, error classification, and done sentinel).
package conformance
