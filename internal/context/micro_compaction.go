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
	"fmt"
	"strings"
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

// MessageCompactor decides whether a compactable unit (per ADR - Tool-Call
// Atomicity in Context Compaction) is large enough to be replaced by a
// placeholder, and counts tokens for comparison against the threshold.
//
// Implementations operate at the *unit* level — never per-message — so that
// a parallel fan-out group is either kept or compacted as a whole. This is
// a deliberate override of the original plan text (which spoke of
// per-message compaction) per the team-lead's brief.
type MessageCompactor interface {
	// ShouldCompact reports whether the unit at unit.Start..unit.End in msgs
	// has accumulated enough tokens to warrant compaction. Implementations
	// must treat msgs as immutable.
	ShouldCompact(unit Unit, msgs []provider.Message) bool
	// TokenCount returns the cheap-approximation token count for a single
	// provider.Message. Used by ShouldCompact and by the splitter when
	// recording OriginalTokenCount.
	TokenCount(msg provider.Message) int
	// UnitTokenCount returns the cheap-approximation token count for an
	// entire unit (sum of TokenCount over its messages).
	UnitTokenCount(unit Unit, msgs []provider.Message) int
	// Compact builds the placeholder message that replaces the whole unit.
	// The returned message is a single solo-class message (per the ADR);
	// any tool_use / tool_result payloads are dropped together.
	Compact(unit Unit, msgs []provider.Message, recordID string) provider.Message
}

// DefaultMessageCompactor is the production implementation of
// MessageCompactor. Token counts come from a cheap whitespace-split
// approximation, and ShouldCompact fires when the unit's total exceeds
// the configured threshold (strictly greater than, per the existing T2
// acceptance test).
type DefaultMessageCompactor struct {
	threshold int
}

// NewDefaultMessageCompactor constructs a DefaultMessageCompactor with the
// given token threshold.
//
// Expected:
//   - threshold is the strict-greater-than firing point (e.g. 1000 means
//     a unit totalling 1001 tokens triggers compaction; 1000 does not).
//     A non-positive threshold disables compaction (ShouldCompact always
//     returns false).
//
// Returns:
//   - A configured DefaultMessageCompactor.
//
// Side effects:
//   - None.
func NewDefaultMessageCompactor(threshold int) *DefaultMessageCompactor {
	return &DefaultMessageCompactor{threshold: threshold}
}

// ShouldCompact reports whether the unit's total token count strictly
// exceeds the configured threshold.
//
// Expected:
//   - unit.Start and unit.End are valid half-open bounds into msgs.
//   - msgs is the slice the unit indexes into. Treated as immutable.
//
// Returns:
//   - true when the unit totals strictly more than threshold tokens.
//   - false when the unit fits or threshold is non-positive.
//
// Side effects:
//   - None.
func (c *DefaultMessageCompactor) ShouldCompact(unit Unit, msgs []provider.Message) bool {
	if c.threshold <= 0 {
		return false
	}
	return c.UnitTokenCount(unit, msgs) > c.threshold
}

// TokenCount returns the whitespace-split token count for the message body.
// Tool-call argument payloads and tool-result content are both counted via
// the message Content field; ToolCalls metadata is not separately scored.
//
// Expected:
//   - msg may be any provider.Message; nil-equivalent (zero) is treated as
//     zero tokens.
//
// Returns:
//   - The whitespace-field count of msg.Content.
//
// Side effects:
//   - None.
func (c *DefaultMessageCompactor) TokenCount(msg provider.Message) int {
	if msg.Content == "" {
		return 0
	}
	return len(strings.Fields(msg.Content))
}

// UnitTokenCount sums TokenCount over every message in the unit.
//
// Expected:
//   - unit.Start and unit.End are valid half-open bounds into msgs.
//   - msgs is the slice the unit indexes into.
//
// Returns:
//   - The sum of TokenCount(msgs[i]) for i in [unit.Start, unit.End).
//
// Side effects:
//   - None.
func (c *DefaultMessageCompactor) UnitTokenCount(unit Unit, msgs []provider.Message) int {
	total := 0
	for i := unit.Start; i < unit.End; i++ {
		total += c.TokenCount(msgs[i])
	}
	return total
}

// Compact builds the placeholder that replaces the whole unit. The
// placeholder is a Role:"user" message (a solo unit per the ADR — never a
// tool message and carrying no tool_call_id). It records the record id and
// the message count for inline diagnostics; the actual content lives in
// the spilled CompactedUnit payload at StoragePath.
//
// Expected:
//   - unit.Start and unit.End bound the unit in the original message slice.
//   - The original message slice is the splitter's input; it is not read
//     here (the placeholder text is derived from unit indices and recordID
//     alone) but the parameter is retained for forward compatibility with
//     richer placeholder strategies (e.g. summarised previews).
//   - recordID is the CompactedMessage.ID used for spillover lookup.
//
// Returns:
//   - A single provider.Message with Role:"user", Content describing the
//     elision, and an empty ToolCalls slice. tool_use and tool_result
//     entries from the original unit are dropped together.
//
// Side effects:
//   - None.
func (c *DefaultMessageCompactor) Compact(unit Unit, _ []provider.Message, recordID string) provider.Message {
	count := unit.End - unit.Start
	noun := "message"
	if count != 1 {
		noun = "messages"
	}
	return provider.Message{
		Role:    "user",
		Content: fmt.Sprintf("[compacted: %s — %d %s elided]", recordID, count, noun),
	}
}
