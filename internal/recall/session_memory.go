// Package recall — Layer 3 SessionMemoryStore (T13).
//
// SessionMemoryStore persists distilled knowledge entries (facts,
// conventions, preferences) extracted from conversation transcripts so
// that knowledge survives session boundaries. It deliberately lives
// alongside FileContextStore in this package so both persistence types
// share the atomic temp-then-rename pattern defined in store.go.
//
// Scope boundaries (enforced by plan T13):
//
//   - This store is NOT an extension of internal/learning/store.go.
//     Learning events (tool invocations, outcomes) and knowledge entries
//     are orthogonal concerns consumed by different pipelines; coupling
//     them would force a schema change on the learning store for a
//     feature that has nothing to do with it.
//
//   - The store emits no LLM calls. Extraction (T14) lives on a
//     separate type that wraps this store; the store itself is a pure
//     persistence layer.
package recall

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// SessionMemoryMinRelevance is the relevance floor enforced by
// Retrieve. Entries scoring below this value are excluded regardless of
// type or limit, so low-signal observations never leak into the
// assembled context window. Exposed so downstream callers that want to
// pre-filter can honour the same threshold.
const SessionMemoryMinRelevance = 0.3

// ErrSessionMemoryNotFound is returned by SessionMemoryStore.Load when
// the requested session has no persisted memory file. Exposed as a
// sentinel so callers can distinguish "no memory yet" from other I/O
// errors via errors.Is.
var ErrSessionMemoryNotFound = errors.New("recall: session memory not found")

// KnowledgeEntry is a single distilled observation drawn from a
// transcript. Type is one of "fact" (factual claim), "convention"
// (project-level norm), or "preference" (user-specific style). Content
// is the entry's free-form body and is the field used for
// deduplication. Relevance is a heuristic score in [0, 1] used by
// Retrieve for sort and filter.
type KnowledgeEntry struct {
	// ID is a stable identifier for the entry; callers may use a UUID
	// or a content hash. Empty IDs are permitted but not recommended
	// (Save will persist them; downstream consumers may need them for
	// cross-session joins).
	ID string `json:"id"`
	// Type is one of the enumerated categories above. Downstream
	// Retrieve filters on this field; unknown types are preserved on
	// load but are invisible to Retrieve queries.
	Type string `json:"type"`
	// Content is the entry body. Deduplication at AddEntry time
	// compares this field verbatim, so callers that want
	// case-insensitive or semantic deduplication should normalise
	// before adding.
	Content string `json:"content"`
	// ExtractedAt is the wall-clock time the entry was produced. Load
	// preserves this field exactly (UTC RFC3339Nano) so aging heuristics
	// can work across process boundaries.
	ExtractedAt time.Time `json:"extracted_at"`
	// Relevance is a heuristic score in [0, 1]. Retrieve sorts
	// descending by this field; values outside the range are preserved
	// verbatim.
	Relevance float64 `json:"relevance"`
}

// SessionMemoryStore persists per-session knowledge entries to
// ${storageDir}/${sessionID}/memory.json using the atomic
// temp-then-rename pattern shared with FileContextStore.
type SessionMemoryStore struct {
	storageDir string

	mu      sync.RWMutex
	entries []KnowledgeEntry
}

// persistedSessionMemory is the on-disk shape of the store. Kept
// deliberately minimal; version negotiation is not required yet because
// every field is additive and tolerant of unknowns via json.Unmarshal.
type persistedSessionMemory struct {
	Entries []KnowledgeEntry `json:"entries"`
}

// NewSessionMemoryStore constructs an empty store rooted at storageDir.
// The directory is created lazily on the first Save so that constructing
// a store for "maybe-I'll-save-something" workflows has no filesystem
// side effects.
//
// Expected:
//   - storageDir is an absolute or project-relative path that the
//     process can create.
//
// Returns:
//   - A zero-entry SessionMemoryStore. Never nil.
//
// Side effects:
//   - None at construction time.
func NewSessionMemoryStore(storageDir string) *SessionMemoryStore {
	return &SessionMemoryStore{storageDir: storageDir}
}

