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
	result    *mcp.ToolResult
	err       error
	callCount int
}

func (s *stubMCPClient) Connect(_ context.Context, _ mcp.ServerConfig) error { return nil }
func (s *stubMCPClient) Disconnect(_ string) error                           { return nil }
func (s *stubMCPClient) DisconnectAll() error                                { return nil }
func (s *stubMCPClient) ListTools(_ context.Context, _ string) ([]mcp.ToolInfo, error) {
	return nil, nil
}
func (s *stubMCPClient) CallTool(_ context.Context, _, _ string, _ map[string]any) (*mcp.ToolResult, error) {
	s.callCount++
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

	It("returns an error when JSON-shaped content is syntactically malformed", func() {
		// Content begins with '{' so it IS a JSON-shape response, but it's
		// broken. That is a real decode error and must surface.
		stubMC.result = &mcp.ToolResult{Content: `{"chunks": [`, IsError: false}
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

	// Reproduces the post-restart 2026-04-13 20:43:35 recall broker log:
	//   "warning: recall source query failed: invalid character 'u'
	//    looking for beginning of value"
	// The vault-rag MCP server emitted the literal string "undefined" on a
	// no-result response; that is non-JSON content and must be treated as
	// an empty result, not a decode error.
	It("returns empty slice without error when MCP returns non-JSON text such as 'undefined'", func() {
		stubMC.result = &mcp.ToolResult{Content: "undefined", IsError: false}
		source := vaultrecall.NewVaultSource(stubMC, "vault-rag", "baphled")
		results, err := source.Query(ctx, "test", 5)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(BeEmpty())
	})

	It("returns empty slice without error when MCP returns whitespace-only content", func() {
		stubMC.result = &mcp.ToolResult{Content: "   \t\r\n   ", IsError: false}
		source := vaultrecall.NewVaultSource(stubMC, "vault-rag", "baphled")
		results, err := source.Query(ctx, "test", 5)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(BeEmpty())
	})

	It("returns empty slice without error when MCP returns an empty string", func() {
		stubMC.result = &mcp.ToolResult{Content: "", IsError: false}
		source := vaultrecall.NewVaultSource(stubMC, "vault-rag", "baphled")
		results, err := source.Query(ctx, "test", 5)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(BeEmpty())
	})

	// P7/C1 defence-in-depth: when the source was constructed with an empty
	// vault string (the current default wiring in app.buildRecallBroker until
	// a config field is introduced), Query must not call the MCP server at
	// all. The previous behaviour sent `{question, vault:"", top_k}` on every
	// user turn and produced 185 debug log lines per session complaining that
	// the MCP returned non-JSON content. The vault string is the scope of
	// the query; with no scope there is nothing meaningful to ask.
	Describe("P7/C1 empty-vault defence-in-depth", func() {
		It("returns an empty slice without calling the MCP client when vault is empty", func() {
			stubMC.result = &mcp.ToolResult{Content: `{"chunks":[]}`, IsError: false}
			source := vaultrecall.NewVaultSource(stubMC, "vault-rag", "")

			results, err := source.Query(ctx, "any query", 5)

			Expect(err).ToNot(HaveOccurred())
			Expect(results).To(BeEmpty())
			Expect(stubMC.callCount).To(Equal(0),
				"Query must short-circuit without invoking CallTool when the source was constructed with an empty vault string")
		})

		It("returns an empty slice without calling the MCP client when vault is whitespace-only", func() {
			stubMC.result = &mcp.ToolResult{Content: `{"chunks":[]}`, IsError: false}
			source := vaultrecall.NewVaultSource(stubMC, "vault-rag", "   \t ")

			results, err := source.Query(ctx, "any query", 5)

			Expect(err).ToNot(HaveOccurred())
			Expect(results).To(BeEmpty())
			Expect(stubMC.callCount).To(Equal(0),
				"Query must short-circuit on whitespace-only vault string as well")
		})

		It("still calls the MCP client when vault is a non-empty string", func() {
			// Regression guard: the defence-in-depth gate must not break the
			// happy path.
			stubMC.result = &mcp.ToolResult{Content: `{"chunks":[]}`, IsError: false}
			source := vaultrecall.NewVaultSource(stubMC, "vault-rag", "baphled")

			_, err := source.Query(ctx, "any query", 5)

			Expect(err).ToNot(HaveOccurred())
			Expect(stubMC.callCount).To(Equal(1),
				"non-empty vault must still reach the MCP server")
		})
	})
})
