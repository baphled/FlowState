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
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	contextpkg "github.com/baphled/flowstate/internal/context"
)

var _ = Describe("AutoCompactor.Rehydrate (T11)", func() {
	// writeTempFile creates a file under GinkgoT().TempDir with the given
	// content and returns its absolute path. All tempfiles are cleaned up
	// when the spec finishes.
	writeTempFile := func(name, content string) string {
		path := filepath.Join(GinkgoT().TempDir(), name)
		Expect(os.WriteFile(path, []byte(content), 0o600)).To(Succeed(),
			"write tempfile %q", name)
		return path
	}

	It("returns [system, tool, tool] for a happy-path summary with two files", func() {
		fileA := writeTempFile("a.go", "package a // file-a content")
		fileB := writeTempFile("b.go", "package b // file-b content")

		compactor := contextpkg.NewAutoCompactor(nil)
		summary := contextpkg.CompactionSummary{
			Intent:         "continue T11 integration work",
			FilesToRestore: []string{fileA, fileB},
		}

		msgs, err := compactor.Rehydrate(summary)
		Expect(err).NotTo(HaveOccurred(), "Rehydrate")
		Expect(msgs).To(HaveLen(3), "want 1 system + 2 tool messages")
		Expect(msgs[0].Role).To(Equal("system"))
		Expect(msgs[0].Content).To(ContainSubstring("continue T11 integration work"))
		Expect(msgs[1].Role).To(Equal("tool"))
		Expect(msgs[2].Role).To(Equal("tool"))
		Expect(msgs[1].Content).To(ContainSubstring("file-a content"))
		Expect(msgs[2].Content).To(ContainSubstring("file-b content"))
	})

	It("returns a single system message when FilesToRestore is empty", func() {
		compactor := contextpkg.NewAutoCompactor(nil)
		summary := contextpkg.CompactionSummary{
			Intent:         "resume with no files to re-read",
			FilesToRestore: nil,
		}

		msgs, err := compactor.Rehydrate(summary)
		Expect(err).NotTo(HaveOccurred(), "Rehydrate")
		Expect(msgs).To(HaveLen(1))
		Expect(msgs[0].Role).To(Equal("system"))
		Expect(msgs[0].Content).To(ContainSubstring("resume with no files"))
	})

	It("wraps ErrRehydrationRead and names the offending path on missing file", func() {
		missing := filepath.Join(GinkgoT().TempDir(), "does-not-exist.go")
		present := writeTempFile("present.go", "package present")

		compactor := contextpkg.NewAutoCompactor(nil)
		summary := contextpkg.CompactionSummary{
			Intent:         "missing-file path exercise",
			FilesToRestore: []string{present, missing},
		}

		_, err := compactor.Rehydrate(summary)
		Expect(err).To(HaveOccurred(), "expected error for missing file")
		Expect(err).To(MatchError(contextpkg.ErrRehydrationRead))
		Expect(err.Error()).To(ContainSubstring(missing),
			"error must name the offending path so callers can log it")
	})

	It("returns ErrInvalidSummary when Intent is empty", func() {
		compactor := contextpkg.NewAutoCompactor(nil)
		summary := contextpkg.CompactionSummary{
			Intent:         "",
			FilesToRestore: nil,
		}

		_, err := compactor.Rehydrate(summary)
		Expect(err).To(HaveOccurred(), "expected validation error for empty intent")
		Expect(err).To(MatchError(contextpkg.ErrInvalidSummary))
	})
})
