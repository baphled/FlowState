package agent_test

import (
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
)

var _ = Describe("Registry", func() {
	var (
		registry *agent.Registry
		tempDir  string
	)

	BeforeEach(func() {
		registry = agent.NewRegistry()
		var err error
		tempDir, err = os.MkdirTemp("", "agent-registry-test")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Describe("NewRegistry", func() {
		It("creates an empty registry", func() {
			Expect(registry).NotTo(BeNil())
			Expect(registry.List()).To(BeEmpty())
		})
	})

	Describe("Register", func() {
		It("adds a manifest to the registry", func() {
			manifest := &agent.Manifest{
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
			manifest1 := &agent.Manifest{
				ID:   "test-agent",
				Name: "Original Name",
			}
			manifest2 := &agent.Manifest{
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
			manifest := &agent.Manifest{
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
			manifests := []*agent.Manifest{
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

	Describe("GetByNameOrAlias", func() {
		Context("when the name matches an exact ID", func() {
			It("returns the manifest", func() {
				manifest := &agent.Manifest{
					ID:      "explorer",
					Name:    "Explorer Agent",
					Aliases: []string{"investigation", "research"},
				}
				registry.Register(manifest)

				result, ok := registry.GetByNameOrAlias("explorer")

				Expect(ok).To(BeTrue())
				Expect(result.ID).To(Equal("explorer"))
			})
		})

		Context("when the name matches an ID case-insensitively", func() {
			It("returns the manifest", func() {
				manifest := &agent.Manifest{
					ID:      "explorer",
					Name:    "Explorer Agent",
					Aliases: []string{"investigation"},
				}
				registry.Register(manifest)

				result, ok := registry.GetByNameOrAlias("Explorer")

				Expect(ok).To(BeTrue())
				Expect(result.ID).To(Equal("explorer"))
			})
		})

		Context("when the name matches an alias case-insensitively", func() {
			It("returns the manifest", func() {
				manifest := &agent.Manifest{
					ID:      "explorer",
					Name:    "Explorer Agent",
					Aliases: []string{"exploration", "investigation"},
				}
				registry.Register(manifest)

				result, ok := registry.GetByNameOrAlias("Investigation")

				Expect(ok).To(BeTrue())
				Expect(result.ID).To(Equal("explorer"))
			})
		})

		Context("when the name does not match any agent", func() {
			It("returns nil and false", func() {
				manifest := &agent.Manifest{
					ID:      "explorer",
					Name:    "Explorer Agent",
					Aliases: []string{"investigation"},
				}
				registry.Register(manifest)

				result, ok := registry.GetByNameOrAlias("nonexistent")

				Expect(ok).To(BeFalse())
				Expect(result).To(BeNil())
			})
		})

		Context("when exact ID and alias both could match", func() {
			It("prefers the exact ID match", func() {
				agentA := &agent.Manifest{
					ID:      "search",
					Name:    "Search Agent",
					Aliases: []string{"find"},
				}
				agentB := &agent.Manifest{
					ID:      "finder",
					Name:    "Finder Agent",
					Aliases: []string{"search", "locate"},
				}
				registry.Register(agentA)
				registry.Register(agentB)

				result, ok := registry.GetByNameOrAlias("search")

				Expect(ok).To(BeTrue())
				Expect(result.ID).To(Equal("search"))
			})
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
				err := os.WriteFile(filepath.Join(tempDir, "json-agent.json"), []byte(jsonContent), 0o600)
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
				err := os.WriteFile(filepath.Join(tempDir, "md-agent.md"), []byte(mdContent), 0o600)
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
				err := os.WriteFile(filepath.Join(tempDir, "json-agent.json"), []byte(jsonContent), 0o600)
				Expect(err).NotTo(HaveOccurred())
				err = os.WriteFile(filepath.Join(tempDir, "md-agent.md"), []byte(mdContent), 0o600)
				Expect(err).NotTo(HaveOccurred())

				err = registry.Discover(tempDir)

				Expect(err).NotTo(HaveOccurred())
				list := registry.List()
				Expect(list).To(HaveLen(2))
			})

			It("skips invalid manifests and continues loading", func() {
				validContent := `{"id": "valid-agent", "name": "Valid Agent"}`
				invalidContent := `{invalid json`
				err := os.WriteFile(filepath.Join(tempDir, "valid.json"), []byte(validContent), 0o600)
				Expect(err).NotTo(HaveOccurred())
				err = os.WriteFile(filepath.Join(tempDir, "invalid.json"), []byte(invalidContent), 0o600)
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
				err := os.WriteFile(filepath.Join(tempDir, "no-id.json"), []byte(noIDContent), 0o600)
				Expect(err).NotTo(HaveOccurred())
				err = os.WriteFile(filepath.Join(tempDir, "valid.json"), []byte(validContent), 0o600)
				Expect(err).NotTo(HaveOccurred())

				err = registry.Discover(tempDir)

				Expect(err).NotTo(HaveOccurred())
				list := registry.List()
				Expect(list).To(HaveLen(1))
				Expect(list[0].ID).To(Equal("valid-agent"))
			})

			It("clears existing manifests on rediscovery", func() {
				registry.Register(&agent.Manifest{ID: "existing", Name: "Existing"})
				content := `{"id": "new-agent", "name": "New Agent"}`
				err := os.WriteFile(filepath.Join(tempDir, "new.json"), []byte(content), 0o600)
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
				err := os.WriteFile(filePath, []byte("content"), 0o600)
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

	Describe("DiscoverMerge", func() {
		Context("merging into an empty registry", func() {
			It("adds agents from the directory", func() {
				content := `{"schema_version": "1", "id": "new-agent", "name": "New Agent"}`
				err := os.WriteFile(filepath.Join(tempDir, "new-agent.json"), []byte(content), 0o600)
				Expect(err).NotTo(HaveOccurred())

				err = registry.DiscoverMerge(tempDir)

				Expect(err).NotTo(HaveOccurred())
				manifest, ok := registry.Get("new-agent")
				Expect(ok).To(BeTrue())
				Expect(manifest.Name).To(Equal("New Agent"))
			})
		})

		Context("merging into a registry with existing agents", func() {
			BeforeEach(func() {
				registry.Register(&agent.Manifest{ID: "existing", Name: "Existing Agent"})
			})

			It("adds new agents without losing existing ones", func() {
				content := `{"schema_version": "1", "id": "beta", "name": "Beta Agent"}`
				err := os.WriteFile(filepath.Join(tempDir, "beta.json"), []byte(content), 0o600)
				Expect(err).NotTo(HaveOccurred())

				err = registry.DiscoverMerge(tempDir)

				Expect(err).NotTo(HaveOccurred())
				_, existingOK := registry.Get("existing")
				Expect(existingOK).To(BeTrue())
				_, betaOK := registry.Get("beta")
				Expect(betaOK).To(BeTrue())
			})

			It("replaces an agent with the same ID so the merged entry wins", func() {
				registry.Register(&agent.Manifest{ID: "planner", Name: "Bundled Planner"})

				content := `{"schema_version": "1", "id": "planner", "name": "User Planner"}`
				err := os.WriteFile(filepath.Join(tempDir, "planner.json"), []byte(content), 0o600)
				Expect(err).NotTo(HaveOccurred())

				err = registry.DiscoverMerge(tempDir)

				Expect(err).NotTo(HaveOccurred())
				manifest, ok := registry.Get("planner")
				Expect(ok).To(BeTrue())
				Expect(manifest.Name).To(Equal("User Planner"))
			})
		})

		Context("with an empty directory", func() {
			It("is a no-op and returns nil", func() {
				registry.Register(&agent.Manifest{ID: "alpha", Name: "Alpha"})

				err := registry.DiscoverMerge(tempDir)

				Expect(err).NotTo(HaveOccurred())
				_, ok := registry.Get("alpha")
				Expect(ok).To(BeTrue())
			})
		})

		Context("with a missing directory", func() {
			It("returns an error satisfying errors.Is(err, ErrAgentDirNotFound)", func() {
				err := registry.DiscoverMerge("/nonexistent/path/for/test")

				Expect(err).To(HaveOccurred())
				Expect(errors.Is(err, agent.ErrAgentDirNotFound)).To(BeTrue())
			})
		})

		Context("with invalid manifests in the directory", func() {
			It("skips them and returns nil", func() {
				invalid := `{invalid json`
				err := os.WriteFile(filepath.Join(tempDir, "invalid.json"), []byte(invalid), 0o600)
				Expect(err).NotTo(HaveOccurred())

				valid := `{"schema_version": "1", "id": "valid-agent", "name": "Valid"}`
				err = os.WriteFile(filepath.Join(tempDir, "valid.json"), []byte(valid), 0o600)
				Expect(err).NotTo(HaveOccurred())

				err = registry.DiscoverMerge(tempDir)

				Expect(err).NotTo(HaveOccurred())
				_, ok := registry.Get("valid-agent")
				Expect(ok).To(BeTrue())
			})
		})

		Context("when both .md and .json exist for the same ID", func() {
			It("uses the .md file (markdown takes precedence)", func() {
				jsonContent := `{"schema_version": "1", "id": "explorer", "name": "Explorer JSON"}`
				err := os.WriteFile(filepath.Join(tempDir, "explorer.json"), []byte(jsonContent), 0o600)
				Expect(err).NotTo(HaveOccurred())

				mdContent := "---\nid: explorer\nname: Explorer Markdown\nschema_version: \"1\"\n---\n"
				err = os.WriteFile(filepath.Join(tempDir, "explorer.md"), []byte(mdContent), 0o600)
				Expect(err).NotTo(HaveOccurred())

				err = registry.DiscoverMerge(tempDir)

				Expect(err).NotTo(HaveOccurred())
				manifest, ok := registry.Get("explorer")
				Expect(ok).To(BeTrue())
				Expect(manifest.Name).To(Equal("Explorer Markdown"))
			})
		})
	})
})
