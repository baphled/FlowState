package grep_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/grep"
)

func TestGrep(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Grep Tool Suite")
}

var _ = Describe("Grep Tool", func() {
	var (
		grepTool *grep.Tool
		ctx      context.Context
	)

	BeforeEach(func() {
		grepTool = grep.New()
		ctx = context.Background()
	})

	It("returns grep as its name", func() {
		Expect(grepTool.Name()).To(Equal("grep"))
	})

	It("requires a pattern and path to search", func() {
		schema := grepTool.Schema()
		Expect(schema.Required).To(ContainElements("pattern", "path"))
		Expect(schema.Properties).To(HaveKey("pattern"))
		Expect(schema.Properties).To(HaveKey("path"))
	})

	Describe("Execute", func() {
		var tempDir string

		BeforeEach(func() {
			var err error
			tempDir, err = os.MkdirTemp("", "grep-tool-test-*")
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			os.RemoveAll(tempDir)
		})

		It("returns matching lines with file names and line numbers", func() {
			filePath := filepath.Join(tempDir, "notes.txt")
			Expect(os.WriteFile(filePath, []byte("alpha\nbeta\nalpha again\n"), 0o600)).To(Succeed())

			result, err := grepTool.Execute(ctx, tool.Input{
				Name: "grep",
				Arguments: map[string]interface{}{
					"pattern": "alpha",
					"path":    tempDir,
				},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).NotTo(HaveOccurred())
			Expect(result.Output).To(ContainSubstring("notes.txt:1:alpha"))
			Expect(result.Output).To(ContainSubstring("notes.txt:3:alpha again"))
		})

		It("filters files by glob pattern", func() {
			Expect(os.WriteFile(filepath.Join(tempDir, "a.txt"), []byte("match me"), 0o600)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(tempDir, "b.md"), []byte("match me too"), 0o600)).To(Succeed())

			result, err := grepTool.Execute(ctx, tool.Input{
				Name: "grep",
				Arguments: map[string]interface{}{
					"pattern": "match",
					"path":    tempDir,
					"include": "*.txt",
				},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).NotTo(HaveOccurred())
			Expect(result.Output).To(ContainSubstring("a.txt"))
			Expect(result.Output).NotTo(ContainSubstring("b.md"))
		})

		It("supports files_with_matches mode", func() {
			Expect(os.WriteFile(filepath.Join(tempDir, "a.txt"), []byte("match me"), 0o600)).To(Succeed())

			result, err := grepTool.Execute(ctx, tool.Input{
				Name: "grep",
				Arguments: map[string]interface{}{
					"pattern": "match",
					"path":    tempDir,
					"mode":    "files_with_matches",
				},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(result.Output)).To(Equal(filepath.Join(tempDir, "a.txt")))
		})

		It("returns a Go error when pattern is missing", func() {
			_, err := grepTool.Execute(ctx, tool.Input{Name: "grep", Arguments: map[string]interface{}{"path": tempDir}})
			Expect(err).To(HaveOccurred())
		})
	})
})
