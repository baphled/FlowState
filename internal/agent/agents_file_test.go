package agent_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
)

var _ = Describe("AgentsFileLoader", func() {
	var (
		configDir  string
		workingDir string
	)

	BeforeEach(func() {
		var err error
		configDir, err = os.MkdirTemp("", "agents-file-config")
		Expect(err).NotTo(HaveOccurred())
		workingDir, err = os.MkdirTemp("", "agents-file-working")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(configDir)
		os.RemoveAll(workingDir)
	})

	Describe("Load", func() {
		Context("when only config dir has AGENTS.md", func() {
			It("returns the global content", func() {
				content := "Global agent instructions"
				err := os.WriteFile(filepath.Join(configDir, "AGENTS.md"), []byte(content), 0o600)
				Expect(err).NotTo(HaveOccurred())

				loader := agent.NewAgentsFileLoader(configDir, workingDir)
				result := loader.Load()

				Expect(result).To(Equal(content))
			})
		})

		Context("when only working dir has AGENTS.md", func() {
			It("returns the local content", func() {
				content := "Project-specific instructions"
				err := os.WriteFile(filepath.Join(workingDir, "AGENTS.md"), []byte(content), 0o600)
				Expect(err).NotTo(HaveOccurred())

				loader := agent.NewAgentsFileLoader(configDir, workingDir)
				result := loader.Load()

				Expect(result).To(Equal(content))
			})
		})

		Context("when both directories have AGENTS.md", func() {
			It("returns merged content separated by divider", func() {
				globalContent := "Global instructions"
				localContent := "Local instructions"
				err := os.WriteFile(filepath.Join(configDir, "AGENTS.md"), []byte(globalContent), 0o600)
				Expect(err).NotTo(HaveOccurred())
				err = os.WriteFile(filepath.Join(workingDir, "AGENTS.md"), []byte(localContent), 0o600)
				Expect(err).NotTo(HaveOccurred())

				loader := agent.NewAgentsFileLoader(configDir, workingDir)
				result := loader.Load()

				expected := globalContent + "\n\n---\n\n" + localContent
				Expect(result).To(Equal(expected))
			})
		})

		Context("when neither file exists", func() {
			It("returns an empty string", func() {
				loader := agent.NewAgentsFileLoader(configDir, workingDir)
				result := loader.Load()

				Expect(result).To(BeEmpty())
			})
		})

		Context("when config dir path is empty", func() {
			It("returns working dir content", func() {
				content := "Working dir content"
				err := os.WriteFile(filepath.Join(workingDir, "AGENTS.md"), []byte(content), 0o600)
				Expect(err).NotTo(HaveOccurred())

				loader := agent.NewAgentsFileLoader("", workingDir)
				result := loader.Load()

				Expect(result).To(Equal(content))
			})
		})

		Context("when both paths point to the same directory", func() {
			It("returns content once without duplication", func() {
				dir, err := os.MkdirTemp("", "agents-file-same")
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func() { os.RemoveAll(dir) })

				content := "Shared instructions"
				err = os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(content), 0o600)
				Expect(err).NotTo(HaveOccurred())

				loader := agent.NewAgentsFileLoader(dir, dir)
				result := loader.Load()

				Expect(result).To(Equal(content))
			})
		})
	})

	Describe("LoadFiles", func() {
		Context("when config dir has AGENTS.md", func() {
			It("returns one InstructionFile with absolute path and content", func() {
				content := "Global agent instructions"
				err := os.WriteFile(filepath.Join(configDir, "AGENTS.md"), []byte(content), 0o600)
				Expect(err).NotTo(HaveOccurred())

				loader := agent.NewAgentsFileLoader(configDir, workingDir)
				files := loader.LoadFiles()

				Expect(files).To(HaveLen(1))
				absPath, pathErr := filepath.Abs(filepath.Join(configDir, "AGENTS.md"))
				Expect(pathErr).NotTo(HaveOccurred())
				Expect(files[0].Path).To(Equal(absPath))
				Expect(files[0].Content).To(Equal(content))
			})
		})

		Context("when both dirs have AGENTS.md", func() {
			It("returns two InstructionFiles in order: config then working", func() {
				globalContent := "Global instructions"
				localContent := "Local instructions"
				err := os.WriteFile(filepath.Join(configDir, "AGENTS.md"), []byte(globalContent), 0o600)
				Expect(err).NotTo(HaveOccurred())
				err = os.WriteFile(filepath.Join(workingDir, "AGENTS.md"), []byte(localContent), 0o600)
				Expect(err).NotTo(HaveOccurred())

				loader := agent.NewAgentsFileLoader(configDir, workingDir)
				files := loader.LoadFiles()

				Expect(files).To(HaveLen(2))

				absConfig, pathErr := filepath.Abs(filepath.Join(configDir, "AGENTS.md"))
				Expect(pathErr).NotTo(HaveOccurred())
				Expect(files[0].Path).To(Equal(absConfig))
				Expect(files[0].Content).To(Equal(globalContent))

				absWorking, pathErr := filepath.Abs(filepath.Join(workingDir, "AGENTS.md"))
				Expect(pathErr).NotTo(HaveOccurred())
				Expect(files[1].Path).To(Equal(absWorking))
				Expect(files[1].Content).To(Equal(localContent))
			})
		})

		Context("when neither file exists", func() {
			It("returns an empty slice", func() {
				loader := agent.NewAgentsFileLoader(configDir, workingDir)
				files := loader.LoadFiles()

				Expect(files).To(BeEmpty())
			})
		})

		Context("when both paths point to the same directory", func() {
			It("returns one InstructionFile without duplication", func() {
				dir, err := os.MkdirTemp("", "agents-file-same")
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func() { os.RemoveAll(dir) })

				content := "Shared instructions"
				err = os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(content), 0o600)
				Expect(err).NotTo(HaveOccurred())

				loader := agent.NewAgentsFileLoader(dir, dir)
				files := loader.LoadFiles()

				Expect(files).To(HaveLen(1))
				Expect(files[0].Content).To(Equal(content))
			})
		})
	})
})
