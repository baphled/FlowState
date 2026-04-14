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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/google/uuid"
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

// SplitResult is the in-memory output of HotColdSplitter.Split. Entries are
// returned per-unit: hot units stay inline as their original messages,
// cold units appear as a single Placeholder message (the output of
// MessageCompactor.Compact). The original input slice is never mutated.
//
// HotMessages preserves the original chronological order: cold units that
// fall inside the kept window are emitted as their placeholder in the
// position they occupied; everything inside the hot tail is copied through
// verbatim.
type SplitResult struct {
	// HotMessages is the assembled, ordered slice ready to be appended to
	// the provider request window. It contains both verbatim hot messages
	// and the placeholders standing in for cold units.
	HotMessages []provider.Message
	// ColdRecords is the metadata for every cold unit spilled to disk. The
	// async persist worker uses these to write payload files.
	ColdRecords []CompactedMessage
}

// persistJob is the work item posted from Split() to the persist worker.
// It carries everything the worker needs to write the payload atomically
// without re-touching the splitter's state.
type persistJob struct {
	record  CompactedMessage
	payload CompactedUnit
}

// HotColdSplitter applies the L1 micro-compaction policy to a (copy of the)
// recent-messages slice. Per ADR - Tool-Call Atomicity in Context
// Compaction, splitting is done at *unit* boundaries (never inside a tool
// group), and per ADR - View-Only Context Compaction, the input slice is
// treated as immutable — Split() copies before transforming.
//
// All disk I/O is asynchronous: Split() never blocks on syscalls. The
// persist worker reads from a buffered channel and writes payloads using
// the temp-then-rename atomic pattern (mirroring internal/recall/store.go
// persist()).
type HotColdSplitter struct {
	compactor   MessageCompactor
	hotTailSize int
	storageDir  string
	sessionID   string

	persistCh chan persistJob
	workerWG  sync.WaitGroup
	workerCtx context.Context
	cancel    context.CancelFunc
	startOnce sync.Once
	stopOnce  sync.Once

	// nowFn is overridable for deterministic timestamps in tests.
	nowFn func() time.Time
}

// HotColdSplitterOptions configures a HotColdSplitter at construction.
type HotColdSplitterOptions struct {
	// Compactor decides which units are large enough to spill and emits
	// placeholder messages. Required.
	Compactor MessageCompactor
	// HotTailSize is the minimum number of *messages* (not units) kept
	// inline at the tail. The actual hot tail may be larger because the
	// boundary is extended outward to the nearest unit edge — a tool
	// group is never split. A non-positive value is treated as zero.
	HotTailSize int
	// StorageDir is the parent directory under which the per-session
	// spillover directory is created. Required when persistence is
	// desired; an empty StorageDir disables disk writes (Split still
	// returns placeholders, but the persist worker is a no-op).
	StorageDir string
	// SessionID is the session whose spillover directory this splitter
	// owns. Required if StorageDir is set.
	SessionID string
	// PersistChannelBuffer is the buffered channel size for async writes.
	// Defaults to 64 when zero or negative.
	PersistChannelBuffer int
	// NowFn overrides the timestamp source. Defaults to time.Now.UTC.
	NowFn func() time.Time
}

// NewHotColdSplitter constructs a splitter from the supplied options. The
// persist worker is NOT started by the constructor — call StartPersistWorker
// explicitly when the caller is ready to accept disk writes.
//
// Expected:
//   - opts.Compactor is non-nil.
//
// Returns:
//   - A configured HotColdSplitter ready to call Split() and
//     StartPersistWorker.
//   - nil when opts.Compactor is nil (mis-configuration is a programming
//     error and the caller should panic if it cannot proceed).
//
// Side effects:
//   - None. No goroutines are spawned and no directories are created
//     until StartPersistWorker runs.
func NewHotColdSplitter(opts HotColdSplitterOptions) *HotColdSplitter {
	if opts.Compactor == nil {
		return nil
	}
	buf := opts.PersistChannelBuffer
	if buf <= 0 {
		buf = 64
	}
	now := opts.NowFn
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &HotColdSplitter{
		compactor:   opts.Compactor,
		hotTailSize: max(opts.HotTailSize, 0),
		storageDir:  opts.StorageDir,
		sessionID:   opts.SessionID,
		persistCh:   make(chan persistJob, buf),
		nowFn:       now,
	}
}

