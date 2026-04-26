package multiedit_test

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/multiedit"
)

// MultiEdit tool tests cover metadata, the happy path of applying an
// ordered series of edits to a file, and rejection of path-traversal
// targets.
var _ = Describe("MultiEdit tool", func() {
	Describe("metadata", func() {
		It("reports its name and a non-empty description", func() {
			toolUnderTest := multiedit.New()
			Expect(toolUnderTest.Name()).To(Equal("multiedit"))
			Expect(toolUnderTest.Description()).NotTo(BeEmpty())
		})
	})

	Describe("Execute", func() {
		var (
			tmpDir   string
			filePath string
		)

		BeforeEach(func() {
			var err error
			tmpDir, err = os.MkdirTemp(".", "multiedit-test-*")
			Expect(err).NotTo(HaveOccurred())
			filePath = filepath.Join(tmpDir, "example.txt")
		})

		AfterEach(func() {
			Expect(os.RemoveAll(tmpDir)).To(Succeed())
		})

		It("applies all edits in order and writes the file back", func() {
			Expect(os.WriteFile(filePath, []byte("one\ntwo\nthree\n"), 0o600)).To(Succeed())

			result, err := multiedit.New().Execute(context.Background(), tool.Input{
				Name: "multiedit",
				Arguments: map[string]any{
					"file_path": filePath,
					"edits": []any{
						map[string]any{"old_string": "one", "new_string": "1"},
						map[string]any{"old_string": "three", "new_string": "3"},
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).NotTo(HaveOccurred())

			updated, readErr := os.ReadFile(filePath)
			Expect(readErr).NotTo(HaveOccurred())
			Expect(string(updated)).To(Equal("1\ntwo\n3\n"))
		})

		It("rejects path traversal targets", func() {
			result, err := multiedit.New().Execute(context.Background(), tool.Input{
				Name: "multiedit",
				Arguments: map[string]any{
					"file_path": "../outside.txt",
					"edits":     []any{map[string]any{"old_string": "one", "new_string": "1"}},
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).To(HaveOccurred())
		})
	})
})
