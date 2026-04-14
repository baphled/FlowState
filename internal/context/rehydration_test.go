// Package context_test — T11 rehydration specification.
//
// Rehydrate is the inverse of Compact: given a CompactionSummary produced
// earlier, it reconstructs a minimum viable context window anchored on
// the summary's Intent and padded out with the file contents listed in
// FilesToRestore. The T10 trigger stores the summary on the engine;
// callers invoke rehydration explicitly when they want to seed the next
// turn with the pre-compaction state.
package context_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	contextpkg "github.com/baphled/flowstate/internal/context"
)

// writeTempFile creates a file under t.TempDir with the given content
// and returns its absolute path. All tempfiles are cleaned up by the
// test framework when the test finishes.
func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write tempfile: %v", err)
	}
	return path
}

// TestAutoCompactor_Rehydrate_HappyPath_ReturnsSystemAndFileMessages
// asserts the central invariant: given a summary with two files, the
// returned slice is [system, tool, tool] in that order, with the system
// message carrying the intent and each tool message carrying the full
// file contents.
func TestAutoCompactor_Rehydrate_HappyPath_ReturnsSystemAndFileMessages(t *testing.T) {
	t.Parallel()

	fileA := writeTempFile(t, "a.go", "package a // file-a content")
	fileB := writeTempFile(t, "b.go", "package b // file-b content")

	compactor := contextpkg.NewAutoCompactor(nil) // Rehydrate needs no summariser.
	summary := contextpkg.CompactionSummary{
		Intent:         "continue T11 integration work",
		FilesToRestore: []string{fileA, fileB},
	}

	msgs, err := compactor.Rehydrate(summary)
	if err != nil {
		t.Fatalf("Rehydrate: unexpected error: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("Rehydrate: len = %d; want 3 (1 system + 2 tool)", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Fatalf("msgs[0].Role = %q; want system", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].Content, "continue T11 integration work") {
		t.Fatalf("msgs[0].Content = %q; want to contain the intent string", msgs[0].Content)
	}
	if msgs[1].Role != "tool" || msgs[2].Role != "tool" {
		t.Fatalf("file messages should have role=tool; got %q, %q", msgs[1].Role, msgs[2].Role)
	}
	if !strings.Contains(msgs[1].Content, "file-a content") {
		t.Fatalf("msgs[1].Content does not contain file-a; got %q", msgs[1].Content)
	}
	if !strings.Contains(msgs[2].Content, "file-b content") {
		t.Fatalf("msgs[2].Content does not contain file-b; got %q", msgs[2].Content)
	}
}

// TestAutoCompactor_Rehydrate_EmptyFilesToRestore_ReturnsSystemOnly
// asserts that the minimal continuation — intent with no files — yields
// a single system message. This is the "summary-only" rehydration path.
func TestAutoCompactor_Rehydrate_EmptyFilesToRestore_ReturnsSystemOnly(t *testing.T) {
	t.Parallel()

	compactor := contextpkg.NewAutoCompactor(nil)
	summary := contextpkg.CompactionSummary{
		Intent:         "resume with no files to re-read",
		FilesToRestore: nil,
	}

	msgs, err := compactor.Rehydrate(summary)
	if err != nil {
		t.Fatalf("Rehydrate: unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("Rehydrate: len = %d; want 1", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Fatalf("msgs[0].Role = %q; want system", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].Content, "resume with no files") {
		t.Fatalf("system message does not carry the intent; got %q", msgs[0].Content)
	}
}

// TestAutoCompactor_Rehydrate_MissingFile_ReturnsWrappedError asserts
// the plan's explicit "do not silently ignore missing files" rule: a
// missing path must surface a wrapped error naming the offending path so
// the caller can log it and react. ErrRehydrationRead is the sentinel
// callers can test against.
func TestAutoCompactor_Rehydrate_MissingFile_ReturnsWrappedError(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "does-not-exist.go")
	present := writeTempFile(t, "present.go", "package present")

	compactor := contextpkg.NewAutoCompactor(nil)
	summary := contextpkg.CompactionSummary{
		Intent:         "missing-file path exercise",
		FilesToRestore: []string{present, missing},
	}

	_, err := compactor.Rehydrate(summary)
	if err == nil {
		t.Fatalf("Rehydrate: expected error for missing file; got nil")
	}
	if !errors.Is(err, contextpkg.ErrRehydrationRead) {
		t.Fatalf("Rehydrate: err = %v; want ErrRehydrationRead", err)
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("Rehydrate: err = %q; want message containing the offending path", err.Error())
	}
}

// TestAutoCompactor_Rehydrate_EmptyIntent_ReturnsValidationError asserts
// that an incompletely-populated summary — specifically one with an
// empty Intent — is rejected. Intent is the semantic anchor of the
// rehydration system message; without it there is nothing to resume
// from. The sentinel ErrInvalidSummary is shared with Compact's
// validation so callers can treat both errors symmetrically.
func TestAutoCompactor_Rehydrate_EmptyIntent_ReturnsValidationError(t *testing.T) {
	t.Parallel()

	compactor := contextpkg.NewAutoCompactor(nil)
	summary := contextpkg.CompactionSummary{
		Intent:         "",
		FilesToRestore: nil,
	}

	_, err := compactor.Rehydrate(summary)
	if err == nil {
		t.Fatalf("Rehydrate: expected validation error for empty intent; got nil")
	}
	if !errors.Is(err, contextpkg.ErrInvalidSummary) {
		t.Fatalf("Rehydrate: err = %v; want ErrInvalidSummary", err)
	}
}