// StartPersistWorker spawns the goroutine that drains persistCh and writes
// payloads to disk under storageDir/sessionID/. Subsequent calls are
// no-ops. Use Stop() to flush and shut down cleanly.
//
// Expected:
//   - parentCtx may be background or a request-scoped context. Cancellation
//     stops the worker after draining whatever is buffered.
//
// Returns:
//   - None.
//
// Side effects:
//   - Spawns one goroutine. Creates storageDir/sessionID/ on demand.
func (s *HotColdSplitter) StartPersistWorker(parentCtx context.Context) {
	s.startOnce.Do(func() {
		//nolint:gosec // cancel is invoked by Stop() which is the pair operation.
		s.workerCtx, s.cancel = context.WithCancel(parentCtx)
		s.workerWG.Add(1)
		go s.runWorker()
	})
}

// Stop signals the worker to drain its buffer and return. Safe to call
// multiple times. Blocks until the worker goroutine has exited.
//
// Expected:
//   - StartPersistWorker has been called previously.
//
// Returns:
//   - None.
//
// Side effects:
//   - Closes the persist channel and joins the worker goroutine.
func (s *HotColdSplitter) Stop() {
	s.stopOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		close(s.persistCh)
		s.workerWG.Wait()
	})
}

// Split partitions msgs into a hot-tail window (kept inline) plus a cold
// prefix (replaced by per-unit placeholders) and posts spillover jobs to
// the persist worker. The returned SplitResult is fully prepared in
// memory; no syscalls are issued from this call.
//
// The hot/cold boundary is computed in two stages:
//  1. The naive index hotStart = max(0, len(msgs)-hotTailSize) marks the
//     tail floor.
//  2. The walker (compaction_units.go) identifies unit boundaries; the
//     boundary is extended *outward* (toward index 0) so it never falls
//     inside a tool group.
//
// Cold units that exceed the compactor's threshold become placeholders;
// cold units below threshold pass through as their original messages
// (preserves correctness when only some old units are large).
//
// Expected:
//   - msgs is the recent-message slice supplied by the caller. Treated as
//     immutable: every element is copied (value-equal) into the result;
//     the caller's slice header and elements remain identity-equal after
//     return.
//
// Returns:
//   - SplitResult with HotMessages (verbatim + placeholders, in original
//     order) and ColdRecords (one per spilled unit).
//
// Side effects:
//   - Posts up to one persistJob per cold-and-spilled unit to persistCh.
//     Non-blocking semantics: when the channel is full, the job is
//     dropped and the placeholder still emitted (the caller will recompute
//     on the next pass; durability of L1 is best-effort by design).
func (s *HotColdSplitter) Split(msgs []provider.Message) SplitResult {
	if len(msgs) == 0 {
		return SplitResult{}
	}

	// View-only invariant: copy before we even consider transforming.
	work := make([]provider.Message, len(msgs))
	copy(work, msgs)

	units := walkUnits(work)
	if units == nil {
		// Malformed input — refuse to compact, return everything verbatim.
		return SplitResult{HotMessages: work}
	}

	naiveHotStart := max(len(work)-s.hotTailSize, 0)
	coldUnitEnd := s.findColdBoundary(units, naiveHotStart)

	out := SplitResult{
		HotMessages: make([]provider.Message, 0, len(work)),
		ColdRecords: make([]CompactedMessage, 0, coldUnitEnd),
	}

	// Cold prefix: spill (or pass-through) per unit.
	for i := range coldUnitEnd {
		u := units[i]
		if !s.compactor.ShouldCompact(u, work) {
			out.HotMessages = append(out.HotMessages, work[u.Start:u.End]...)
			continue
		}
		record, placeholder := s.spillUnit(u, work)
		out.ColdRecords = append(out.ColdRecords, record)
		out.HotMessages = append(out.HotMessages, placeholder)
	}
	// Hot tail: verbatim through.
	for i := coldUnitEnd; i < len(units); i++ {
		u := units[i]
		out.HotMessages = append(out.HotMessages, work[u.Start:u.End]...)
	}

	return out
}

