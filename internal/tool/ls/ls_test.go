package ls_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/ls"
)

var _ = Describe("LS Tool", func() {
	var toolInstance *ls.Tool

	BeforeEach(func() {
		toolInstance = ls.New()
	})

	It("identifies itself as ls", func() {
		Expect(toolInstance.Name()).To(Equal("ls"))
	})

	It("lists files and directories in sorted order", func() {
		tempDir, err := os.MkdirTemp("", "ls-tool-test-*")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(tempDir)

		Expect(os.Mkdir(filepath.Join(tempDir, "nested"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(tempDir, "beta.txt"), []byte("b"), 0o600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(tempDir, "alpha.txt"), []byte("a"), 0o600)).To(Succeed())

		result, err := toolInstance.Execute(context.Background(), tool.Input{
			Name: "ls",
			Arguments: map[string]interface{}{
				"path": tempDir,
			},
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(result.Error).NotTo(HaveOccurred())
		Expect(strings.Split(result.Output, "\n")).To(Equal([]string{"alpha.txt", "beta.txt", "nested/"}))
	})

	It("filters entries by pattern", func() {
		tempDir, err := os.MkdirTemp("", "ls-tool-filter-*")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(tempDir)

		Expect(os.WriteFile(filepath.Join(tempDir, "alpha.txt"), []byte("a"), 0o600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(tempDir, "beta.md"), []byte("b"), 0o600)).To(Succeed())

		result, err := toolInstance.Execute(context.Background(), tool.Input{
			Name: "ls",
			Arguments: map[string]interface{}{
				"path":    tempDir,
				"pattern": "*.txt",
			},
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(result.Error).NotTo(HaveOccurred())
		Expect(result.Output).To(Equal("alpha.txt"))
	})

	It("truncates oversized listings and embeds the recovery hint", func() {
		tempDir, err := os.MkdirTemp("", "ls-tool-truncate-*")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(tempDir)

		for i := 0; i < 2500; i++ {
			p := filepath.Join(tempDir, fmt.Sprintf("entry-%05d.txt", i))
			Expect(os.WriteFile(p, []byte{}, 0o600)).To(Succeed())
		}

		result, err := toolInstance.Execute(context.Background(), tool.Input{
			Name: "ls",
			Arguments: map[string]interface{}{
				"path": tempDir,
			},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Error).NotTo(HaveOccurred())
		Expect(strings.Contains(result.Output, "truncated")).To(BeTrue())
		Expect(result.Output).To(ContainSubstring("grep"))
		Expect(result.Output).To(ContainSubstring("offset"))
	})
})
