package vaultindex_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/vaultindex"
)

func writeFile(path, content string) {
	GinkgoHelper()
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(content), 0o644)).To(Succeed())
}

var _ = Describe("WalkVault", func() {
	var root string

	BeforeEach(func() {
		root = GinkgoT().TempDir()
	})

	It("returns an empty slice for an empty vault", func() {
		files, err := vaultindex.WalkVault(root)
		Expect(err).ToNot(HaveOccurred())
		Expect(files).To(BeEmpty())
	})

	It("collects markdown files at any depth", func() {
		writeFile(filepath.Join(root, "top.md"), "top")
		writeFile(filepath.Join(root, "nested", "deep", "leaf.md"), "leaf")
		writeFile(filepath.Join(root, "ignore.txt"), "txt")

		files, err := vaultindex.WalkVault(root)
		Expect(err).ToNot(HaveOccurred())
		Expect(files).To(HaveLen(2))
		paths := []string{files[0].RelPath, files[1].RelPath}
		Expect(paths).To(ConsistOf("top.md", "nested/deep/leaf.md"))
	})

	It("skips hidden directories such as .obsidian", func() {
		writeFile(filepath.Join(root, ".obsidian", "config.md"), "config")
		writeFile(filepath.Join(root, "real.md"), "real")

		files, err := vaultindex.WalkVault(root)
		Expect(err).ToNot(HaveOccurred())
		Expect(files).To(HaveLen(1))
		Expect(files[0].RelPath).To(Equal("real.md"))
	})

	It("matches markdown extensions case-insensitively", func() {
		writeFile(filepath.Join(root, "Upper.MD"), "uppercase")
		files, err := vaultindex.WalkVault(root)
		Expect(err).ToNot(HaveOccurred())
		Expect(files).To(HaveLen(1))
	})

	It("returns an error when the vault root does not exist", func() {
		_, err := vaultindex.WalkVault(filepath.Join(root, "missing"))
		Expect(err).To(HaveOccurred())
	})
})
