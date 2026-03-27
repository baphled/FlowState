package recall_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

var _ = Describe("ChainContextStore", func() {
	Describe("NewInMemoryChainStore", func() {
		It("returns a store with a non-empty ChainID", func() {
			store := recall.NewInMemoryChainStore(nil)
			Expect(store.ChainID()).NotTo(BeEmpty())
		})

		It("returns unique ChainIDs for different stores", func() {
			a := recall.NewInMemoryChainStore(nil)
			b := recall.NewInMemoryChainStore(nil)
			Expect(a.ChainID()).NotTo(Equal(b.ChainID()))
		})
	})

	Describe("Append", func() {
		Context("when appending a message for an agent", func() {
			It("stores the message retrievable by that agent", func() {
				store := recall.NewInMemoryChainStore(nil)
				msg := provider.Message{Role: "assistant", Content: "hello from agent A"}

				Expect(store.Append("agent-a", msg)).To(Succeed())

				messages, err := store.GetByAgent("agent-a", 10)
				Expect(err).NotTo(HaveOccurred())
				Expect(messages).To(HaveLen(1))
				Expect(messages[0].Content).To(Equal("hello from agent A"))
			})
		})

		Context("when appending messages from multiple agents", func() {
			It("keeps messages from each agent isolated by agentID", func() {
				store := recall.NewInMemoryChainStore(nil)

				Expect(store.Append("agent-a", provider.Message{Role: "assistant", Content: "A says hi"})).To(Succeed())
				Expect(store.Append("agent-b", provider.Message{Role: "assistant", Content: "B says hello"})).To(Succeed())
				Expect(store.Append("agent-a", provider.Message{Role: "assistant", Content: "A says bye"})).To(Succeed())

				aMessages, err := store.GetByAgent("agent-a", 10)
				Expect(err).NotTo(HaveOccurred())
				Expect(aMessages).To(HaveLen(2))

				bMessages, err := store.GetByAgent("agent-b", 10)
				Expect(err).NotTo(HaveOccurred())
				Expect(bMessages).To(HaveLen(1))
				Expect(bMessages[0].Content).To(Equal("B says hello"))
			})
		})
	})

	Describe("GetByAgent", func() {
		Context("when requesting the last N messages", func() {
			It("returns only the N most recent messages", func() {
				store := recall.NewInMemoryChainStore(nil)
				for range 5 {
					Expect(store.Append("agent-a", provider.Message{
						Role:    "assistant",
						Content: "message",
					})).To(Succeed())
				}

				messages, err := store.GetByAgent("agent-a", 3)
				Expect(err).NotTo(HaveOccurred())
				Expect(messages).To(HaveLen(3))
			})
		})

		Context("when agentID is empty string", func() {
			It("returns messages from all agents combined, most recent first", func() {
				store := recall.NewInMemoryChainStore(nil)
				Expect(store.Append("agent-a", provider.Message{Role: "assistant", Content: "first"})).To(Succeed())
				Expect(store.Append("agent-b", provider.Message{Role: "assistant", Content: "second"})).To(Succeed())
				Expect(store.Append("agent-a", provider.Message{Role: "assistant", Content: "third"})).To(Succeed())

				all, err := store.GetByAgent("", 10)
				Expect(err).NotTo(HaveOccurred())
				Expect(all).To(HaveLen(3))
			})
		})

		Context("when agentID has no messages", func() {
			It("returns an empty slice without error", func() {
				store := recall.NewInMemoryChainStore(nil)

				messages, err := store.GetByAgent("unknown-agent", 5)
				Expect(err).NotTo(HaveOccurred())
				Expect(messages).To(BeEmpty())
			})
		})
	})

	Describe("Search", func() {
		Context("when embedding provider is nil (graceful degradation)", func() {
			It("falls back to GetByAgent with all messages up to topK", func() {
				store := recall.NewInMemoryChainStore(nil)
				Expect(store.Append("agent-a", provider.Message{Role: "assistant", Content: "alpha"})).To(Succeed())
				Expect(store.Append("agent-b", provider.Message{Role: "assistant", Content: "beta"})).To(Succeed())

				results, err := store.Search(context.Background(), "any query", 10)
				Expect(err).NotTo(HaveOccurred())
				Expect(results).To(HaveLen(2))
			})

			It("limits results to topK when there are more messages than topK", func() {
				store := recall.NewInMemoryChainStore(nil)
				for range 5 {
					Expect(store.Append("agent-a", provider.Message{Role: "assistant", Content: "message"})).To(Succeed())
				}

				results, err := store.Search(context.Background(), "any query", 3)
				Expect(err).NotTo(HaveOccurred())
				Expect(results).To(HaveLen(3))
			})

			It("returns empty results when store is empty", func() {
				store := recall.NewInMemoryChainStore(nil)

				results, err := store.Search(context.Background(), "any query", 5)
				Expect(err).NotTo(HaveOccurred())
				Expect(results).To(BeEmpty())
			})
		})
	})

	Describe("chain isolation", func() {
		It("two stores do not share messages", func() {
			storeA := recall.NewInMemoryChainStore(nil)
			storeB := recall.NewInMemoryChainStore(nil)

			Expect(storeA.Append("agent-a", provider.Message{Role: "assistant", Content: "private"})).To(Succeed())

			messages, err := storeB.GetByAgent("agent-a", 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(messages).To(BeEmpty())
		})
	})

	Describe("thread safety", func() {
		It("allows concurrent appends without race conditions", func() {
			store := recall.NewInMemoryChainStore(nil)
			done := make(chan struct{})

			for range 10 {
				go func() {
					defer func() { done <- struct{}{} }()
					_ = store.Append("agent-x", provider.Message{Role: "assistant", Content: "concurrent"})
				}()
			}

			for range 10 {
				<-done
			}

			messages, err := store.GetByAgent("agent-x", 20)
			Expect(err).NotTo(HaveOccurred())
			Expect(messages).To(HaveLen(10))
		})
	})
})
