package vaultindex_test

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/vaultindex"
)

var _ = Describe("State sidecar", func() {
	var (
		tmpDir string
		path   string
	)

	BeforeEach(func() {
		tmpDir = GinkgoT().TempDir()
		path = filepath.Join(tmpDir, vaultindex.SidecarFilename)
	})

	It("loads an empty state when the sidecar does not exist", func() {
		s, err := vaultindex.LoadState(path)
		Expect(err).ToNot(HaveOccurred())
		_, ok := s.Get("any.md")
		Expect(ok).To(BeFalse())
	})

	It("treats a brand-new file as needing reindex", func() {
		s, err := vaultindex.LoadState(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(s.NeedsReindex("note.md", time.Now())).To(BeTrue())
	})

	It("treats an unchanged file as up-to-date after Update + Save", func() {
		s, err := vaultindex.LoadState(path)
		Expect(err).ToNot(HaveOccurred())

		mtime := time.Now()
		s.Update("note.md", mtime, 4)
		Expect(s.Save()).To(Succeed())

		reloaded, err := vaultindex.LoadState(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(reloaded.NeedsReindex("note.md", mtime)).To(BeFalse())
		entry, ok := reloaded.Get("note.md")
		Expect(ok).To(BeTrue())
		Expect(entry.ChunkCount).To(Equal(4))
	})

	It("reindexes a file whose mtime has advanced", func() {
		s, err := vaultindex.LoadState(path)
		Expect(err).ToNot(HaveOccurred())
		old := time.Now().Add(-1 * time.Hour)
		s.Update("note.md", old, 1)

		Expect(s.NeedsReindex("note.md", time.Now())).To(BeTrue())
	})

	It("returns an error when the sidecar is malformed JSON", func() {
		Expect(os.WriteFile(path, []byte("{not json"), 0o644)).To(Succeed())
		_, err := vaultindex.LoadState(path)
		Expect(err).To(HaveOccurred())
	})

	It("computes the canonical sidecar path", func() {
		Expect(vaultindex.SidecarPath("/vault")).To(Equal("/vault/" + vaultindex.SidecarFilename))
	})
})
