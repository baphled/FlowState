package glob_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/glob"
)

var _ = Describe("Glob Tool", func() {
	var (
		globTool *glob.Tool
		ctx      context.Context
	)

	BeforeEach(func() {
		globTool = glob.New()
		ctx = context.Background()
	})

	Describe("Name", func() {
		It("returns glob", func() {
			Expect(globTool.Name()).To(Equal("glob"))
		})
	})

	Describe("Description", func() {
		It("returns a non-empty description", func() {
			Expect(globTool.Description()).NotTo(BeEmpty())
		})
	})

	Describe("Schema", func() {
		It("requires pattern", func() {
			schema := globTool.Schema()
			Expect(schema.Required).To(ConsistOf("pattern"))
		})

		It("defines pattern and path properties", func() {
			schema := globTool.Schema()
			Expect(schema.Properties).To(HaveKey("pattern"))
			Expect(schema.Properties).To(HaveKey("path"))
		})
	})

	Describe("Execute", func() {
		var tempDir string

		BeforeEach(func() {
			var err error
			tempDir, err = os.MkdirTemp("", "glob-tool-test-*")
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			os.RemoveAll(tempDir)
		})

		It("returns sorted matching file paths", func() {
			files := []string{
				filepath.Join(tempDir, "a.txt"),
				filepath.Join(tempDir, "b.txt"),
				filepath.Join(tempDir, "nested", "c.txt"),
			}
			Expect(os.MkdirAll(filepath.Join(tempDir, "nested"), 0o755)).To(Succeed())
			for _, file := range files {
				Expect(os.WriteFile(file, []byte("x"), 0o600)).To(Succeed())
			}

			result, err := globTool.Execute(ctx, tool.Input{
				Name: "glob",
				Arguments: map[string]interface{}{
					"pattern": "**/*.txt",
					"path":    tempDir,
				},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).NotTo(HaveOccurred())

			lines := []string{}
			for _, line := range strings.Split(result.Output, "\n") {
				if line != "" {
					lines = append(lines, line)
				}
			}
			Expect(lines).To(ContainElement(filepath.Join(tempDir, "a.txt")))
			Expect(lines).To(ContainElement(filepath.Join(tempDir, "b.txt")))
			Expect(lines).To(ContainElement(filepath.Join(tempDir, "nested", "c.txt")))
			sorted := append([]string(nil), lines...)
			sort.Strings(sorted)
			Expect(lines).To(Equal(sorted))
		})

		It("returns an error when pattern is missing", func() {
			_, err := globTool.Execute(ctx, tool.Input{Name: "glob", Arguments: map[string]interface{}{}})
			Expect(err).To(HaveOccurred())
		})

		It("respects the file limit", func() {
			Expect(os.MkdirAll(filepath.Join(tempDir, "many"), 0o755)).To(Succeed())
			for i := range 101 {
				Expect(os.WriteFile(filepath.Join(tempDir, "many", "file"+strings.Repeat("a", i)+".txt"), []byte("x"), 0o600)).To(Succeed())
			}

			result, err := globTool.Execute(ctx, tool.Input{
				Name: "glob",
				Arguments: map[string]interface{}{
					"pattern": "**/*.txt",
					"path":    tempDir,
				},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).To(HaveOccurred())
		})
	})
})
