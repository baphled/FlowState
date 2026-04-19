package app

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/learning"
	"github.com/baphled/flowstate/internal/mcp"
)

// recordingMCPClient counts CallTool invocations per (server, tool) pair so
// the P7/C1 tests can assert the recall broker does not hit the vault-rag
// MCP server when the vault string is unset.
type recordingMCPClient struct {
	// Use atomic counters — recall.Broker fans out queries to sources in
	// goroutines, so counts must be safe to read concurrently.
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

// TestBuildRecallBroker_DoesNotAttachVaultSource_WhenVaultStringEmpty locks
// in bug C1 — on every user turn, buildRecallBroker attached a VaultSource
// constructed with an empty vault string, which then fired query_vault
// against the vault-rag MCP server. That produced 185 non-JSON debug log
// lines per session and forced the engine's window builder down the
// semantic-results path with garbage data. The fix is to gate attachment of
// the vault source on a non-empty vault string at construction time.
func TestBuildRecallBroker_DoesNotAttachVaultSource_WhenVaultStringEmpty(t *testing.T) {
	client := &recordingMCPClient{}
	cfg := &config.AppConfig{}
	// Leave cfg.Qdrant.URL empty so the broker path is the one currently
	// exercised in production when vault is not configured.

	broker := buildRecallBroker(recallBrokerParams{
		cfg:       cfg,
		mcpClient: client,
	})
	if broker == nil {
		t.Fatal("buildRecallBroker returned nil")
	}

	ctx := context.WithValue(context.Background(), learning.AgentIDKey, "test-agent")
	if _, err := broker.Query(ctx, "hello", 5); err != nil {
		t.Fatalf("broker.Query: %v", err)
	}

	if got := client.vaultCallCount.Load(); got != 0 {
		t.Errorf("vault-rag CallTool invocations = %d; want 0 when vault string is empty (C1)", got)
	}
}

// TestBuildRecallBroker_AttachesVaultSource_WhenVaultStringNonEmpty is the
// forward-compatibility counterpart: once callers thread a real vault string
// into buildRecallBroker (via a future config field) the vault source must
// reattach and query the MCP server. This test drives the presence of that
// attach site without yet introducing the config field.
func TestBuildRecallBroker_AttachesVaultSource_WhenVaultStringNonEmpty(t *testing.T) {
	client := &recordingMCPClient{}
	cfg := &config.AppConfig{}

	broker := buildRecallBrokerWithVault(recallBrokerParams{
		cfg:       cfg,
		mcpClient: client,
	}, "baphled")
	if broker == nil {
		t.Fatal("buildRecallBrokerWithVault returned nil")
	}

	ctx := context.WithValue(context.Background(), learning.AgentIDKey, "test-agent")
	if _, err := broker.Query(ctx, "hello", 5); err != nil {
		t.Fatalf("broker.Query: %v", err)
	}

	if got := client.vaultCallCount.Load(); got == 0 {
		t.Errorf("vault-rag CallTool invocations = 0; want >=1 when vault string is provided")
	}
}

// TestBuildRecallBroker_AttachesVaultSource_WhenConfigVaultPathSet verifies
// that buildRecallBroker (not buildRecallBrokerWithVault) attaches the vault
// source when cfg.VaultPath is populated — the B3 bug fix.
func TestBuildRecallBroker_AttachesVaultSource_WhenConfigVaultPathSet(t *testing.T) {
	client := &recordingMCPClient{}
	cfg := &config.AppConfig{
		VaultPath: "baphled",
	}

	broker := buildRecallBroker(recallBrokerParams{
		cfg:       cfg,
		mcpClient: client,
	})
	if broker == nil {
		t.Fatal("buildRecallBroker returned nil")
	}

	ctx := context.WithValue(context.Background(), learning.AgentIDKey, "test-agent")
	if _, err := broker.Query(ctx, "hello", 5); err != nil {
		t.Fatalf("broker.Query: %v", err)
	}

	if got := client.vaultCallCount.Load(); got == 0 {
		t.Errorf("vault-rag CallTool invocations = 0; want >=1 when cfg.VaultPath is set")
	}
}
