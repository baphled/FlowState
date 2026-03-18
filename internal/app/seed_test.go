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
			"agents/general.json":    &fstest.MapFile{Data: []byte(`{"id": "general", "name": "General"}`)},
			"agents/coder.json":      &fstest.MapFile{Data: []byte(`{"id": "coder", "name": "Coder"}`)},
			"agents/researcher.json": &fstest.MapFile{Data: []byte(`{"id": "researcher", "name": "Researcher"}`)},
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

			content, err := os.ReadFile(filepath.Join(agentsDest, "general.json"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(ContainSubstring(`"id": "general"`))
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
		It("does not overwrite existing files", func() {
			agentsDest := filepath.Join(destDir, "agents")
			Expect(os.MkdirAll(agentsDest, 0o755)).To(Succeed())

			existingContent := `{"id": "general", "name": "Custom General"}`
			Expect(os.WriteFile(filepath.Join(agentsDest, "general.json"), []byte(existingContent), 0o600)).To(Succeed())

			err := app.SeedAgentsDir(srcFS, agentsDest)

			Expect(err).NotTo(HaveOccurred())

			content, err := os.ReadFile(filepath.Join(agentsDest, "general.json"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(Equal(existingContent))
		})

		It("copies missing files without touching existing ones", func() {
			agentsDest := filepath.Join(destDir, "agents")
			Expect(os.MkdirAll(agentsDest, 0o755)).To(Succeed())

			existingContent := `{"id": "general", "name": "Custom General"}`
			Expect(os.WriteFile(filepath.Join(agentsDest, "general.json"), []byte(existingContent), 0o600)).To(Succeed())

			err := app.SeedAgentsDir(srcFS, agentsDest)

			Expect(err).NotTo(HaveOccurred())

			entries, err := os.ReadDir(agentsDest)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(HaveLen(3))

			content, err := os.ReadFile(filepath.Join(agentsDest, "general.json"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(Equal(existingContent))

			content, err = os.ReadFile(filepath.Join(agentsDest, "coder.json"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(ContainSubstring(`"id": "coder"`))
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
