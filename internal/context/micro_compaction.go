// Package context — Layer 1 (L1) micro-compaction.
//
// This file implements the view-only micro-compaction layer described in
// [[Context Compression System]] and constrained by:
//
//   - ADR - Tool-Call Atomicity in Context Compaction — compaction operates on
//     *units*, not raw messages. A unit is the smallest indivisible range
//     produced by walkUnits (see compaction_units.go).
//   - ADR - View-Only Context Compaction — compaction MUST NOT mutate
//     session.Messages, MUST NOT write to ~/.flowstate/sessions/, and its
//     only permitted artefact directory (for L1) is ~/.flowstate/compacted/.
//
// T1  defines the on-disk storage schema (CompactedMessage, CompactionIndex).
// T2  defines MessageCompactor and its unit-level ShouldCompact predicate.
// T3  defines DefaultMessageCompactor.Compact and placeholder emission.
// T4  defines HotColdSplitter with async temp-then-rename spillover.
// T5  wires the splitter into WindowBuilder.appendRecentMessages.
//
// All disk I/O for L1 is async-through-channel: Split() never blocks on
// syscalls, and the persist worker uses atomic temp-then-rename writes.
package context

import (
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

// CompactedMessage is the on-disk metadata record for a single message (or
// whole unit — see CompactionIndex.UnitKind) that has been spilled to the
// cold tier. The *payload* (the original provider.Message values) is stored
// in a sibling JSON file at StoragePath; this struct is the index entry.
//
// Fields are explicit JSON-tagged to lock the on-disk format.
type CompactedMessage struct {
	// ID is the UUID of this compaction record. It is also the filename
	// stem of the payload: ~/.flowstate/compacted/{session-id}/{ID}.json.
	ID string `json:"id"`
	// OriginalTokenCount is the token count of the pre-compaction content.
	// Used by WindowBuilder to report compression savings.
	OriginalTokenCount int `json:"original_token_count"`
	// StoragePath is the absolute path to the payload JSON file on disk.
	// Tilde expansion is resolved at index load time.
	StoragePath string `json:"storage_path"`
	// Checksum is the SHA-256 hash (hex) of the payload file contents. Used
	// to detect bit-rot on rehydration (Phase 2).
	Checksum string `json:"checksum"`
	// CreatedAt timestamps the initial spill.
	CreatedAt time.Time `json:"created_at"`
	// RetrievalCount records how many times this record has been rehydrated.
	// Incremented by the rehydration path (Phase 2); zero on initial spill.
	RetrievalCount int `json:"retrieval_count"`
}

// CompactedUnit is the on-disk payload for one compactable unit. A single
// CompactedUnit contains every provider.Message in the unit (1 message for
// a solo, 2 for a single tool pair, N+1 for an N-way fan-out) so that
// rehydration replaces the whole unit atomically (per the Tool-Call
// Atomicity Invariant).
//
// Exactly one payload file exists per CompactedMessage index entry.
type CompactedUnit struct {
	// Kind records the unit's classification at spill time. Used by the
	// rehydrator to double-check the payload has not been mis-indexed.
	Kind UnitKind `json:"kind"`
	// Messages is the ordered list of provider.Message values that make up
	// the unit. JSON-roundtrip safe because every provider.Message field is
	// JSON-taggable with the zero-value semantics Go gives it.
	Messages []provider.Message `json:"messages"`
}

// CompactionIndex is the per-session index file
// (~/.flowstate/compacted/{session-id}/index.json) that catalogues every
// spilled unit. It is written atomically via temp-then-rename on every
// successful spill.
type CompactionIndex struct {
	// SessionID binds the index to a specific session. Never empty once the
	// index has been written.
	SessionID string `json:"session_id"`
	// Entries maps the CompactedMessage.ID → record. A map (not slice)
	// because lookup is keyed by id and insertion order is immaterial.
	Entries map[string]CompactedMessage `json:"entries"`
	// UpdatedAt timestamps the most recent successful index write.
	UpdatedAt time.Time `json:"updated_at"`
}

// NewCompactionIndex constructs an empty index bound to the given session.
//
// Expected:
//   - sessionID is non-empty.
//
// Returns:
//   - A CompactionIndex with no entries and UpdatedAt set to the zero value.
//     Callers MUST stamp UpdatedAt before persisting.
//
// Side effects:
//   - None.
func NewCompactionIndex(sessionID string) CompactionIndex {
	return CompactionIndex{
		SessionID: sessionID,
		Entries:   make(map[string]CompactedMessage),
	}
}
