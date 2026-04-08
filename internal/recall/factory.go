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
func (f *ToolFactory) Tools() []tool.Tool {
	return []tool.Tool{
		NewSearchContextTool(f.store, f.embedder, f.topK),
		NewGetMessagesTool(f.store),
		NewSummarizeContextTool(f.store, f.embedder, 2, f.tokenCounter, f.model),
	}
}

// ToolsWithChainStore returns recall tools with chain store integration.
func (f *ToolFactory) ToolsWithChainStore(chainStore ChainContextStore) []tool.Tool {
	tools := f.Tools()
	if chainStore != nil {
		chainTool := NewChainSearchTool(chainStore, f.embedder, f.store)
		tools = append(tools, chainTool)
	}
	return tools
}
