package vault

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/vaultindex"
)

// Handler is the subset of vaultindex.QueryHandler the tool requires.
type Handler interface {
	Handle(ctx context.Context, args vaultindex.QueryArgs) (vaultindex.QueryResponse, error)
}

// QueryVaultTool implements mcp_vault-rag_query_vault: semantic search over the
// indexed Obsidian vault using the local Qdrant collection.
type QueryVaultTool struct {
	handler Handler
}

// NewQueryVaultTool creates a QueryVaultTool backed by the given Handler.
func NewQueryVaultTool(handler Handler) *QueryVaultTool {
	return &QueryVaultTool{handler: handler}
}

func (t *QueryVaultTool) Name() string { return "mcp_vault-rag_query_vault" }
func (t *QueryVaultTool) Description() string {
	return "Search the indexed Obsidian vault for relevant knowledge"
}

func (t *QueryVaultTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"question": {Type: "string", Description: "Question or query to search the vault for"},
			"vault":    {Type: "string", Description: "Vault path scope (optional)"},
			"top_k":    {Type: "integer", Description: "Maximum number of chunks to return (default 5)"},
		},
		Required: []string{"question"},
	}
}

// Execute queries the vault and returns formatted chunk results.
func (t *QueryVaultTool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	question, ok := input.Arguments["question"].(string)
	if !ok || question == "" {
		return tool.Result{}, errors.New("question argument is required")
	}

	args := vaultindex.QueryArgs{Question: question}
	if v, ok := input.Arguments["vault"].(string); ok {
		args.Vault = v
	}
	if raw, ok := input.Arguments["top_k"]; ok {
		if f, ok := raw.(float64); ok && f > 0 {
			args.TopK = int(f)
		}
	}

	resp, err := t.handler.Handle(ctx, args)
	if err != nil {
		return tool.Result{}, fmt.Errorf("querying vault: %w", err)
	}

	if len(resp.Chunks) == 0 {
		return tool.Result{Output: "no results found"}, nil
	}

	var sb strings.Builder
	for i, chunk := range resp.Chunks {
		fmt.Fprintf(&sb, "[%d] %s (chunk %d)\n%s\n", i+1, chunk.SourceFile, chunk.ChunkIndex, chunk.Content)
		if i < len(resp.Chunks)-1 {
			sb.WriteString("---\n")
		}
	}
	return tool.Result{Output: sb.String()}, nil
}
