package write_test

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/pathguard"
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

		// Slice B integration smoke: drive Execute through a guard
		// constructed via pathguard.NewWithPermissions, mirroring what
		// app.buildPathGuard produces when permissions.yaml is present.
		// Confirms the wired path: Execute -> CheckForTool("write", ...)
		// -> *config.Permissions.Match -> allow/deny decision.
		Context("when wired through pathguard.NewWithPermissions (Slice B integration)", func() {
			var (
				deniedRoot string
				deniedPath string
			)

			BeforeEach(func() {
				// Use a real on-disk vault sentinel so the legacy
				// denied-roots check is meaningful (the cwd carve-out
				// in pathguard requires cwd not to be under it).
				var err error
				deniedRoot, err = os.MkdirTemp("", "write-tool-slice-b-vault-*")
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func() { os.RemoveAll(deniedRoot) })
				deniedPath = filepath.Join(deniedRoot, "file.md")
			})

			It("DENIES a write under the vault when no permissions.yaml is wired (legacy fallback)", func() {
				// Mirrors app.buildPathGuard's branch when
				// LoadPermissions returns (nil, nil) — Guard built
				// via New(denied), CheckForTool falls through to
				// legacy Check.
				guard := pathguard.New([]string{deniedRoot})
				guardedWrite := write.NewWithGuard(guard)

				result, err := guardedWrite.Execute(ctx, tool.Input{
					Name: "write",
					Arguments: map[string]interface{}{
						"path":    deniedPath,
						"content": "bypass attempt",
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).To(HaveOccurred(), "legacy denied-roots should still block writes under the vault")
				_, statErr := os.Stat(deniedPath)
				Expect(os.IsNotExist(statErr)).To(BeTrue(), "no file should have been created")
			})

			It("ALLOWS a write under the vault when permissions.yaml grants write.allow=[<vault>/**]", func() {
				perms := &config.Permissions{
					Version: 1,
					Tools: map[string]config.ToolRules{
						"write": {Allow: []string{filepath.Join(deniedRoot, "**")}},
					},
				}
				guard := pathguard.NewWithPermissions([]string{deniedRoot}, perms)
				guardedWrite := write.NewWithGuard(guard)

				result, err := guardedWrite.Execute(ctx, tool.Input{
					Name: "write",
					Arguments: map[string]interface{}{
						"path":    deniedPath,
						"content": "permissioned write",
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).NotTo(HaveOccurred(), "permissions.yaml allow grant should bypass the legacy denied-roots check")
				data, readErr := os.ReadFile(deniedPath)
				Expect(readErr).NotTo(HaveOccurred())
				Expect(string(data)).To(Equal("permissioned write"))
			})
		})
	})
})
