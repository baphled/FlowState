package app

import (
	"context"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/learning"
	"github.com/baphled/flowstate/internal/mcp"
)

// recordingMCPClient counts CallTool invocations per (server, tool) pair
// so the P7/C1 specs can assert the recall broker does not hit the
// vault-rag MCP server when the vault string is unset.
type recordingMCPClient struct {
	// Use atomic counters — recall.Broker fans out queries to sources
	// in goroutines, so counts must be safe to read concurrently.
	vaultCallCount  atomic.Int64
	memoryCallCount atomic.Int64
	otherCallCount  atomic.Int64
}

func (r *recordingMCPClient) Connect(_ context.Context, _ mcp.ServerConfig) error { return nil }
func (r *recordingMCPClient) Disconnect(_ string) error                           { return nil }
func (r *recordingMCPClient) DisconnectAll() error                                { return nil }
func (r *recordingMCPClient) ListTools(_ context.Context, _ string) ([]mcp.ToolInfo, error) {
	return nil, nil
}

func (r *recordingMCPClient) CallTool(_ context.Context, serverName, _ string, _ map[string]any) (*mcp.ToolResult, error) {
	switch serverName {
	case "vault-rag":
		r.vaultCallCount.Add(1)
	case "memory":
		r.memoryCallCount.Add(1)
	default:
		r.otherCallCount.Add(1)
	}
	return &mcp.ToolResult{Content: `{}`, IsError: false}, nil
}

// Recall broker vault-source gating.
//
// Bug C1: on every user turn, buildRecallBroker attached a VaultSource
// constructed with an empty vault string, which then fired query_vault
// against the vault-rag MCP server. That produced 185 non-JSON debug log
// lines per session and forced the engine's window builder down the
// semantic-results path with garbage data. The fix is to gate attachment
// of the vault source on a non-empty vault string at construction time.
// B3 then wired cfg.VaultPath as the canonical source.
var _ = Describe("buildRecallBroker vault-source gating", func() {
	It("does not attach the vault source when no vault string is supplied (C1)", func() {
		client := &recordingMCPClient{}
		cfg := &config.AppConfig{}
		// Leave cfg.Qdrant.URL empty so the broker path is the one
		// currently exercised in production when vault is not configured.

		broker := buildRecallBroker(recallBrokerParams{
			cfg:       cfg,
			mcpClient: client,
		})
		Expect(broker).NotTo(BeNil())

		ctx := context.WithValue(context.Background(), learning.AgentIDKey, "test-agent")
		_, err := broker.Query(ctx, "hello", 5)
		Expect(err).NotTo(HaveOccurred())

		Expect(client.vaultCallCount.Load()).To(Equal(int64(0)),
			"vault-rag CallTool must not fire when the vault string is empty")
	})

	It("attaches the vault source when buildRecallBrokerWithVault is given a non-empty string", func() {
		client := &recordingMCPClient{}
		cfg := &config.AppConfig{}

		broker := buildRecallBrokerWithVault(recallBrokerParams{
			cfg:       cfg,
			mcpClient: client,
		}, "baphled")
		Expect(broker).NotTo(BeNil())

		ctx := context.WithValue(context.Background(), learning.AgentIDKey, "test-agent")
		_, err := broker.Query(ctx, "hello", 5)
		Expect(err).NotTo(HaveOccurred())

		Expect(client.vaultCallCount.Load()).To(BeNumerically(">=", 1),
			"vault-rag CallTool must fire when a vault string is provided")
	})

	It("attaches the vault source when cfg.VaultPath is populated (B3)", func() {
		client := &recordingMCPClient{}
		cfg := &config.AppConfig{
			VaultPath: "baphled",
		}

		broker := buildRecallBroker(recallBrokerParams{
			cfg:       cfg,
			mcpClient: client,
		})
		Expect(broker).NotTo(BeNil())

		ctx := context.WithValue(context.Background(), learning.AgentIDKey, "test-agent")
		_, err := broker.Query(ctx, "hello", 5)
		Expect(err).NotTo(HaveOccurred())

		Expect(client.vaultCallCount.Load()).To(BeNumerically(">=", 1),
			"vault-rag CallTool must fire when cfg.VaultPath is set")
	})
})
