package recall

import (
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

type RecallToolFactory struct {
	store        *FileContextStore
	embedder     provider.Provider
	tokenCounter TokenCounter
	model        string
	topK         int
}

func NewRecallToolFactory(store *FileContextStore, embedder provider.Provider, tokenCounter TokenCounter, model string) *RecallToolFactory {
	return &RecallToolFactory{
		store:        store,
		embedder:     embedder,
		tokenCounter: tokenCounter,
		model:        model,
		topK:         5,
	}
}

func (f *RecallToolFactory) Tools() []tool.Tool {
	return []tool.Tool{
		NewSearchContextTool(f.store, f.embedder, f.topK),
		NewGetMessagesTool(f.store),
		NewSummarizeContextTool(f.store, f.embedder, 2, f.tokenCounter, f.model),
	}
}

func (f *RecallToolFactory) ToolsWithChainStore(chainStore ChainContextStore) []tool.Tool {
	tools := f.Tools()
	if chainStore != nil {
		chainTool := NewChainSearchTool(chainStore, f.embedder, f.store)
		tools = append(tools, chainTool)
	}
	return tools
}
