package edit_test

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/edit"
)

var _ = Describe("Edit Tool", func() {
	var (
		editTool *edit.Tool
		ctx      context.Context
	)

	BeforeEach(func() {
		editTool = edit.New()
		ctx = context.Background()
	})

	Describe("Name", func() {
		It("returns edit", func() {
			Expect(editTool.Name()).To(Equal("edit"))
		})
	})

	Describe("Schema", func() {
		It("requires file, old_string, and new_string", func() {
			schema := editTool.Schema()
			Expect(schema.Required).To(ConsistOf("file", "old_string", "new_string"))
		})
	})

	Describe("Execute", func() {
		var tempDir string

		BeforeEach(func() {
			var err error
			tempDir, err = os.MkdirTemp(".", "edit-tool-test-*")
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			os.RemoveAll(tempDir)
		})

		It("replaces the first exact match in the file", func() {
			testPath := filepath.Join(tempDir, "test.txt")
			Expect(os.WriteFile(testPath, []byte("hello world\nhello world\n"), 0o600)).To(Succeed())

			result, err := editTool.Execute(ctx, tool.Input{
				Name: "edit",
				Arguments: map[string]interface{}{
					"file":       testPath,
					"old_string": "hello",
					"new_string": "goodbye",
				},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).NotTo(HaveOccurred())

			data, readErr := os.ReadFile(testPath)
			Expect(readErr).NotTo(HaveOccurred())
			Expect(string(data)).To(Equal("goodbye world\nhello world\n"))
		})

		It("returns an error when the old string is missing", func() {
			testPath := filepath.Join(tempDir, "test.txt")
			Expect(os.WriteFile(testPath, []byte("hello world"), 0o600)).To(Succeed())

			result, err := editTool.Execute(ctx, tool.Input{
				Name: "edit",
				Arguments: map[string]interface{}{
					"file":       testPath,
					"old_string": "missing",
					"new_string": "goodbye",
				},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).To(HaveOccurred())
		})

		It("rejects path traversal targets", func() {
			result, err := editTool.Execute(ctx, tool.Input{
				Name: "edit",
				Arguments: map[string]interface{}{
					"file":       "../outside.txt",
					"old_string": "hello",
					"new_string": "goodbye",
				},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).To(HaveOccurred())
			Expect(result.Error.Error()).To(ContainSubstring("path traversal"))
		})
	})
})
