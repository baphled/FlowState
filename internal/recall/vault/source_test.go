package vault_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/mcp"
	vaultrecall "github.com/baphled/flowstate/internal/recall/vault"
)

type stubMCPClient struct {
	result *mcp.ToolResult
	err    error
}

func (s *stubMCPClient) Connect(_ context.Context, _ mcp.ServerConfig) error { return nil }
func (s *stubMCPClient) Disconnect(_ string) error                           { return nil }
func (s *stubMCPClient) DisconnectAll() error                                { return nil }
func (s *stubMCPClient) ListTools(_ context.Context, _ string) ([]mcp.ToolInfo, error) {
	return nil, nil
}
func (s *stubMCPClient) CallTool(_ context.Context, _, _ string, _ map[string]any) (*mcp.ToolResult, error) {
	return s.result, s.err
}

func chunksJSON(chunks []map[string]any) string {
	b, _ := json.Marshal(map[string]any{"chunks": chunks})
	return string(b)
}

var _ = Describe("VaultSource", func() {
	var (
		ctx    context.Context
		stubMC *stubMCPClient
	)

	BeforeEach(func() {
		ctx = context.Background()
		stubMC = &stubMCPClient{}
	})

	It("returns empty slice when MCP CallTool returns an error", func() {
		stubMC.err = errors.New("network failure")
		source := vaultrecall.NewVaultSource(stubMC, "vault-rag", "baphled")
		results, err := source.Query(ctx, "test", 5)
		Expect(err).To(MatchError("network failure"))
		Expect(results).To(BeEmpty())
	})

	It("returns empty slice when MCP result IsError is true", func() {
		stubMC.result = &mcp.ToolResult{Content: "server error", IsError: true}
		source := vaultrecall.NewVaultSource(stubMC, "vault-rag", "baphled")
		results, err := source.Query(ctx, "test", 5)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(BeEmpty())
	})

	It("maps vault chunks to observations", func() {
		content := chunksJSON([]map[string]any{
			{"content": "hello world", "source_file": "/docs/note.md", "chunk_index": 0},
		})
		stubMC.result = &mcp.ToolResult{Content: content, IsError: false}
		source := vaultrecall.NewVaultSource(stubMC, "vault-rag", "baphled")
		results, err := source.Query(ctx, "hello", 5)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(1))
		Expect(results[0].Source).To(Equal("vault-rag"))
		Expect(results[0].Content).To(Equal("hello world"))
		Expect(results[0].ID).To(Equal("vault:/docs/note.md:" + strconv.Itoa(0)))
	})

	It("returns empty slice when JSON is malformed", func() {
		stubMC.result = &mcp.ToolResult{Content: "not-json", IsError: false}
		source := vaultrecall.NewVaultSource(stubMC, "vault-rag", "baphled")
		results, err := source.Query(ctx, "test", 5)
		Expect(err).To(HaveOccurred())
		Expect(results).To(BeEmpty())
	})

	It("returns empty slice when chunks array is empty", func() {
		stubMC.result = &mcp.ToolResult{Content: `{"chunks":[]}`, IsError: false}
		source := vaultrecall.NewVaultSource(stubMC, "vault-rag", "baphled")
		results, err := source.Query(ctx, "test", 5)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(BeEmpty())
	})
})
