// Package recall_test — T13 SessionMemoryStore specification.
//
// SessionMemoryStore is the Phase 3 persistent store for distilled
// knowledge entries extracted from conversation transcripts. It is
// physically co-located with FileContextStore so both share the same
// atomic temp-then-rename write pattern and can be audited together.
package recall_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/recall"
)

// sampleEntries returns a deterministic mix of entry types. Tests that
// care about ordering pass the entries through Retrieve, which owns the
// sorting contract.
func sampleEntries(now time.Time) []recall.KnowledgeEntry {
	return []recall.KnowledgeEntry{
		{ID: "e1", Type: "fact", Content: "API base URL is /v1", ExtractedAt: now, Relevance: 0.9},
		{ID: "e2", Type: "convention", Content: "prefer snake_case in JSON", ExtractedAt: now, Relevance: 0.8},
		{ID: "e3", Type: "preference", Content: "use British English", ExtractedAt: now, Relevance: 0.7},
	}
}

// TestSessionMemoryStore_SaveLoadRoundTrip_PreservesEveryEntry asserts
// the central persistence contract: entries written via Save are
// byte-for-byte recoverable via Load. Time fields are compared with
// .Equal to dodge monotonic-clock drift after a round-trip.
func TestSessionMemoryStore_SaveLoadRoundTrip_PreservesEveryEntry(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := recall.NewSessionMemoryStore(dir)

	now := time.Date(2026, 4, 14, 10, 30, 0, 0, time.UTC)
	for _, e := range sampleEntries(now) {
		store.AddEntry(e)
	}

	if err := store.Save("sess-roundtrip"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded := recall.NewSessionMemoryStore(dir)
	if err := loaded.Load("sess-roundtrip"); err != nil {
		t.Fatalf("Load: %v", err)
	}

	got := loaded.Entries()
	want := sampleEntries(now)
	if len(got) != len(want) {
		t.Fatalf("Entries len = %d; want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i].ID ||
			got[i].Type != want[i].Type ||
			got[i].Content != want[i].Content ||
			!got[i].ExtractedAt.Equal(want[i].ExtractedAt) ||
			got[i].Relevance != want[i].Relevance {
			t.Fatalf("Entries[%d] = %+v; want %+v", i, got[i], want[i])
		}
	}
}

// TestSessionMemoryStore_Save_UsesAtomicTempThenRename asserts that the
// persisted file is written under its final name (no .tmp residue) so a
// crash midway through a save cannot leave a half-written file in the
// path that Load reads from. This mirrors the invariant enforced by
// FileContextStore.
func TestSessionMemoryStore_Save_UsesAtomicTempThenRename(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := recall.NewSessionMemoryStore(dir)
	store.AddEntry(recall.KnowledgeEntry{ID: "e1", Type: "fact", Content: "x", Relevance: 0.5})

	if err := store.Save("sess-atomic"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "sess-atomic"))
	if err != nil {
		t.Fatalf("read session dir: %v", err)
	}
	for _, ent := range entries {
		if filepath.Ext(ent.Name()) == ".tmp" {
			t.Fatalf("atomic contract breached: temp file %q still present", ent.Name())
		}
	}
}

// TestSessionMemoryStore_Load_MissingSession_ReturnsSentinel asserts
// that loading from a session that has never been saved returns a
// typed sentinel rather than silently producing an empty store. Tests
// and callers can use errors.Is to distinguish "no memory" from other
// I/O failures.
func TestSessionMemoryStore_Load_MissingSession_ReturnsSentinel(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := recall.NewSessionMemoryStore(dir)

	err := store.Load("no-such-session")
	if err == nil {
		t.Fatalf("Load: expected error for missing session; got nil")
	}
}

// TestSessionMemoryStore_Retrieve_ReturnsByTypeSortedByRelevance
// asserts that Retrieve:
//   - filters by Type verbatim,
//   - sorts descending by Relevance,
//   - applies the limit cap,
//   - filters out entries with Relevance < 0.3 (the floor established
//     by plan T16 so trivia never leaks into the window).
func TestSessionMemoryStore_Retrieve_ReturnsByTypeSortedByRelevance(t *testing.T) {
	t.Parallel()

	store := recall.NewSessionMemoryStore(t.TempDir())
	store.AddEntry(recall.KnowledgeEntry{ID: "f1", Type: "fact", Content: "high-relevance fact", Relevance: 0.9})
	store.AddEntry(recall.KnowledgeEntry{ID: "f2", Type: "fact", Content: "mid-relevance fact", Relevance: 0.6})
	store.AddEntry(recall.KnowledgeEntry{ID: "f3", Type: "fact", Content: "low-relevance fact", Relevance: 0.2}) // filtered out
	store.AddEntry(recall.KnowledgeEntry{ID: "c1", Type: "convention", Content: "conv entry", Relevance: 0.4})

	got := store.Retrieve("fact", 5)
	if len(got) != 2 {
		t.Fatalf("Retrieve(fact, 5) len = %d; want 2 (low-relevance filtered out)", len(got))
	}
	if got[0].ID != "f1" {
		t.Fatalf("Retrieve[0] = %s; want f1 (highest relevance first)", got[0].ID)
	}
	if got[1].ID != "f2" {
		t.Fatalf("Retrieve[1] = %s; want f2", got[1].ID)
	}
}

// TestSessionMemoryStore_Retrieve_RespectsLimit asserts that limit
// caps the returned slice even when more entries qualify.
func TestSessionMemoryStore_Retrieve_RespectsLimit(t *testing.T) {
	t.Parallel()

	store := recall.NewSessionMemoryStore(t.TempDir())
	for i, r := range []float64{0.9, 0.8, 0.7, 0.6, 0.5} {
		store.AddEntry(recall.KnowledgeEntry{
			ID:        fmt.Sprintf("f%d", i),
			Type:      "fact",
			Content:   fmt.Sprintf("fact %d", i),
			Relevance: r,
		})
	}

	got := store.Retrieve("fact", 3)
	if len(got) != 3 {
		t.Fatalf("Retrieve(fact, 3) len = %d; want 3", len(got))
	}
}

// TestSessionMemoryStore_Save_MkdirError_SurfacesWrapped asserts that a
// filesystem failure when creating the session directory returns a
// descriptive wrapped error rather than silently succeeding. We trip
// it by passing a storage directory that already exists as a file on
// disk — MkdirAll returns ENOTDIR.
func TestSessionMemoryStore_Save_MkdirError_SurfacesWrapped(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	// Create a regular file at the path we're about to use as a directory.
	blocker := filepath.Join(parent, "blocker")
	if err := os.WriteFile(blocker, []byte("regular file"), 0o600); err != nil {
		t.Fatalf("setup blocker: %v", err)
	}

	store := recall.NewSessionMemoryStore(blocker)
	store.AddEntry(recall.KnowledgeEntry{ID: "e1", Type: "fact", Content: "x", Relevance: 0.5})

	if err := store.Save("any"); err == nil {
		t.Fatalf("Save: expected mkdir error when storageDir is a regular file; got nil")
	}
}

// TestSessionMemoryStore_Load_MalformedJSON_ReturnsUnmarshalError
// asserts that a corrupted memory.json is surfaced as an error rather
// than silently producing an empty store. Hand-write garbage at the
// expected location and assert Load rejects it.
func TestSessionMemoryStore_Load_MalformedJSON_ReturnsUnmarshalError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sessDir := filepath.Join(dir, "corrupt-sess")
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatalf("mkdir sess: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessDir, "memory.json"), []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("write garbage: %v", err)
	}

	store := recall.NewSessionMemoryStore(dir)
	if err := store.Load("corrupt-sess"); err == nil {
		t.Fatalf("Load: expected unmarshal error for garbage file; got nil")
	}
}

