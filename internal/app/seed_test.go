package app_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing/fstest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
)

var _ = Describe("SeedAgentsDir", func() {
	var (
		destDir string
		srcFS   fs.FS
	)

	BeforeEach(func() {
		var err error
		destDir, err = os.MkdirTemp("", "seed-test")
		Expect(err).NotTo(HaveOccurred())

		srcFS = fstest.MapFS{
			"agents/general.md":    &fstest.MapFile{Data: []byte("---\nid: general\nname: General\n---\n")},
			"agents/coder.md":      &fstest.MapFile{Data: []byte("---\nid: coder\nname: Coder\n---\n")},
			"agents/researcher.md": &fstest.MapFile{Data: []byte("---\nid: researcher\nname: Researcher\n---\n")},
		}
	})

	AfterEach(func() {
		os.RemoveAll(destDir)
	})

	Context("when destination directory is empty", func() {
		It("copies all agent files from source", func() {
			agentsDest := filepath.Join(destDir, "agents")
			Expect(os.MkdirAll(agentsDest, 0o755)).To(Succeed())

			err := app.SeedAgentsDir(srcFS, agentsDest)

			Expect(err).NotTo(HaveOccurred())

			entries, err := os.ReadDir(agentsDest)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(HaveLen(3))

			content, err := os.ReadFile(filepath.Join(agentsDest, "general.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(ContainSubstring("id: general"))
		})
	})

	Context("when destination directory does not exist", func() {
		It("creates the directory and copies files", func() {
			agentsDest := filepath.Join(destDir, "nonexistent", "agents")

			err := app.SeedAgentsDir(srcFS, agentsDest)

			Expect(err).NotTo(HaveOccurred())

			entries, err := os.ReadDir(agentsDest)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(HaveLen(3))
		})
	})

	Context("when destination directory already has files", func() {
		It("preserves existing files and does not overwrite them", func() {
			agentsDest := filepath.Join(destDir, "agents")
			Expect(os.MkdirAll(agentsDest, 0o755)).To(Succeed())

			customContent := "---\nid: general\nname: My Custom General\n---\n"
			Expect(os.WriteFile(filepath.Join(agentsDest, "general.md"), []byte(customContent), 0o600)).To(Succeed())

			err := app.SeedAgentsDir(srcFS, agentsDest)

			Expect(err).NotTo(HaveOccurred())

			content, err := os.ReadFile(filepath.Join(agentsDest, "general.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(ContainSubstring("My Custom General"))
		})

		It("copies missing files while preserving existing ones", func() {
			agentsDest := filepath.Join(destDir, "agents")
			Expect(os.MkdirAll(agentsDest, 0o755)).To(Succeed())

			customContent := "---\nid: general\nname: My Custom General\n---\n"
			Expect(os.WriteFile(filepath.Join(agentsDest, "general.md"), []byte(customContent), 0o600)).To(Succeed())

			err := app.SeedAgentsDir(srcFS, agentsDest)

			Expect(err).NotTo(HaveOccurred())

			entries, err := os.ReadDir(agentsDest)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(HaveLen(3))

			content, err := os.ReadFile(filepath.Join(agentsDest, "general.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(ContainSubstring("My Custom General"))

			content, err = os.ReadFile(filepath.Join(agentsDest, "coder.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(ContainSubstring("id: coder"))
		})

		It("preserves a custom agent file that has no embedded counterpart", func() {
			agentsDest := filepath.Join(destDir, "agents")
			Expect(os.MkdirAll(agentsDest, 0o755)).To(Succeed())

			customContent := "---\nid: my-custom\nname: My Custom Agent\n---\n"
			Expect(os.WriteFile(filepath.Join(agentsDest, "my-custom.md"), []byte(customContent), 0o600)).To(Succeed())

			err := app.SeedAgentsDir(srcFS, agentsDest)

			Expect(err).NotTo(HaveOccurred())

			content, err := os.ReadFile(filepath.Join(agentsDest, "my-custom.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(ContainSubstring("My Custom Agent"))
		})
	})

	Context("when source FS has no agents directory", func() {
		It("returns an error", func() {
			emptyFS := fstest.MapFS{}
			agentsDest := filepath.Join(destDir, "agents")

			err := app.SeedAgentsDir(emptyFS, agentsDest)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("agents"))
		})
	})
})

var _ = Describe("EmbeddedAgentsFS", func() {
	Context("when calling EmbeddedAgentsFS", func() {
		It("returns a valid fs.FS", func() {
			embeddedFS := app.EmbeddedAgentsFS()

			Expect(embeddedFS).NotTo(BeNil())
		})

		It("contains planner.md", func() {
			embeddedFS := app.EmbeddedAgentsFS()

			agentsDir, err := fs.Sub(embeddedFS, "agents")
			Expect(err).NotTo(HaveOccurred())

			plannerContent, err := fs.ReadFile(agentsDir, "planner.md")
			Expect(err).NotTo(HaveOccurred())
			Expect(string(plannerContent)).To(ContainSubstring("id: planner"))
		})

		It("contains executor.md", func() {
			embeddedFS := app.EmbeddedAgentsFS()

			agentsDir, err := fs.Sub(embeddedFS, "agents")
			Expect(err).NotTo(HaveOccurred())

			executorContent, err := fs.ReadFile(agentsDir, "executor.md")
			Expect(err).NotTo(HaveOccurred())
			Expect(string(executorContent)).To(ContainSubstring("id: executor"))
		})

		It("can seed all manifests on first run", func() {
			destDir, err := os.MkdirTemp("", "embedded-seed-test")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(destDir) })

			embeddedFS := app.EmbeddedAgentsFS()
			err = app.SeedAgentsDir(embeddedFS, destDir)

			Expect(err).NotTo(HaveOccurred())

			entries, err := os.ReadDir(destDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(HaveLen(7))

			plannerContent, err := os.ReadFile(filepath.Join(destDir, "planner.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(plannerContent)).To(ContainSubstring("id: planner"))

			executorContent, err := os.ReadFile(filepath.Join(destDir, "executor.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(executorContent)).To(ContainSubstring("id: executor"))
		})
	})
})
