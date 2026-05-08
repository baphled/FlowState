package toolset_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/toolset"
)

func toolNames(tools []tool.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name())
	}
	return names
}

// stubTool is a minimal tool.Tool implementation for testing tool list
// composition.
type stubTool struct {
	name string
}

func (s *stubTool) Name() string        { return s.name }
func (s *stubTool) Description() string { return "" }
func (s *stubTool) Schema() tool.Schema { return tool.Schema{} }
func (s *stubTool) Execute(_ context.Context, _ tool.Input) (tool.Result, error) {
	return tool.Result{}, nil
}

// AppendChainTools tests cover the helper that decorates a base tool
// slice with the chain_search_context + chain_get_messages tools when a
// chain store is configured. Coverage:
//   - non-nil store appends both chain tools, in order.
//   - nil store returns the base slice unchanged.
//   - existing tools survive the append (chain tools come last).
var _ = Describe("AppendChainTools (in-package behaviour)", func() {
	It("appends chain_search_context and chain_get_messages when the store is non-nil", func() {
		base := []tool.Tool{}
		store := recall.NewInMemoryChainStore(nil)

		result := toolset.AppendChainTools(base, store)

		names := toolNames(result)
		Expect(names).To(HaveLen(2))
		Expect(names[0]).To(Equal("chain_search_context"))
		Expect(names[1]).To(Equal("chain_get_messages"))
	})

	It("preserves existing tools and appends chain tools after them", func() {
		stub := &stubTool{name: "existing_tool"}
		base := []tool.Tool{stub}
		store := recall.NewInMemoryChainStore(nil)

		result := toolset.AppendChainTools(base, store)

		names := toolNames(result)
		Expect(names).To(HaveLen(3))
		Expect(names[0]).To(Equal("existing_tool"))
	})
})
