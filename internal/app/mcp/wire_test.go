package mcp_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appmcp "github.com/baphled/flowstate/internal/app/mcp"
	"github.com/baphled/flowstate/internal/config"
	mcpclient "github.com/baphled/flowstate/internal/mcp"
)

var _ = Describe("MergeServers", func() {
	Context("when discovered list is non-empty and configured list is empty", func() {
		It("returns all discovered servers", func() {
			discovered := []config.MCPServerConfig{
				{Name: "memory", Command: "mcp-mem0-server", Enabled: true},
				{Name: "vault-rag", Command: "mcp-vault-server", Enabled: true},
			}

			result := appmcp.MergeServers(nil, discovered)

			Expect(result).To(HaveLen(2))
			Expect(result[0].Name).To(Equal("memory"))
			Expect(result[1].Name).To(Equal("vault-rag"))
		})
	})

	Context("when discovered list is empty", func() {
		It("returns all configured servers unchanged", func() {
			configured := []config.MCPServerConfig{
				{Name: "filesystem", Command: "npx", Enabled: true},
			}

			result := appmcp.MergeServers(configured, nil)

			Expect(result).To(HaveLen(1))
			Expect(result[0].Name).To(Equal("filesystem"))
		})
	})

	Context("when configured and discovered share a server name", func() {
		It("preserves the configured server's command", func() {
			configured := []config.MCPServerConfig{
				{Name: "memory", Command: "custom-memory-server", Enabled: true},
			}
			discovered := []config.MCPServerConfig{
				{Name: "memory", Command: "mcp-mem0-server", Enabled: true},
			}

			result := appmcp.MergeServers(configured, discovered)

			Expect(result).To(HaveLen(1))
			Expect(result[0].Command).To(Equal("custom-memory-server"))
		})

		It("does not duplicate the server", func() {
			configured := []config.MCPServerConfig{
				{Name: "memory", Command: "custom-memory-server", Enabled: true},
			}
			discovered := []config.MCPServerConfig{
				{Name: "memory", Command: "mcp-mem0-server", Enabled: true},
			}

			result := appmcp.MergeServers(configured, discovered)

			Expect(result).To(HaveLen(1))
		})

		It("preserves the configured Enabled=false flag", func() {
			configured := []config.MCPServerConfig{
				{Name: "memory", Command: "mcp-mem0-server", Enabled: false},
			}
			discovered := []config.MCPServerConfig{
				{Name: "memory", Command: "mcp-mem0-server", Enabled: true},
			}

			result := appmcp.MergeServers(configured, discovered)

			Expect(result).To(HaveLen(1))
			Expect(result[0].Enabled).To(BeFalse())
		})
	})

	Context("when configured and discovered have distinct servers", func() {
		It("returns the union with configured servers first", func() {
			configured := []config.MCPServerConfig{
				{Name: "filesystem", Command: "npx", Enabled: true},
			}
			discovered := []config.MCPServerConfig{
				{Name: "memory", Command: "mcp-mem0-server", Enabled: true},
				{Name: "vault-rag", Command: "mcp-vault-server", Enabled: true},
			}

			result := appmcp.MergeServers(configured, discovered)

			Expect(result).To(HaveLen(3))
			names := make([]string, 0, len(result))
			for _, s := range result {
				names = append(names, s.Name)
			}
			Expect(names).To(ContainElements("filesystem", "memory", "vault-rag"))
			Expect(names[0]).To(Equal("filesystem"))
		})
	})
})

