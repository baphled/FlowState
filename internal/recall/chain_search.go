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
func NewChainSearchTool(chainStore ChainContextStore, embedder provider.Provider, store *FileContextStore) *ChainSearchTool {
	return &ChainSearchTool{
		chainStore: chainStore,
		embedder:   embedder,
		store:      store,
		topK:       5,
	}
}

// Name returns the name of the tool.
func (t *ChainSearchTool) Name() string {
	return "chain_search"
}

// Description returns a description of the tool.
func (t *ChainSearchTool) Description() string {
	return "Search cross-agent context from the delegation chain"
}

// Schema returns the JSON schema for the tool parameters.
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

func (t *ChainSearchTool) fallbackToRecent() (tool.Result, error) {
	messages, err := t.chainStore.GetByAgent("", t.topK)
	if err != nil || len(messages) == 0 {
		return tool.Result{Output: ""}, err
	}
	return tool.Result{Output: formatMessages(messages)}, nil
}
