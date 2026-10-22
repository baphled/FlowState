// Package learning point_id.go provides a shared helper for converting
// arbitrary FlowState source identifiers (session IDs, timestamps,
// relation strings) into valid Qdrant point IDs.
//
// Qdrant only accepts an unsigned integer or a UUID as a point ID and
// rejects anything else with HTTP 400. To keep a single contract across
// every write caller (Mem0LearningStore, VectorStoreMemoryClient), we
// derive a deterministic UUIDv5 from the caller-supplied source string.
// The original source string is preserved in the point payload as
// "source_id" so it remains queryable.
package learning

import "github.com/google/uuid"

// pointIDNamespace is the fixed UUID namespace used to derive FlowState
// Qdrant point IDs via UUIDv5. Changing this value would re-hash every
// FlowState-generated point ID and break idempotent re-indexing of
// previously-written content, so it must remain stable.
//
// This namespace is used ONLY for FlowState-generated vector point IDs
// and is not an externally-registered namespace.
var pointIDNamespace = uuid.MustParse("f107a5ea-2024-4f10-b5ea-f107a5ea2024")

// PointIDFromSource converts an arbitrary source identifier into a
// UUIDv5 string suitable for use as a Qdrant point ID.
//
// Expected:
//   - source is any string identifier produced by FlowState (e.g.
//     "session-1776075781962028658", a numeric timestamp, or a
//     composed relation string).
//
// Returns:
//   - A 36-character UUIDv5 string derived deterministically from
//     pointIDNamespace and source. The same source always produces
//     the same UUID.
//
// Side effects:
//   - None.
func PointIDFromSource(source string) string {
	return uuid.NewSHA1(pointIDNamespace, []byte(source)).String()
}
