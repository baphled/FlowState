package recall

import (
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

// ToolFactory creates recall-related tools.
type ToolFactory struct {
	store        *FileContextStore
	embedder     provider.Provider
	tokenCounter TokenCounter
	model        string
	topK         int
}

// NewToolFactory creates a new ToolFactory.
//
// Expected:
//   - store and embedder provide recall access.
//   - tokenCounter counts model tokens.
//   - model identifies the active embedding model.
//
// Returns:
//   - A configured tool factory.
//
// Side effects:
//   - None.
func NewToolFactory(store *FileContextStore, embedder provider.Provider, tokenCounter TokenCounter, model string) *ToolFactory {
	return &ToolFactory{
		store:        store,
		embedder:     embedder,
		tokenCounter: tokenCounter,
		model:        model,
		topK:         5,
	}
}

// Tools returns the available recall tools.
//
// Expected:
//   - The receiver is a valid ToolFactory.
//
// Returns:
//   - The base recall tools.
//
// Side effects:
//   - None.
func (f *ToolFactory) Tools() []tool.Tool {
	return []tool.Tool{
		NewSearchContextTool(f.store, f.embedder, f.topK),
		NewGetMessagesTool(f.store),
		NewSummarizeContextTool(f.store, f.embedder, 2, f.tokenCounter, f.model),
	}
}

// ToolsWithChainStore returns recall tools with chain store integration.
//
// Expected:
//   - The receiver is a valid ToolFactory.
//   - chainStore may be nil.
//
// Returns:
//   - The base tools, plus chain search when a chain store is provided.
//
// Side effects:
//   - None.
func (f *ToolFactory) ToolsWithChainStore(chainStore ChainContextStore) []tool.Tool {
	tools := f.Tools()
	if chainStore != nil {
		chainTool := NewChainSearchTool(chainStore, f.embedder, f.store)
		tools = append(tools, chainTool)
	}
	return tools
}
