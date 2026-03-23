package write_test

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/write"
)

var _ = Describe("Write Tool", func() {
	var (
		writeTool *write.Tool
		ctx       context.Context
	)

	BeforeEach(func() {
		writeTool = write.New()
		ctx = context.Background()
	})

	Describe("Name", func() {
		It("returns write", func() {
			Expect(writeTool.Name()).To(Equal("write"))
		})
	})

	Describe("Description", func() {
		It("returns a non-empty description", func() {
			Expect(writeTool.Description()).NotTo(BeEmpty())
		})
	})

	Describe("Schema", func() {
		It("has path in Required", func() {
			schema := writeTool.Schema()
			Expect(schema.Required).To(ConsistOf("path"))
		})

		It("has path and content properties", func() {
			schema := writeTool.Schema()
			Expect(schema.Properties).To(HaveKey("path"))
			Expect(schema.Properties).To(HaveKey("content"))
			Expect(schema.Properties).To(HaveLen(2))
		})
	})

	Describe("Execute", func() {
		var tempDir string

		BeforeEach(func() {
			var err error
			tempDir, err = os.MkdirTemp("", "write-tool-test-*")
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			os.RemoveAll(tempDir)
		})

		Context("when writing content to a file", func() {
			It("writes the content and returns a confirmation", func() {
				testPath := filepath.Join(tempDir, "test.txt")
				testContent := "hello world"

				input := tool.Input{
					Name: "write",
					Arguments: map[string]interface{}{
						"path":    testPath,
						"content": testContent,
					},
				}

				result, err := writeTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("wrote"))

				data, readErr := os.ReadFile(testPath)
				Expect(readErr).NotTo(HaveOccurred())
				Expect(string(data)).To(Equal(testContent))
			})
		})

		Context("when writing to a nested directory", func() {
			It("creates parent directories and writes the file", func() {
				testPath := filepath.Join(tempDir, "sub", "dir", "test.txt")
				testContent := "nested content"

				input := tool.Input{
					Name: "write",
					Arguments: map[string]interface{}{
						"path":    testPath,
						"content": testContent,
					},
				}

				result, err := writeTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).NotTo(HaveOccurred())

				data, readErr := os.ReadFile(testPath)
				Expect(readErr).NotTo(HaveOccurred())
				Expect(string(data)).To(Equal(testContent))
			})
		})

		Context("when content is missing", func() {
			It("writes an empty file", func() {
				testPath := filepath.Join(tempDir, "empty.txt")

				input := tool.Input{
					Name: "write",
					Arguments: map[string]interface{}{
						"path": testPath,
					},
				}

				result, err := writeTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).NotTo(HaveOccurred())

				data, readErr := os.ReadFile(testPath)
				Expect(readErr).NotTo(HaveOccurred())
				Expect(data).To(BeEmpty())
			})
		})

		Context("when path contains traversal", func() {
			It("returns non-nil Error in result", func() {
				input := tool.Input{
					Name: "write",
					Arguments: map[string]interface{}{
						"path":    "../etc/evil",
						"content": "bad",
					},
				}

				result, err := writeTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).To(HaveOccurred())
			})
		})

		Context("when path argument is missing", func() {
			It("returns a Go error", func() {
				input := tool.Input{
					Name:      "write",
					Arguments: map[string]interface{}{},
				}

				_, err := writeTool.Execute(ctx, input)
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when path argument is empty", func() {
			It("returns a Go error", func() {
				input := tool.Input{
					Name: "write",
					Arguments: map[string]interface{}{
						"path": "",
					},
				}

				_, err := writeTool.Execute(ctx, input)
				Expect(err).To(HaveOccurred())
			})
		})
	})
})
