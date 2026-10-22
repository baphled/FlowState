package recall_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	chainrecall "github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/tool"
	recalltool "github.com/baphled/flowstate/internal/tool/recall"
)

var _ = Describe("ChainSearchTool", func() {
	var (
		store *chainrecall.InMemoryChainStore
		t     *recalltool.ChainSearchTool
	)

	BeforeEach(func() {
		store = chainrecall.NewInMemoryChainStore(nil)
		t = recalltool.NewChainSearchTool(store)
	})

	Describe("Name", func() {
		It("returns chain_search_context", func() {
			Expect(t.Name()).To(Equal("chain_search_context"))
		})
	})

	Describe("Description", func() {
		It("returns a human-readable description", func() {
			Expect(t.Description()).NotTo(BeEmpty())
		})
	})

	Describe("Schema", func() {
		It("has the correct schema type", func() {
			schema := t.Schema()
			Expect(schema.Type).To(Equal("object"))
		})

		It("requires a query property", func() {
			schema := t.Schema()
			Expect(schema.Properties).To(HaveKey("query"))
			Expect(schema.Required).To(ContainElement("query"))
		})

		It("includes a top_k property", func() {
			schema := t.Schema()
			Expect(schema.Properties).To(HaveKey("top_k"))
		})
	})

	Describe("Execute", func() {
		Context("when the store has messages", func() {
			BeforeEach(func() {
				Expect(store.Append("agent-a", provider.Message{Role: "assistant", Content: "alpha content"})).To(Succeed())
				Expect(store.Append("agent-b", provider.Message{Role: "assistant", Content: "beta content"})).To(Succeed())
			})

			It("returns formatted results when given a valid query", func() {
				result, err := t.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{
						"query": "alpha",
						"top_k": float64(5),
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).NotTo(BeEmpty())
			})

			It("returns results without error for a query with no top_k specified", func() {
				result, err := t.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{
						"query": "beta",
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).NotTo(BeEmpty())
			})
		})

		Context("when the store is empty", func() {
			It("returns empty output without error", func() {
				result, err := t.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{
						"query": "anything",
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(BeEmpty())
			})
		})

		Context("when query argument is missing", func() {
			It("returns an error", func() {
				_, err := t.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{},
				})
				Expect(err).To(HaveOccurred())
			})
		})
	})
})
