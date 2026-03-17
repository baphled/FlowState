package file_test

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/file"
)

var _ = Describe("File Tool", func() {
	var (
		fileTool *file.Tool
		ctx      context.Context
	)

	BeforeEach(func() {
		fileTool = file.New()
		ctx = context.Background()
	})

	Describe("Name", func() {
		It("returns file", func() {
			Expect(fileTool.Name()).To(Equal("file"))
		})
	})

	Describe("Description", func() {
		It("returns a non-empty description", func() {
			Expect(fileTool.Description()).NotTo(BeEmpty())
		})
	})

	Describe("Schema", func() {
		It("has operation and path in Required", func() {
			schema := fileTool.Schema()
			Expect(schema.Required).To(ContainElements("operation", "path"))
		})
	})

	Describe("Execute", func() {
		var tempDir string

		BeforeEach(func() {
			var err error
			tempDir, err = os.MkdirTemp("", "file-tool-test-*")
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			os.RemoveAll(tempDir)
		})

		Context("write then read round-trip", func() {
			It("writes content and reads it back", func() {
				testPath := filepath.Join(tempDir, "test.txt")
				testContent := "hello world"

				writeInput := tool.ToolInput{
					Name: "file",
					Arguments: map[string]interface{}{
						"operation": "write",
						"path":      testPath,
						"content":   testContent,
					},
				}

				writeResult, err := fileTool.Execute(ctx, writeInput)
				Expect(err).NotTo(HaveOccurred())
				Expect(writeResult.Error).ToNot(HaveOccurred())
				Expect(writeResult.Output).To(ContainSubstring("wrote"))

				readInput := tool.ToolInput{
					Name: "file",
					Arguments: map[string]interface{}{
						"operation": "read",
						"path":      testPath,
					},
				}

				readResult, err := fileTool.Execute(ctx, readInput)
				Expect(err).NotTo(HaveOccurred())
				Expect(readResult.Error).ToNot(HaveOccurred())
				Expect(readResult.Output).To(Equal(testContent))
			})
		})

		Context("read non-existent file", func() {
			It("returns non-nil Error in result", func() {
				input := tool.ToolInput{
					Name: "file",
					Arguments: map[string]interface{}{
						"operation": "read",
						"path":      filepath.Join(tempDir, "nonexistent.txt"),
					},
				}

				result, err := fileTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).To(HaveOccurred())
			})
		})

		Context("path traversal attempt", func() {
			It("returns non-nil Error in result for ../etc/passwd", func() {
				input := tool.ToolInput{
					Name: "file",
					Arguments: map[string]interface{}{
						"operation": "read",
						"path":      "../etc/passwd",
					},
				}

				result, err := fileTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).To(HaveOccurred())
			})
		})

		Context("missing operation", func() {
			It("returns Go error", func() {
				input := tool.ToolInput{
					Name: "file",
					Arguments: map[string]interface{}{
						"path": "/tmp/test.txt",
					},
				}

				_, err := fileTool.Execute(ctx, input)
				Expect(err).To(HaveOccurred())
			})
		})

		Context("unknown operation", func() {
			It("returns Go error", func() {
				input := tool.ToolInput{
					Name: "file",
					Arguments: map[string]interface{}{
						"operation": "delete",
						"path":      "/tmp/test.txt",
					},
				}

				_, err := fileTool.Execute(ctx, input)
				Expect(err).To(HaveOccurred())
			})
		})
	})
})