// AddEntry appends an entry, skipping any whose Content exactly matches
// an already-stored entry. Deduplication is by Content only because
// the extraction pipeline may assign fresh IDs on every run but the
// semantic payload is stable.
//
// Expected:
//   - entry.Content is the comparison key. Callers that want
//     case-insensitive or semantic dedup must normalise upstream.
//
// Returns:
//   - None.
//
// Side effects:
//   - Holds the store's write lock.
//   - No I/O.
func (s *SessionMemoryStore) AddEntry(entry KnowledgeEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.entries {
		if s.entries[i].Content == entry.Content {
			return
		}
	}
	s.entries = append(s.entries, entry)
}

// Entries returns a snapshot of the stored entries. The returned slice
// is a copy; mutating it does not affect the store.
//
// Expected:
//   - None.
//
// Returns:
//   - A new slice. Never nil; may have length 0.
//
// Side effects:
//   - Holds the store's read lock for the copy.
func (s *SessionMemoryStore) Entries() []KnowledgeEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]KnowledgeEntry, len(s.entries))
	copy(out, s.entries)
	return out
}

// Retrieve returns up to limit entries of the given type, sorted by
// Relevance descending. Entries with Relevance below
// SessionMemoryMinRelevance are excluded to prevent trivia from
// reaching the context window.
//
// Expected:
//   - entryType is one of the supported types ("fact", "convention",
//     "preference"). Unknown types produce an empty result without an
//     error — callers should validate types upstream if strict mode is
//     required.
//   - limit is non-negative; zero returns an empty slice.
//
// Returns:
//   - A fresh slice up to length limit. Never nil; may have length 0.
//
// Side effects:
//   - Holds the store's read lock for the snapshot.
func (s *SessionMemoryStore) Retrieve(entryType string, limit int) []KnowledgeEntry {
	if limit <= 0 {
		return []KnowledgeEntry{}
	}

	s.mu.RLock()
	candidates := make([]KnowledgeEntry, 0, len(s.entries))
	for _, e := range s.entries {
		if e.Type != entryType {
			continue
		}
		if e.Relevance < SessionMemoryMinRelevance {
			continue
		}
		candidates = append(candidates, e)
	}
	s.mu.RUnlock()

	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Relevance > candidates[j].Relevance
	})

	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates
}

// Save writes the store's current entries to
// ${storageDir}/${sessionID}/memory.json using an atomic
// temp-then-rename. The session directory is created if it does not
// already exist.
//
// Expected:
//   - sessionID is a non-empty, filesystem-safe identifier.
//
// Returns:
//   - nil on success.
//   - A wrapped error naming the failing step (mkdir, marshal, write,
//     rename) when the operation fails.
//
// Side effects:
//   - Creates ${storageDir}/${sessionID}/ when absent.
//   - Writes a tempfile and renames it into place.
func (s *SessionMemoryStore) Save(sessionID string) error {
	s.mu.RLock()
	snapshot := make([]KnowledgeEntry, len(s.entries))
	copy(snapshot, s.entries)
	s.mu.RUnlock()

	dir := filepath.Join(s.storageDir, sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("session memory: mkdir %s: %w", dir, err)
	}

	finalPath := filepath.Join(dir, "memory.json")
	tmpPath := finalPath + ".tmp"

	data, err := json.MarshalIndent(persistedSessionMemory{Entries: snapshot}, "", "  ")
	if err != nil {
		return fmt.Errorf("session memory: marshal: %w", err)
	}
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("session memory: write temp: %w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return fmt.Errorf("session memory: rename: %w", err)
	}
	return nil
}

// Load reads the persisted entries for sessionID into the store,
// replacing any in-memory state. A missing file is reported via
// ErrSessionMemoryNotFound so callers can distinguish "never saved"
// from other I/O failures.
//
// Expected:
//   - sessionID identifies a previously-saved session.
//
// Returns:
//   - nil on success.
//   - ErrSessionMemoryNotFound when no memory.json exists for the
//     session.
//   - A wrapped error for any other read or unmarshal failure.
//
// Side effects:
//   - Replaces the store's entries slice on success.
func (s *SessionMemoryStore) Load(sessionID string) error {
	path := filepath.Join(s.storageDir, sessionID, "memory.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrSessionMemoryNotFound, path)
		}
		return fmt.Errorf("session memory: read %s: %w", path, err)
	}
	var payload persistedSessionMemory
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("session memory: unmarshal %s: %w", path, err)
	}

	s.mu.Lock()
	s.entries = payload.Entries
	s.mu.Unlock()
	return nil
}
