package agent_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
)

var _ = Describe("AgentRegistry", func() {
	var (
		registry *agent.AgentRegistry
		tempDir  string
	)

	BeforeEach(func() {
		registry = agent.NewAgentRegistry()
		var err error
		tempDir, err = os.MkdirTemp("", "agent-registry-test")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Describe("NewAgentRegistry", func() {
		It("creates an empty registry", func() {
			Expect(registry).NotTo(BeNil())
			Expect(registry.List()).To(BeEmpty())
		})
	})

	Describe("Register", func() {
		It("adds a manifest to the registry", func() {
			manifest := &agent.AgentManifest{
				ID:   "test-agent",
				Name: "Test Agent",
			}

			registry.Register(manifest)

			retrieved, ok := registry.Get("test-agent")
			Expect(ok).To(BeTrue())
			Expect(retrieved.ID).To(Equal("test-agent"))
			Expect(retrieved.Name).To(Equal("Test Agent"))
		})

		It("overwrites existing manifest with same ID", func() {
			manifest1 := &agent.AgentManifest{
				ID:   "test-agent",
				Name: "Original Name",
			}
			manifest2 := &agent.AgentManifest{
				ID:   "test-agent",
				Name: "Updated Name",
			}

			registry.Register(manifest1)
			registry.Register(manifest2)

			retrieved, ok := registry.Get("test-agent")
			Expect(ok).To(BeTrue())
			Expect(retrieved.Name).To(Equal("Updated Name"))
		})
	})

	Describe("Get", func() {
		It("returns manifest and true when found", func() {
			manifest := &agent.AgentManifest{
				ID:   "existing-agent",
				Name: "Existing Agent",
			}
			registry.Register(manifest)

			retrieved, ok := registry.Get("existing-agent")

			Expect(ok).To(BeTrue())
			Expect(retrieved.ID).To(Equal("existing-agent"))
		})

		It("returns nil and false when not found", func() {
			retrieved, ok := registry.Get("nonexistent-agent")

			Expect(ok).To(BeFalse())
			Expect(retrieved).To(BeNil())
		})
	})

	Describe("List", func() {
		It("returns nil for empty registry", func() {
			Expect(registry.List()).To(BeNil())
		})

		It("returns all manifests sorted by ID", func() {
			manifests := []*agent.AgentManifest{
				{ID: "charlie-agent", Name: "Charlie"},
				{ID: "alpha-agent", Name: "Alpha"},
				{ID: "bravo-agent", Name: "Bravo"},
			}
			for _, m := range manifests {
				registry.Register(m)
			}

			list := registry.List()

			Expect(list).To(HaveLen(3))
			Expect(list[0].ID).To(Equal("alpha-agent"))
			Expect(list[1].ID).To(Equal("bravo-agent"))
			Expect(list[2].ID).To(Equal("charlie-agent"))
		})
	})

	Describe("Discover", func() {
		Context("with valid directory", func() {
			It("discovers JSON manifests", func() {
				jsonContent := `{
					"schema_version": "1",
					"id": "json-agent",
					"name": "JSON Agent"
				}`
				err := os.WriteFile(filepath.Join(tempDir, "json-agent.json"), []byte(jsonContent), 0o644)
				Expect(err).NotTo(HaveOccurred())

				err = registry.Discover(tempDir)

				Expect(err).NotTo(HaveOccurred())
				manifest, ok := registry.Get("json-agent")
				Expect(ok).To(BeTrue())
				Expect(manifest.Name).To(Equal("JSON Agent"))
			})

			It("discovers Markdown manifests", func() {
				mdContent := `---
description: Markdown agent for testing
mode: subagent
---
# Agent Instructions
`
				err := os.WriteFile(filepath.Join(tempDir, "md-agent.md"), []byte(mdContent), 0o644)
				Expect(err).NotTo(HaveOccurred())

				err = registry.Discover(tempDir)

				Expect(err).NotTo(HaveOccurred())
				manifest, ok := registry.Get("md-agent")
				Expect(ok).To(BeTrue())
				Expect(manifest.Metadata.Role).To(Equal("Markdown agent for testing"))
			})

			It("discovers both JSON and Markdown manifests", func() {
				jsonContent := `{"id": "json-agent", "name": "JSON Agent"}`
				mdContent := `---
description: MD Agent
---
`
				err := os.WriteFile(filepath.Join(tempDir, "json-agent.json"), []byte(jsonContent), 0o644)
				Expect(err).NotTo(HaveOccurred())
				err = os.WriteFile(filepath.Join(tempDir, "md-agent.md"), []byte(mdContent), 0o644)
				Expect(err).NotTo(HaveOccurred())

				err = registry.Discover(tempDir)

				Expect(err).NotTo(HaveOccurred())
				list := registry.List()
				Expect(list).To(HaveLen(2))
			})

			It("skips invalid manifests and continues loading", func() {
				validContent := `{"id": "valid-agent", "name": "Valid Agent"}`
				invalidContent := `{invalid json`
				err := os.WriteFile(filepath.Join(tempDir, "valid.json"), []byte(validContent), 0o644)
				Expect(err).NotTo(HaveOccurred())
				err = os.WriteFile(filepath.Join(tempDir, "invalid.json"), []byte(invalidContent), 0o644)
				Expect(err).NotTo(HaveOccurred())

				err = registry.Discover(tempDir)

				Expect(err).NotTo(HaveOccurred())
				list := registry.List()
				Expect(list).To(HaveLen(1))
				Expect(list[0].ID).To(Equal("valid-agent"))
			})

			It("skips manifests failing validation", func() {
				noIDContent := `{"name": "No ID Agent"}`
				validContent := `{"id": "valid-agent", "name": "Valid Agent"}`
				err := os.WriteFile(filepath.Join(tempDir, "no-id.json"), []byte(noIDContent), 0o644)
				Expect(err).NotTo(HaveOccurred())
				err = os.WriteFile(filepath.Join(tempDir, "valid.json"), []byte(validContent), 0o644)
				Expect(err).NotTo(HaveOccurred())

				err = registry.Discover(tempDir)

				Expect(err).NotTo(HaveOccurred())
				list := registry.List()
				Expect(list).To(HaveLen(1))
				Expect(list[0].ID).To(Equal("valid-agent"))
			})

			It("clears existing manifests on rediscovery", func() {
				registry.Register(&agent.AgentManifest{ID: "existing", Name: "Existing"})
				content := `{"id": "new-agent", "name": "New Agent"}`
				err := os.WriteFile(filepath.Join(tempDir, "new.json"), []byte(content), 0o644)
				Expect(err).NotTo(HaveOccurred())

				err = registry.Discover(tempDir)

				Expect(err).NotTo(HaveOccurred())
				_, exists := registry.Get("existing")
				Expect(exists).To(BeFalse())
				_, exists = registry.Get("new-agent")
				Expect(exists).To(BeTrue())
			})
		})

		Context("with invalid directory", func() {
			It("returns error for nonexistent directory", func() {
				err := registry.Discover("/nonexistent/path")

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("stat agent directory"))
			})

			It("returns error for file path instead of directory", func() {
				filePath := filepath.Join(tempDir, "file.txt")
				err := os.WriteFile(filePath, []byte("content"), 0o644)
				Expect(err).NotTo(HaveOccurred())

				err = registry.Discover(filePath)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("is not a directory"))
			})
		})

		Context("with empty directory", func() {
			It("returns no error and empty registry", func() {
				err := registry.Discover(tempDir)

				Expect(err).NotTo(HaveOccurred())
				Expect(registry.List()).To(BeEmpty())
			})
		})
	})
})