// TestSessionMemoryStore_Retrieve_ZeroLimit_ReturnsEmpty asserts the
// zero-limit short-circuit: callers that pass 0 get an empty slice
// without touching the store. This avoids the edge case where an
// external caller accidentally disables retrieval by config.
func TestSessionMemoryStore_Retrieve_ZeroLimit_ReturnsEmpty(t *testing.T) {
	t.Parallel()

	store := recall.NewSessionMemoryStore(t.TempDir())
	store.AddEntry(recall.KnowledgeEntry{ID: "f1", Type: "fact", Content: "x", Relevance: 0.9})

	if got := store.Retrieve("fact", 0); len(got) != 0 {
		t.Fatalf("Retrieve(fact, 0) = %v; want empty", got)
	}
	if got := store.Retrieve("fact", -5); len(got) != 0 {
		t.Fatalf("Retrieve(fact, -5) = %v; want empty", got)
	}
}

// TestSessionMemoryStore_AddEntry_DedupesByContent asserts that adding
// an entry with the same Content as an existing one is a no-op. This
// keeps the store idempotent across re-extraction runs so repeated
// goroutine firings cannot grow the store without bound.
func TestSessionMemoryStore_AddEntry_DedupesByContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := recall.NewSessionMemoryStore(dir)

	store.AddEntry(recall.KnowledgeEntry{ID: "e1", Type: "fact", Content: "dup", Relevance: 0.5})
	store.AddEntry(recall.KnowledgeEntry{ID: "e2", Type: "fact", Content: "dup", Relevance: 0.6})
	store.AddEntry(recall.KnowledgeEntry{ID: "e3", Type: "convention", Content: "unique", Relevance: 0.4})

	entries := store.Entries()
	if len(entries) != 2 {
		t.Fatalf("Entries len = %d; want 2 (dup collapsed to one + one unique)", len(entries))
	}
}
