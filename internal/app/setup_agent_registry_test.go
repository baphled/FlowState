package app_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/config"
)

var _ = Describe("SetupAgentRegistryForTest", func() {
	var (
		dir1    string
		dir2    string
		tempDir string
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "setup-agent-registry-test")
		Expect(err).NotTo(HaveOccurred())

		dir1 = filepath.Join(tempDir, "dir1")
		dir2 = filepath.Join(tempDir, "dir2")
		Expect(os.MkdirAll(dir1, 0o755)).To(Succeed())
		Expect(os.MkdirAll(dir2, 0o755)).To(Succeed())
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Context("when AgentDirs overrides an agent from AgentDir", func() {
		It("uses the agent from AgentDirs when IDs clash", func() {
			bundled := "---\nid: planner\nname: Bundled Planner\nschema_version: \"1\"\n---\nbundled instructions\n"
			Expect(os.WriteFile(filepath.Join(dir1, "planner.md"), []byte(bundled), 0o600)).To(Succeed())

			user := "---\nid: planner\nname: User Planner\nschema_version: \"1\"\n---\nuser instructions\n"
			Expect(os.WriteFile(filepath.Join(dir2, "planner.md"), []byte(user), 0o600)).To(Succeed())

			cfg := &config.AppConfig{
				AgentDir:  dir1,
				AgentDirs: []string{dir2},
			}

			registry := app.SetupAgentRegistryForTest(cfg)

			manifest, ok := registry.Get("planner")
			Expect(ok).To(BeTrue())
			Expect(manifest.Name).To(Equal("User Planner"))
		})

		It("includes agents unique to AgentDirs", func() {
			bundled := "---\nid: planner\nname: Bundled Planner\nschema_version: \"1\"\n---\n"
			Expect(os.WriteFile(filepath.Join(dir1, "planner.md"), []byte(bundled), 0o600)).To(Succeed())

			custom := "---\nid: custom-agent\nname: Custom Agent\nschema_version: \"1\"\n---\n"
			Expect(os.WriteFile(filepath.Join(dir2, "custom-agent.md"), []byte(custom), 0o600)).To(Succeed())

			cfg := &config.AppConfig{
				AgentDir:  dir1,
				AgentDirs: []string{dir2},
			}

			registry := app.SetupAgentRegistryForTest(cfg)

			_, ok := registry.Get("custom-agent")
			Expect(ok).To(BeTrue())
		})
	})

	Context("when AgentDirs is empty", func() {
		It("only discovers from AgentDir", func() {
			content := "---\nid: base-agent\nname: Base Agent\nschema_version: \"1\"\n---\n"
			Expect(os.WriteFile(filepath.Join(dir1, "base-agent.md"), []byte(content), 0o600)).To(Succeed())

			cfg := &config.AppConfig{
				AgentDir:  dir1,
				AgentDirs: nil,
			}

			registry := app.SetupAgentRegistryForTest(cfg)

			_, ok := registry.Get("base-agent")
			Expect(ok).To(BeTrue())
			Expect(registry.List()).To(HaveLen(1))
		})
	})
})
