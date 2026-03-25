package coordination_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	store "github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/tool"
	coordination "github.com/baphled/flowstate/internal/tool/coordination"
)

var _ = Describe("CoordinationTool", func() {
	var (
		t   tool.Tool
		mem *store.MemoryStore
		ctx context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		mem = store.NewMemoryStore()
		t = coordination.New(mem)
	})

	Describe("Name", func() {
		It("returns coordination_store", func() {
			Expect(t.Name()).To(Equal("coordination_store"))
		})
	})

	Describe("Schema", func() {
		It("returns a valid schema with required parameters", func() {
			s := t.Schema()
			Expect(s.Type).To(Equal("object"))
			Expect(s.Properties).To(HaveKey("operation"))
			Expect(s.Properties).To(HaveKey("key"))
			Expect(s.Properties).To(HaveKey("value"))
			Expect(s.Properties).To(HaveKey("prefix"))
			Expect(s.Required).To(ConsistOf("operation"))
			Expect(s.Properties["operation"].Enum).To(ConsistOf("get", "set", "list", "delete"))
		})
	})

	Describe("Execute", func() {
		Context("set operation", func() {
			It("stores a value", func() {
				result, err := t.Execute(ctx, tool.Input{
					Name: "coordination_store",
					Arguments: map[string]interface{}{
						"operation": "set",
						"key":       "chain1/plan",
						"value":     "my plan content",
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("stored"))

				val, storeErr := mem.Get("chain1/plan")
				Expect(storeErr).NotTo(HaveOccurred())
				Expect(string(val)).To(Equal("my plan content"))
			})
		})

		Context("get operation", func() {
			It("returns a stored value", func() {
				Expect(mem.Set("chain1/review", []byte("review content"))).To(Succeed())

				result, err := t.Execute(ctx, tool.Input{
					Name: "coordination_store",
					Arguments: map[string]interface{}{
						"operation": "get",
						"key":       "chain1/review",
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(Equal("review content"))
			})
		})

		Context("list operation", func() {
			It("returns matching keys", func() {
				Expect(mem.Set("chain1/plan", []byte("p"))).To(Succeed())
				Expect(mem.Set("chain1/review", []byte("r"))).To(Succeed())
				Expect(mem.Set("chain2/plan", []byte("p2"))).To(Succeed())

				result, err := t.Execute(ctx, tool.Input{
					Name: "coordination_store",
					Arguments: map[string]interface{}{
						"operation": "list",
						"prefix":    "chain1/",
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("chain1/plan"))
				Expect(result.Output).To(ContainSubstring("chain1/review"))
				Expect(result.Output).NotTo(ContainSubstring("chain2/"))
			})
		})

		Context("delete operation", func() {
			It("removes a key", func() {
				Expect(mem.Set("chain1/temp", []byte("tmp"))).To(Succeed())

				result, err := t.Execute(ctx, tool.Input{
					Name: "coordination_store",
					Arguments: map[string]interface{}{
						"operation": "delete",
						"key":       "chain1/temp",
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("deleted"))

				_, storeErr := mem.Get("chain1/temp")
				Expect(storeErr).To(HaveOccurred())
			})
		})

		Context("unknown operation", func() {
			It("returns an error", func() {
				_, err := t.Execute(ctx, tool.Input{
					Name: "coordination_store",
					Arguments: map[string]interface{}{
						"operation": "invalid",
					},
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("unknown operation"))
			})
		})
	})
})
