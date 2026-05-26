package toolset_test

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/pathguard"
	todotool "github.com/baphled/flowstate/internal/tool/todo"
	"github.com/baphled/flowstate/internal/tool/toolset"
)

var _ = Describe("BuildAppTools", func() {
	It("returns the canonical base slice in order", func() {
		loader := skill.NewFileSkillLoader("")
		todos := todotool.NewMemoryStore()

		tools := toolset.BuildAppTools(loader, todos, "/tmp/plans", nil)

		Expect(tools).To(HaveLen(9))
		names := make([]string, 0, len(tools))
		for _, t := range tools {
			names = append(names, t.Name())
		}
		Expect(names).To(Equal([]string{
			"bash",
			"read",
			"write",
			"web",
			"skill_load",
			"todowrite",
			"todo_update",
			"plan_list",
			"plan_read",
		}))
	})

	// Pre-existing wiring bug surfaced by live verification of cbe4464e:
	// BuildAppTools constructed write.New() / read.New() / bash.New()
	// with no pathguard, leaving the main engine's mutating-FS tools
	// silently un-guarded. The default-assistant session's pathguard
	// overlay (Plan-mode + permissions.yaml) was never reached because
	// CheckForTool is called from each tool's Execute only when
	// t.guard != nil. The fix threads a pathguard.Guard through
	// BuildAppTools so the wired tool instances honour pathguard
	// decisions end-to-end.
	Context("when a pathguard.Guard is supplied", func() {
		var (
			deniedRoot string
			deniedPath string
			ctx        context.Context
		)

		BeforeEach(func() {
			var err error
			deniedRoot, err = os.MkdirTemp("", "buildapptools-guard-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(deniedRoot) })
			deniedPath = filepath.Join(deniedRoot, "escape.md")
			ctx = context.Background()
		})

		It("returns a write tool that DENIES writes under a guarded root", func() {
			loader := skill.NewFileSkillLoader("")
			todos := todotool.NewMemoryStore()
			guard := pathguard.New([]string{deniedRoot})

			tools := toolset.BuildAppTools(loader, todos, "/tmp/plans", guard)

			var writeTool tool.Tool
			for _, t := range tools {
				if t.Name() == "write" {
					writeTool = t
					break
				}
			}
			Expect(writeTool).NotTo(BeNil(), "write tool must be present in BuildAppTools slice")

			result, err := writeTool.Execute(ctx, tool.Input{
				Name: "write",
				Arguments: map[string]interface{}{
					"path":    deniedPath,
					"content": "bypass attempt",
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).To(HaveOccurred(), "pathguard denied root should block the write")
			_, statErr := os.Stat(deniedPath)
			Expect(os.IsNotExist(statErr)).To(BeTrue(), "no file should have been created on disk")
		})

		It("returns a read tool that DENIES reads under a guarded root", func() {
			// Seed a file under the denied root so a guard-less read
			// would succeed — proving the guard is the reason the
			// read is rejected, not absence of the file.
			Expect(os.WriteFile(deniedPath, []byte("sentinel"), 0o600)).To(Succeed())

			loader := skill.NewFileSkillLoader("")
			todos := todotool.NewMemoryStore()
			guard := pathguard.New([]string{deniedRoot})

			tools := toolset.BuildAppTools(loader, todos, "/tmp/plans", guard)

			var readTool tool.Tool
			for _, t := range tools {
				if t.Name() == "read" {
					readTool = t
					break
				}
			}
			Expect(readTool).NotTo(BeNil(), "read tool must be present in BuildAppTools slice")

			result, err := readTool.Execute(ctx, tool.Input{
				Name: "read",
				Arguments: map[string]interface{}{
					"path": deniedPath,
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).To(HaveOccurred(), "pathguard denied root should block the read")
		})

		It("returns a bash tool that DENIES commands referencing a guarded root", func() {
			loader := skill.NewFileSkillLoader("")
			todos := todotool.NewMemoryStore()
			guard := pathguard.New([]string{deniedRoot})

			tools := toolset.BuildAppTools(loader, todos, "/tmp/plans", guard)

			var bashTool tool.Tool
			for _, t := range tools {
				if t.Name() == "bash" {
					bashTool = t
					break
				}
			}
			Expect(bashTool).NotTo(BeNil(), "bash tool must be present in BuildAppTools slice")

			result, err := bashTool.Execute(ctx, tool.Input{
				Name: "bash",
				Arguments: map[string]interface{}{
					"command": "cat " + deniedPath,
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).To(HaveOccurred(), "pathguard denied root should block bash referencing it")
		})
	})
})

var _ = Describe("AppendSwarmTools", func() {
	Context("when registry is nil", func() {
		It("returns the base slice unchanged", func() {
			base := []tool.Tool{}
			Expect(toolset.AppendSwarmTools(base, nil)).To(BeEmpty())
		})
	})

	Context("when registry is non-nil", func() {
		It("appends swarm_list, swarm_info, swarm_validate", func() {
			base := []tool.Tool{}
			reg := &swarm.Registry{}

			result := toolset.AppendSwarmTools(base, reg)

			Expect(result).To(HaveLen(3))
			names := []string{}
			for _, t := range result {
				names = append(names, t.Name())
			}
			Expect(names).To(ConsistOf("swarm_list", "swarm_info", "swarm_validate"))
		})
	})
})

var _ = Describe("AppendMemoryTools", func() {
	Context("when memory client is nil", func() {
		It("returns the base slice unchanged", func() {
			Expect(toolset.AppendMemoryTools([]tool.Tool{}, nil)).To(BeEmpty())
		})
	})
})

var _ = Describe("AppendVaultTools", func() {
	Context("when handler is nil", func() {
		It("returns the base slice unchanged", func() {
			Expect(toolset.AppendVaultTools([]tool.Tool{}, nil)).To(BeEmpty())
		})
	})
})

var _ = Describe("AppendVaultIndexTools", func() {
	Context("when cfg is nil", func() {
		It("returns the base slice unchanged", func() {
			Expect(toolset.AppendVaultIndexTools([]tool.Tool{}, nil)).To(BeEmpty())
		})
	})

	Context("when VaultPath is empty", func() {
		It("returns the base slice unchanged", func() {
			cfg := &config.AppConfig{}
			cfg.Qdrant.URL = "http://localhost:6333"
			Expect(toolset.AppendVaultIndexTools([]tool.Tool{}, cfg)).To(BeEmpty())
		})
	})

	Context("when Qdrant URL is empty", func() {
		It("returns the base slice unchanged", func() {
			cfg := &config.AppConfig{VaultPath: "/tmp/vault"}
			Expect(toolset.AppendVaultIndexTools([]tool.Tool{}, cfg)).To(BeEmpty())
		})
	})

	Context("when both VaultPath and Qdrant URL are configured", func() {
		It("appends vault_index and vault_sync", func() {
			cfg := &config.AppConfig{
				VaultPath:       "/tmp/vault",
				VaultCollection: "test-collection",
			}
			cfg.Qdrant.URL = "http://localhost:6333"

			result := toolset.AppendVaultIndexTools([]tool.Tool{}, cfg)

			Expect(result).To(HaveLen(2))
			names := []string{}
			for _, t := range result {
				names = append(names, t.Name())
			}
			Expect(names).To(ConsistOf("vault_index", "vault_sync"))
		})

		It("falls back to the default vault collection when none is configured", func() {
			cfg := &config.AppConfig{VaultPath: "/tmp/vault"}
			cfg.Qdrant.URL = "http://localhost:6333"

			result := toolset.AppendVaultIndexTools([]tool.Tool{}, cfg)

			Expect(result).To(HaveLen(2))
			Expect(toolset.DefaultVaultCollection).To(Equal("flowstate-vault"))
		})
	})
})

var _ = Describe("AppendChainTools", func() {
	Context("when chain store is nil", func() {
		It("returns the base slice unchanged", func() {
			Expect(toolset.AppendChainTools([]tool.Tool{}, nil)).To(BeEmpty())
		})
	})
})
