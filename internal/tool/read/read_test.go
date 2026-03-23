package read_test

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/read"
)

var _ = Describe("Read Tool", func() {
	var (
		readTool *read.Tool
		ctx      context.Context
	)

	BeforeEach(func() {
		readTool = read.New()
		ctx = context.Background()
	})

	Describe("Name", func() {
		It("returns read", func() {
			Expect(readTool.Name()).To(Equal("read"))
		})
	})

	Describe("Description", func() {
		It("returns a non-empty description", func() {
			Expect(readTool.Description()).NotTo(BeEmpty())
		})
	})

	Describe("Schema", func() {
		It("has path in Required", func() {
			schema := readTool.Schema()
			Expect(schema.Required).To(ConsistOf("path"))
		})

		It("has only the path property", func() {
			schema := readTool.Schema()
			Expect(schema.Properties).To(HaveKey("path"))
			Expect(schema.Properties).To(HaveLen(1))
		})
	})

	Describe("Execute", func() {
		var tempDir string

		BeforeEach(func() {
			var err error
			tempDir, err = os.MkdirTemp("", "read-tool-test-*")
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			os.RemoveAll(tempDir)
		})

		Context("when reading an existing file", func() {
			It("returns the file content", func() {
				testPath := filepath.Join(tempDir, "test.txt")
				testContent := "hello world"
				Expect(os.WriteFile(testPath, []byte(testContent), 0o600)).To(Succeed())

				input := tool.Input{
					Name: "read",
					Arguments: map[string]interface{}{
						"path": testPath,
					},
				}

				result, err := readTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).NotTo(HaveOccurred())
				Expect(result.Output).To(Equal(testContent))
			})
		})

		Context("when reading a non-existent file", func() {
			It("returns non-nil Error in result", func() {
				input := tool.Input{
					Name: "read",
					Arguments: map[string]interface{}{
						"path": filepath.Join(tempDir, "nonexistent.txt"),
					},
				}

				result, err := readTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).To(HaveOccurred())
			})
		})

		Context("when path contains traversal", func() {
			It("returns non-nil Error in result", func() {
				input := tool.Input{
					Name: "read",
					Arguments: map[string]interface{}{
						"path": "../etc/passwd",
					},
				}

				result, err := readTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).To(HaveOccurred())
			})
		})

		Context("when path argument is missing", func() {
			It("returns a Go error", func() {
				input := tool.Input{
					Name:      "read",
					Arguments: map[string]interface{}{},
				}

				_, err := readTool.Execute(ctx, input)
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when path argument is empty", func() {
			It("returns a Go error", func() {
				input := tool.Input{
					Name: "read",
					Arguments: map[string]interface{}{
						"path": "",
					},
				}

				_, err := readTool.Execute(ctx, input)
				Expect(err).To(HaveOccurred())
			})
		})
	})
})
