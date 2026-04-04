package app_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/config"
)

var _ = Describe("MCP server merge integration", Label("integration"), func() {
	Context("when merging auto-discovered servers with an empty configured list", func() {
		It("returns all discovered servers", func() {
			discovered := []config.MCPServerConfig{
				{Name: "memory", Command: "mcp-mem0-server", Enabled: true},
				{Name: "vault-rag", Command: "mcp-vault-server", Enabled: true},
			}

			result := app.MergeMCPServersForTest(nil, discovered)

			Expect(result).To(HaveLen(2))
			Expect(result[0].Name).To(Equal("memory"))
			Expect(result[1].Name).To(Equal("vault-rag"))
		})
	})

	Context("when merging with an empty discovered list", func() {
		It("returns all configured servers unchanged", func() {
			configured := []config.MCPServerConfig{
				{Name: "filesystem", Command: "npx", Enabled: true},
			}

			result := app.MergeMCPServersForTest(configured, nil)

			Expect(result).To(HaveLen(1))
			Expect(result[0].Name).To(Equal("filesystem"))
		})
	})

	Context("when a discovered server name collides with a configured server", func() {
		It("configured server's command wins on name collision", func() {
			configured := []config.MCPServerConfig{
				{Name: "memory", Command: "custom-memory-server", Enabled: true},
			}
			discovered := []config.MCPServerConfig{
				{Name: "memory", Command: "mcp-mem0-server", Enabled: true},
			}

			result := app.MergeMCPServersForTest(configured, discovered)

			Expect(result).To(HaveLen(1))
			Expect(result[0].Command).To(Equal("custom-memory-server"))
		})

		It("does not duplicate a server present in both lists", func() {
			configured := []config.MCPServerConfig{
				{Name: "memory", Command: "custom-memory-server", Enabled: true},
			}
			discovered := []config.MCPServerConfig{
				{Name: "memory", Command: "mcp-mem0-server", Enabled: true},
			}

			result := app.MergeMCPServersForTest(configured, discovered)

			Expect(result).To(HaveLen(1))
		})
	})

	Context("when auto-discovered servers have Enabled=true", func() {
		It("preserves Enabled=true from discovered servers", func() {
			discovered := []config.MCPServerConfig{
				{Name: "memory", Command: "mcp-mem0-server", Enabled: true},
			}

			result := app.MergeMCPServersForTest(nil, discovered)

			Expect(result[0].Enabled).To(BeTrue())
		})
	})

	Context("when configured server has Enabled=false", func() {
		It("explicit Enabled=false takes precedence over discovered server", func() {
			configured := []config.MCPServerConfig{
				{Name: "memory", Command: "mcp-mem0-server", Enabled: false},
			}
			discovered := []config.MCPServerConfig{
				{Name: "memory", Command: "mcp-mem0-server", Enabled: true},
			}

			result := app.MergeMCPServersForTest(configured, discovered)

			Expect(result).To(HaveLen(1))
			Expect(result[0].Enabled).To(BeFalse())
		})
	})

	Context("when merging distinct servers from both lists", func() {
		It("appends discovered servers not present in configured list", func() {
			configured := []config.MCPServerConfig{
				{Name: "filesystem", Command: "npx", Enabled: true},
			}
			discovered := []config.MCPServerConfig{
				{Name: "memory", Command: "mcp-mem0-server", Enabled: true},
				{Name: "vault-rag", Command: "mcp-vault-server", Enabled: true},
			}

			result := app.MergeMCPServersForTest(configured, discovered)

			Expect(result).To(HaveLen(3))
			names := make([]string, 0, len(result))
			for _, s := range result {
				names = append(names, s.Name)
			}
			Expect(names).To(ContainElements("filesystem", "memory", "vault-rag"))
		})
	})
})