// findColdBoundary returns the index in units up to which (exclusive)
// units are considered cold. The boundary is rounded down to the nearest
// unit edge so a tool group is never split.
//
// Expected:
//   - units is the walker output (non-nil, may be empty).
//   - naiveHotStart is the message-level boundary to round.
//
// Returns:
//   - The unit index i such that units[0..i) are cold and units[i..]
//     are hot. Returns 0 when the entire input fits inside the hot tail.
//
// Side effects:
//   - None.
func (s *HotColdSplitter) findColdBoundary(units []Unit, naiveHotStart int) int {
	for i, u := range units {
		// First unit whose Start is at or beyond the naive hot floor
		// becomes the first hot unit; everything before is cold.
		if u.Start >= naiveHotStart {
			return i
		}
	}
	return len(units)
}

// spillUnit constructs the CompactedMessage record + placeholder for one
// cold unit and posts the persistJob to the worker channel.
//
// Expected:
//   - unit indexes a valid range of work.
//   - work is the splitter's local copy of the input slice (never mutated).
//
// Returns:
//   - The CompactedMessage record (suitable for SplitResult.ColdRecords).
//   - The placeholder provider.Message returned by Compact.
//
// Side effects:
//   - Posts a persistJob to persistCh non-blockingly (drops on full).
func (s *HotColdSplitter) spillUnit(unit Unit, work []provider.Message) (CompactedMessage, provider.Message) {
	id := uuid.NewString()
	payload := CompactedUnit{
		Kind:     unit.Kind,
		Messages: append([]provider.Message(nil), work[unit.Start:unit.End]...),
	}

	storagePath := ""
	if s.storageDir != "" && s.sessionID != "" {
		storagePath = filepath.Join(s.storageDir, s.sessionID, id+".json")
	}

	record := CompactedMessage{
		ID:                 id,
		OriginalTokenCount: s.compactor.UnitTokenCount(unit, work),
		StoragePath:        storagePath,
		Checksum:           checksumPayload(payload),
		CreatedAt:          s.nowFn(),
	}

	placeholder := s.compactor.Compact(unit, work, id)

	if storagePath != "" {
		select {
		case s.persistCh <- persistJob{record: record, payload: payload}:
		default:
			// Channel full: drop. Placeholder is still emitted; the
			// next pass will recompute and re-attempt persistence.
		}
	}

	return record, placeholder
}

// runWorker is the body of the persist goroutine.
//
// Expected:
//   - s.persistCh is open; Stop() closes it to signal shutdown.
//
// Returns:
//   - None.
//
// Side effects:
//   - Consumes jobs from s.persistCh until the channel closes.
//   - Writes payload files via writeJob. Logs to stderr on failure.
//   - Signals s.workerWG on exit.
func (s *HotColdSplitter) runWorker() {
	defer s.workerWG.Done()
	for job := range s.persistCh {
		// Honour cancellation but still drain pending jobs already on the
		// channel — we want at-least-best-effort durability.
		if err := writeJob(job); err != nil {
			// Failure here is logged-and-continue: L1 durability is
			// best-effort. The placeholder is already in the caller's
			// view; re-computation on the next turn will retry.
			fmt.Fprintf(os.Stderr, "[micro_compaction] persist failed for %s: %v\n", job.record.ID, err)
		}
	}
}

// writeJob persists one payload using temp-then-rename, mirroring the
// pattern used by internal/recall/store.go persist().
//
// Expected:
//   - job.record.StoragePath is non-empty and ends in .json.
//
// Returns:
//   - nil on successful atomic rename.
//   - A wrapped error when the parent directory cannot be created, the
//     temp file cannot be written, or the rename fails.
//
// Side effects:
//   - Creates the parent directory (mode 0o700) if missing.
//   - Writes job.record.StoragePath atomically.
func writeJob(job persistJob) error {
	dir := filepath.Dir(job.record.StoragePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating spill dir: %w", err)
	}
	data, err := json.MarshalIndent(job.payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling payload: %w", err)
	}
	tmp := job.record.StoragePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := os.Rename(tmp, job.record.StoragePath); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}
	return nil
}

// checksumPayload returns the sha-256 hex digest of the marshalled
// payload. Used to detect bit-rot on rehydration (Phase 2).
//
// Expected:
//   - payload is the CompactedUnit about to be persisted.
//
// Returns:
//   - Hex-encoded sha-256 of the marshalled payload, or empty string if
//     marshalling fails (the persist path will then fail loudly with the
//     real marshal error so absence of a checksum is not silent).
//
// Side effects:
//   - None.
func checksumPayload(payload CompactedUnit) string {
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
