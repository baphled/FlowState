package agent_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
)

var _ = Describe("Loader", func() {
	var tempDir string

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "agent-loader-test")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Describe("LoadManifest", func() {
		Context("with a JSON file", func() {
			It("loads the manifest with correct fields and defaults", func() {
				jsonPath := filepath.Join(tempDir, "test-agent.json")
				jsonContent := `{
					"schema_version": "1",
					"id": "test-agent",
					"name": "Test Agent",
					"complexity": "standard",
					"metadata": {
						"role": "Test role"
					}
				}`
				err := os.WriteFile(jsonPath, []byte(jsonContent), 0o600)
				Expect(err).NotTo(HaveOccurred())

				m, err := agent.LoadManifest(jsonPath)

				Expect(err).NotTo(HaveOccurred())
				Expect(m.ID).To(Equal("test-agent"))
				Expect(m.Name).To(Equal("Test Agent"))
				Expect(m.ContextManagement.MaxRecursionDepth).To(Equal(2))
			})
		})

		Context("with a Markdown file", func() {
			It("loads the manifest from frontmatter with correct fields", func() {
				mdPath := filepath.Join(tempDir, "md-agent.md")
				mdContent := "---\ndescription: Markdown agent\nmode: subagent\ndefault_skills:\n  - skill1\n  - skill2\n---\n# Agent Instructions\n"
				err := os.WriteFile(mdPath, []byte(mdContent), 0o600)
				Expect(err).NotTo(HaveOccurred())

				m, err := agent.LoadManifest(mdPath)

				Expect(err).NotTo(HaveOccurred())
				Expect(m.ID).To(Equal("md-agent"))
				Expect(m.Metadata.Role).To(Equal("Markdown agent"))
				Expect(m.Capabilities.Skills).To(HaveLen(2))
			})
		})
	})
})
