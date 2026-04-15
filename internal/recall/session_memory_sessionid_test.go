// Package recall_test — H4 defence-in-depth coverage for
// SessionMemoryStore.Save and SessionMemoryStore.Load.
//
// The CLI gate in internal/cli/run.go rejects unsafe --session values
// before any store method runs, but SessionMemoryStore is also callable
// from in-process consumers (tests, future internal APIs) that bypass
// the CLI. These tests pin the layered-defence contract: every path-
// unsafe sessionID supplied directly to Save/Load is refused with
// ErrInvalidSessionID, and no filesystem mutation occurs.
package recall_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/sessionid"
)

// TestSessionMemoryStore_Save_RejectsUnsafeSessionID pins the refusal.
// The store never touches the filesystem because the validator fires
// before filepath.Join is called.
func TestSessionMemoryStore_Save_RejectsUnsafeSessionID(t *testing.T) {
	t.Parallel()

	cases := []string{
		"",
		"../../tmp/evil",
		"/abs/evil",
		".hidden",
		"foo/bar",
	}

	for _, id := range cases {
		t.Run(id, func(t *testing.T) {
			dir := t.TempDir()
			store := recall.NewSessionMemoryStore(dir)
			err := store.Save(id)
			if err == nil {
				t.Fatalf("Save(%q) = nil; want refusal", id)
			}
			if !errors.Is(err, sessionid.ErrInvalidSessionID) {
				t.Fatalf("Save(%q) err = %v; want errors.Is ErrInvalidSessionID", id, err)
			}

			// No side effect — the root dir must be empty because
			// Save must not call MkdirAll with a tainted path.
			entries, readErr := os.ReadDir(dir)
			if readErr != nil {
				t.Fatalf("ReadDir after refused Save: %v", readErr)
			}
			if len(entries) != 0 {
				t.Fatalf("Save(%q) left %d entries in storageDir; want 0", id, len(entries))
			}
		})
	}
}

// TestSessionMemoryStore_Load_RejectsUnsafeSessionID pins the symmetric
// refusal on the read path. An attacker who plants a file under a
// traversal-escape path must not be able to get SessionMemoryStore to
// read it by supplying the escape as sessionID.
func TestSessionMemoryStore_Load_RejectsUnsafeSessionID(t *testing.T) {
	t.Parallel()

	cases := []string{
		"",
		"../../tmp/evil",
		"/abs/evil",
		".hidden",
		"foo\\bar",
	}

	for _, id := range cases {
		t.Run(id, func(t *testing.T) {
			dir := t.TempDir()
			store := recall.NewSessionMemoryStore(dir)
			err := store.Load(id)
			if err == nil {
				t.Fatalf("Load(%q) = nil; want refusal", id)
			}
			if !errors.Is(err, sessionid.ErrInvalidSessionID) {
				t.Fatalf("Load(%q) err = %v; want errors.Is ErrInvalidSessionID", id, err)
			}
		})
	}
}

// TestSessionMemoryStore_Save_AcceptsSafeSessionID belts the positive
// contract so the validator additions do not regress legitimate use.
func TestSessionMemoryStore_Save_AcceptsSafeSessionID(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := recall.NewSessionMemoryStore(dir)
	if err := store.Save("safe-session-123"); err != nil {
		t.Fatalf("Save(safe): %v", err)
	}
	// Memory file must exist under the expected subdir.
	if _, err := os.Stat(filepath.Join(dir, "safe-session-123", "memory.json")); err != nil {
		t.Fatalf("expected memory.json at safe-session-123: %v", err)
	}
}
