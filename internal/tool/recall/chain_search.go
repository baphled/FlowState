package recall

import (
	"context"
	"errors"
	"fmt"
	"strings"

	chainrecall "github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/tool"
)

const defaultTopK = 5

// ChainSearchTool performs semantic search over the shared chain context store.
type ChainSearchTool struct {
	store chainrecall.ChainContextStore
}

// NewChainSearchTool creates a new ChainSearchTool backed by the given chain store.
//
// Expected:
//   - store is a valid, non-nil ChainContextStore.
//
// Returns:
//   - A pointer to an initialised ChainSearchTool.
//
// Side effects:
//   - None.
func NewChainSearchTool(store chainrecall.ChainContextStore) *ChainSearchTool {
	return &ChainSearchTool{store: store}
}

// Name returns the tool identifier.
//
// Returns:
//   - The string "chain_search_context".
//
// Side effects:
//   - None.
func (t *ChainSearchTool) Name() string {
	return "chain_search_context"
}

// Description returns a human-readable description of the tool.
//
// Returns:
//   - A string describing the tool's purpose.
//
// Side effects:
//   - None.
func (t *ChainSearchTool) Description() string {
	return "Search the shared chain context semantically across all agents in the delegation chain"
}

// Schema returns the JSON schema for the tool inputs.
//
// Returns:
//   - A tool.Schema describing the query and top_k properties.
//
// Side effects:
//   - None.
func (t *ChainSearchTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"query": {
				Type:        "string",
				Description: "The search query to find semantically relevant messages",
			},
			"top_k": {
				Type:        "integer",
				Description: "Maximum number of results to return (default 5)",
			},
		},
		Required: []string{"query"},
	}
}

// Execute performs semantic search over the chain context store.
//
// Expected:
//   - ctx is a valid context for the search operation.
//   - input contains a "query" string argument.
//   - input may optionally contain a "top_k" integer argument.
//
// Returns:
//   - A tool.Result containing formatted matching messages.
//   - An error if the query argument is missing or the search fails.
//
// Side effects:
//   - May call the embedding provider via the chain store.
func (t *ChainSearchTool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	query, ok := input.Arguments["query"].(string)
	if !ok || query == "" {
		return tool.Result{}, errors.New("query argument is required")
	}

	topK := defaultTopK
	if raw, ok := input.Arguments["top_k"]; ok {
		if f, ok := raw.(float64); ok && f > 0 {
			topK = int(f)
		}
	}

	results, err := t.store.Search(ctx, query, topK)
	if err != nil {
		return tool.Result{}, fmt.Errorf("searching chain context: %w", err)
	}

	if len(results) == 0 {
		return tool.Result{Output: ""}, nil
	}

	var parts []string
	for _, r := range results {
		parts = append(parts, fmt.Sprintf("%s: %s", r.Message.Role, r.Message.Content))
	}
	return tool.Result{Output: strings.Join(parts, "\n---\n")}, nil
}
