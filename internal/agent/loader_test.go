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

	// LoadAndValidateManifest is the manifest-gate primitive that the
	// autoresearch harness invokes between candidate-edit and scoring
	// (plan § 5.5 N13 / § 4.7). It composes LoadManifest's parse step
	// with Manifest.Validate's required-field check so the harness has a
	// single call site that yields one of three signals: success, parse
	// failure, validation failure. A validation failure on a candidate
	// is a reverted trial — not a hard error of the harness — so the
	// caller distinguishes the two failure modes by the error path.
	Describe("LoadAndValidateManifest", func() {
		Context("when the manifest parses and validates", func() {
			It("returns the loaded manifest with no error", func() {
				jsonPath := filepath.Join(tempDir, "valid-agent.json")
				jsonContent := `{
					"schema_version": "1",
					"id": "valid-agent",
					"name": "Valid Agent",
					"complexity": "standard",
					"metadata": {"role": "Test role"}
				}`
				Expect(os.WriteFile(jsonPath, []byte(jsonContent), 0o600)).To(Succeed())

				m, err := agent.LoadAndValidateManifest(jsonPath)

				Expect(err).NotTo(HaveOccurred())
				Expect(m).NotTo(BeNil())
				Expect(m.ID).To(Equal("valid-agent"))
				Expect(m.Name).To(Equal("Valid Agent"))
			})

			It("loads markdown manifests with derived id/name through the same call", func() {
				mdPath := filepath.Join(tempDir, "md-valid.md")
				mdContent := "---\ndescription: Markdown agent\ndefault_skills:\n  - skill1\n---\nbody\n"
				Expect(os.WriteFile(mdPath, []byte(mdContent), 0o600)).To(Succeed())

				m, err := agent.LoadAndValidateManifest(mdPath)

				Expect(err).NotTo(HaveOccurred())
				Expect(m).NotTo(BeNil())
				Expect(m.ID).To(Equal("md-valid"))
				Expect(m.Name).To(Equal("md-valid"))
			})
		})

		Context("when the manifest cannot be parsed", func() {
			It("returns a parse error and a nil manifest for malformed JSON", func() {
				jsonPath := filepath.Join(tempDir, "broken.json")
				Expect(os.WriteFile(jsonPath, []byte("{not valid json"), 0o600)).To(Succeed())

				m, err := agent.LoadAndValidateManifest(jsonPath)

				Expect(err).To(HaveOccurred())
				Expect(m).To(BeNil())
				Expect(err.Error()).To(ContainSubstring("parsing JSON"))
			})

			It("returns a parse error for malformed markdown frontmatter", func() {
				mdPath := filepath.Join(tempDir, "broken.md")
				// Missing closing --- delimiter — extractFrontmatter rejects this.
				Expect(os.WriteFile(mdPath, []byte("---\nid: broken\n"), 0o600)).To(Succeed())

				m, err := agent.LoadAndValidateManifest(mdPath)

				Expect(err).To(HaveOccurred())
				Expect(m).To(BeNil())
				Expect(err.Error()).To(ContainSubstring("frontmatter"))
			})

			It("returns an error when the file does not exist", func() {
				m, err := agent.LoadAndValidateManifest(filepath.Join(tempDir, "missing.json"))

				Expect(err).To(HaveOccurred())
				Expect(m).To(BeNil())
			})
		})

		Context("when the manifest parses but fails Validate", func() {
			It("returns a ValidationError so the caller can record manifest-validate-failed", func() {
				// JSON loader runs validateContextManagement and applyDefaults
				// before returning, but Manifest.Validate still rejects on the
				// required-id contract because the JSON loader does not derive
				// an id from the file name (only the markdown loader does).
				jsonPath := filepath.Join(tempDir, "no-id.json")
				jsonContent := `{
					"schema_version": "1",
					"name": "Has Name But No ID",
					"complexity": "standard",
					"metadata": {"role": "x"}
				}`
				Expect(os.WriteFile(jsonPath, []byte(jsonContent), 0o600)).To(Succeed())

				m, err := agent.LoadAndValidateManifest(jsonPath)

				Expect(err).To(HaveOccurred())
				Expect(m).To(BeNil())
				var validationErr *agent.ValidationError
				Expect(err).To(BeAssignableToTypeOf(validationErr))
				Expect(err.Error()).To(ContainSubstring("id"))
			})

			It("returns a ValidationError when colour is malformed", func() {
				jsonPath := filepath.Join(tempDir, "bad-colour.json")
				jsonContent := `{
					"schema_version": "1",
					"id": "bad-colour",
					"name": "Bad Colour Agent",
					"color": "teal",
					"complexity": "standard",
					"metadata": {"role": "x"}
				}`
				Expect(os.WriteFile(jsonPath, []byte(jsonContent), 0o600)).To(Succeed())

				m, err := agent.LoadAndValidateManifest(jsonPath)

				Expect(err).To(HaveOccurred())
				Expect(m).To(BeNil())
				var validationErr *agent.ValidationError
				Expect(err).To(BeAssignableToTypeOf(validationErr))
				Expect(err.Error()).To(ContainSubstring("color"))
			})
		})

		Context("when the file extension is unsupported", func() {
			It("surfaces the LoadManifest error verbatim", func() {
				txtPath := filepath.Join(tempDir, "agent.txt")
				Expect(os.WriteFile(txtPath, []byte("ignored"), 0o600)).To(Succeed())

				m, err := agent.LoadAndValidateManifest(txtPath)

				Expect(err).To(HaveOccurred())
				Expect(m).To(BeNil())
				Expect(err.Error()).To(ContainSubstring("unsupported file type"))
			})
		})
	})
})
