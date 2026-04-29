package vault_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	toolsvault "github.com/baphled/flowstate/internal/tool/vault"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/vaultindex"
)

// stubHandler lets tests control QueryHandler output without hitting Qdrant.
type stubHandler struct {
	result vaultindex.QueryResponse
	err    error
}

func (s *stubHandler) Handle(_ context.Context, _ vaultindex.QueryArgs) (vaultindex.QueryResponse, error) {
	return s.result, s.err
}

var _ = Describe("mcp_vault-rag_query_vault", func() {
	It("returns formatted chunks for a matching query", func() {
		handler := &stubHandler{
			result: vaultindex.QueryResponse{
				Chunks: []vaultindex.Chunk{
					{Content: "Go channels enable goroutine communication", SourceFile: "concurrency.md", ChunkIndex: 0},
					{Content: "Select statement multiplexes channels", SourceFile: "concurrency.md", ChunkIndex: 1},
				},
			},
		}
		t := toolsvault.NewQueryVaultTool(handler)

		result, err := t.Execute(context.Background(), tool.Input{
			Name:      "mcp_vault-rag_query_vault",
			Arguments: map[string]interface{}{"question": "go channels"},
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(result.Output).To(ContainSubstring("Go channels enable goroutine communication"))
		Expect(result.Output).To(ContainSubstring("concurrency.md"))
		Expect(result.Output).To(ContainSubstring("Select statement multiplexes channels"))
	})

	It("returns a no-results message when handler returns empty chunks", func() {
		handler := &stubHandler{result: vaultindex.QueryResponse{Chunks: []vaultindex.Chunk{}}}
		t := toolsvault.NewQueryVaultTool(handler)

		result, err := t.Execute(context.Background(), tool.Input{
			Name:      "mcp_vault-rag_query_vault",
			Arguments: map[string]interface{}{"question": "nothing here"},
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(result.Output).To(ContainSubstring("no results"))
	})

	It("returns an error when question argument is missing", func() {
		t := toolsvault.NewQueryVaultTool(&stubHandler{})

		_, err := t.Execute(context.Background(), tool.Input{
			Name:      "mcp_vault-rag_query_vault",
			Arguments: map[string]interface{}{},
		})

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("question"))
	})

	It("propagates handler errors", func() {
		handler := &stubHandler{err: errors.New("qdrant offline")}
		t := toolsvault.NewQueryVaultTool(handler)

		_, err := t.Execute(context.Background(), tool.Input{
			Name:      "mcp_vault-rag_query_vault",
			Arguments: map[string]interface{}{"question": "test"},
		})

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("qdrant offline"))
	})

	It("has the correct tool name and requires question in schema", func() {
		t := toolsvault.NewQueryVaultTool(&stubHandler{})
		Expect(t.Name()).To(Equal("mcp_vault-rag_query_vault"))
		Expect(t.Schema().Required).To(ContainElement("question"))
	})

	It("passes top_k and vault arguments through to the handler", func() {
		called := false
		var capturedArgs vaultindex.QueryArgs
		handler := &capturingHandler{onHandle: func(args vaultindex.QueryArgs) {
			called = true
			capturedArgs = args
		}}
		t := toolsvault.NewQueryVaultTool(handler)

		_, _ = t.Execute(context.Background(), tool.Input{
			Name: "mcp_vault-rag_query_vault",
			Arguments: map[string]interface{}{
				"question": "goroutine scheduling",
				"top_k":    float64(3),
				"vault":    "/home/user/vault",
			},
		})

		Expect(called).To(BeTrue())
		Expect(capturedArgs.Question).To(Equal("goroutine scheduling"))
		Expect(capturedArgs.TopK).To(Equal(3))
		Expect(capturedArgs.Vault).To(Equal("/home/user/vault"))
	})
})

// capturingHandler records the QueryArgs passed to Handle for assertion.
type capturingHandler struct {
	onHandle func(vaultindex.QueryArgs)
}

func (c *capturingHandler) Handle(_ context.Context, args vaultindex.QueryArgs) (vaultindex.QueryResponse, error) {
	if c.onHandle != nil {
		c.onHandle(args)
	}
	return vaultindex.QueryResponse{Chunks: []vaultindex.Chunk{}}, nil
}
