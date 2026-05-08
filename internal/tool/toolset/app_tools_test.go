package toolset_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tool"
	todotool "github.com/baphled/flowstate/internal/tool/todo"
	"github.com/baphled/flowstate/internal/tool/toolset"
)

var _ = Describe("BuildAppTools", func() {
	It("returns the canonical base slice in order", func() {
		loader := skill.NewFileSkillLoader("")
		todos := todotool.NewMemoryStore()

		tools := toolset.BuildAppTools(loader, todos, "/tmp/plans")

		Expect(tools).To(HaveLen(8))
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
			"plan_list",
			"plan_read",
		}))
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
