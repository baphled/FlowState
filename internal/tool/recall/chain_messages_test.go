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

var _ = Describe("ChainGetMessagesTool", func() {
	var (
		store *chainrecall.InMemoryChainStore
		t     *recalltool.ChainGetMessagesTool
	)

	BeforeEach(func() {
		store = chainrecall.NewInMemoryChainStore(nil)
		t = recalltool.NewChainGetMessagesTool(store)
	})

	Describe("Name", func() {
		It("returns chain_get_messages", func() {
			Expect(t.Name()).To(Equal("chain_get_messages"))
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

		It("includes an agent_id property", func() {
			schema := t.Schema()
			Expect(schema.Properties).To(HaveKey("agent_id"))
		})

		It("includes a last property", func() {
			schema := t.Schema()
			Expect(schema.Properties).To(HaveKey("last"))
		})
	})

	Describe("Execute", func() {
		BeforeEach(func() {
			Expect(store.Append("agent-a", provider.Message{Role: "assistant", Content: "message from A"})).To(Succeed())
			Expect(store.Append("agent-b", provider.Message{Role: "assistant", Content: "message from B"})).To(Succeed())
			Expect(store.Append("agent-a", provider.Message{Role: "assistant", Content: "second from A"})).To(Succeed())
		})

		Context("when requesting messages from a specific agent", func() {
			It("returns only messages from that agent", func() {
				result, err := t.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{
						"agent_id": "agent-a",
						"last":     float64(10),
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("agent-a"))
				Expect(result.Output).NotTo(ContainSubstring("message from B"))
			})
		})

		Context("when requesting messages from all agents (empty agent_id)", func() {
			It("returns messages from all agents", func() {
				result, err := t.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{
						"agent_id": "",
						"last":     float64(10),
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("message from A"))
				Expect(result.Output).To(ContainSubstring("message from B"))
			})
		})

		Context("when no agent_id is provided", func() {
			It("defaults to returning messages from all agents", func() {
				result, err := t.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{
						"last": float64(10),
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).NotTo(BeEmpty())
			})
		})

		Context("when the store has no messages for the requested agent", func() {
			It("returns empty output without error", func() {
				result, err := t.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{
						"agent_id": "nonexistent",
						"last":     float64(10),
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(BeEmpty())
			})
		})

		Context("when last is provided as a limit", func() {
			It("returns at most last messages", func() {
				result, err := t.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{
						"agent_id": "",
						"last":     float64(1),
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).NotTo(BeEmpty())
			})
		})
	})
})
