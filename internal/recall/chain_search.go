package recall

import (
	"context"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

// ChainSearchTool provides search functionality across message chains.
type ChainSearchTool struct {
	chainStore ChainContextStore
	embedder   provider.Provider
	store      *FileContextStore
	topK       int
}

// NewChainSearchTool creates a new ChainSearchTool.
//
// Expected:
//   - chainStore provides chain access.
//   - embedder provides embedding support.
//   - store provides context access.
//
// Returns:
//   - A chain search tool.
//
// Side effects:
//   - None.
func NewChainSearchTool(chainStore ChainContextStore, embedder provider.Provider, store *FileContextStore) *ChainSearchTool {
	return &ChainSearchTool{
		chainStore: chainStore,
		embedder:   embedder,
		store:      store,
		topK:       5,
	}
}

// Name returns the name of the tool.
//
// Expected:
//   - The receiver is a valid ChainSearchTool.
//
// Returns:
//   - The tool name.
//
// Side effects:
//   - None.
func (t *ChainSearchTool) Name() string {
	return "chain_search"
}

// Description returns a description of the tool.
//
// Expected:
//   - The receiver is a valid ChainSearchTool.
//
// Returns:
//   - A short tool description.
//
// Side effects:
//   - None.
func (t *ChainSearchTool) Description() string {
	return "Search cross-agent context from the delegation chain"
}

// Schema returns the JSON schema for the tool parameters.
//
// Expected:
//   - The receiver is a valid ChainSearchTool.
//
// Returns:
//   - The tool parameter schema.
//
// Side effects:
//   - None.
func (t *ChainSearchTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"query":    {Type: "string", Description: "Search query"},
			"agent_id": {Type: "string", Description: "Filter by agent ID (optional)"},
		},
		Required: []string{"query"},
	}
}

// Execute performs the chain search operation.
//
// Expected:
//   - ctx is valid for chain lookups.
//   - input contains a query string.
//
// Returns:
//   - A tool result containing formatted chain messages.
//   - An error when fallback retrieval fails.
//
// Side effects:
//   - Reads from the chain store.
func (t *ChainSearchTool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	query, ok := input.Arguments["query"].(string)
	if !ok || query == "" {
		return t.fallbackToRecent()
	}

	results, err := t.chainStore.Search(ctx, query, t.topK)
	if err != nil || len(results) == 0 {
		return t.fallbackToRecent()
	}

	return tool.Result{Output: formatMessages(extractMessages(results))}, nil
}

// fallbackToRecent returns recent chain messages when a query cannot be used.
//
// Expected:
//   - The receiver is a valid ChainSearchTool.
//
// Returns:
//   - A tool result containing recent messages when available.
//   - An error when retrieving recent messages fails.
//
// Side effects:
//   - Reads from the chain store.
func (t *ChainSearchTool) fallbackToRecent() (tool.Result, error) {
	messages, err := t.chainStore.GetByAgent("", t.topK)
	if err != nil || len(messages) == 0 {
		return tool.Result{Output: ""}, err
	}
	return tool.Result{Output: formatMessages(messages)}, nil
}