var _ = Describe("ConnectServers", func() {
	var client *fakeMCPClient

	BeforeEach(func() {
		client = newFakeMCPClient()
	})

	It("returns proxy tools for each MCP tool listed by a connected server", func() {
		client.listToolsFn = func(_ context.Context, _ string) ([]mcpclient.ToolInfo, error) {
			return []mcpclient.ToolInfo{
				{Name: "echo", Description: "Echoes input"},
				{Name: "fetch", Description: "Fetches URL"},
			}, nil
		}

		servers := []config.MCPServerConfig{
			{Name: "test-server", Command: "test-cmd", Enabled: true},
		}

		tools, results, names := appmcp.ConnectServers(context.Background(), client, servers)

		Expect(tools).To(HaveLen(2))
		Expect(tools[0].Name()).To(Equal("echo"))
		Expect(tools[1].Name()).To(Equal("fetch"))
		Expect(results).To(HaveLen(1))
		Expect(results[0].Name).To(Equal("test-server"))
		Expect(results[0].Success).To(BeTrue())
		Expect(results[0].ToolCount).To(Equal(2))
		Expect(names).To(HaveKeyWithValue("test-server", []string{"echo", "fetch"}))
	})

	It("skips servers where Enabled is false", func() {
		servers := []config.MCPServerConfig{
			{Name: "disabled-server", Command: "test-cmd", Enabled: false},
		}

		tools, results, _ := appmcp.ConnectServers(context.Background(), client, servers)

		Expect(tools).To(BeEmpty())
		Expect(results).To(BeEmpty())
		Expect(client.connectCalls).To(BeEmpty())
	})

	It("records a failure entry without crashing when Connect returns an error", func() {
		client.connectFn = func(_ context.Context, cfg mcpclient.ServerConfig) error {
			if cfg.Name == "bad-server" {
				return errors.New("connection refused")
			}
			return nil
		}
		client.listToolsFn = func(_ context.Context, _ string) ([]mcpclient.ToolInfo, error) {
			return []mcpclient.ToolInfo{{Name: "good-tool"}}, nil
		}

		servers := []config.MCPServerConfig{
			{Name: "bad-server", Command: "x", Enabled: true},
			{Name: "good-server", Command: "y", Enabled: true},
		}

		tools, results, _ := appmcp.ConnectServers(context.Background(), client, servers)

		Expect(tools).To(HaveLen(1))
		Expect(tools[0].Name()).To(Equal("good-tool"))
		Expect(results).To(HaveLen(2))
		Expect(results[0].Name).To(Equal("bad-server"))
		Expect(results[0].Success).To(BeFalse())
		Expect(results[0].Error).To(ContainSubstring("connection refused"))
	})

	It("records a failure entry when ListTools fails", func() {
		client.listToolsFn = func(_ context.Context, name string) ([]mcpclient.ToolInfo, error) {
			if name == "broken-server" {
				return nil, errors.New("list tools failed")
			}
			return []mcpclient.ToolInfo{{Name: "ok-tool"}}, nil
		}

		servers := []config.MCPServerConfig{
			{Name: "broken-server", Command: "x", Enabled: true},
			{Name: "ok-server", Command: "y", Enabled: true},
		}

		tools, results, _ := appmcp.ConnectServers(context.Background(), client, servers)

		Expect(tools).To(HaveLen(1))
		Expect(tools[0].Name()).To(Equal("ok-tool"))
		Expect(results).To(HaveLen(2))
		Expect(results[0].Name).To(Equal("broken-server"))
		Expect(results[0].Success).To(BeFalse())
		Expect(results[0].Error).To(ContainSubstring("list tools failed"))
	})
})

// fakeMCPClient is a hand-rolled mcpclient.Client stub. Hand-rolling is
// preferred over a generated mock here because the surface is small and
// the specs need direct stub-fn injection per scenario.
type fakeMCPClient struct {
	connectFn    func(ctx context.Context, cfg mcpclient.ServerConfig) error
	listToolsFn  func(ctx context.Context, name string) ([]mcpclient.ToolInfo, error)
	connectCalls []mcpclient.ServerConfig
}

func newFakeMCPClient() *fakeMCPClient {
	return &fakeMCPClient{
		connectFn:   func(_ context.Context, _ mcpclient.ServerConfig) error { return nil },
		listToolsFn: func(_ context.Context, _ string) ([]mcpclient.ToolInfo, error) { return nil, nil },
	}
}

func (f *fakeMCPClient) Connect(ctx context.Context, cfg mcpclient.ServerConfig) error {
	f.connectCalls = append(f.connectCalls, cfg)
	return f.connectFn(ctx, cfg)
}

func (f *fakeMCPClient) Disconnect(_ string) error { return nil }

func (f *fakeMCPClient) ListTools(ctx context.Context, name string) ([]mcpclient.ToolInfo, error) {
	return f.listToolsFn(ctx, name)
}

func (f *fakeMCPClient) CallTool(_ context.Context, _, _ string, _ map[string]any) (*mcpclient.ToolResult, error) {
	return &mcpclient.ToolResult{}, nil
}

func (f *fakeMCPClient) DisconnectAll() error { return nil }
