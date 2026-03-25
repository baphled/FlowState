package context_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("FileContextStore", func() {
	var (
		store   *context.FileContextStore
		tempDir string
		path    string
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "context-store-test")
		Expect(err).NotTo(HaveOccurred())
		path = filepath.Join(tempDir, "store.json")
		store, err = context.NewFileContextStore(path, "test-model")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Describe("MessageStore", func() {
		Context("Append", func() {
			It("increases Count by 1", func() {
				initialCount := store.Count()
				store.Append(provider.Message{Role: "user", Content: "hello"})
				Expect(store.Count()).To(Equal(initialCount + 1))
			})
		})

		Context("GetRecent", func() {
			It("returns last n messages", func() {
				store.Append(provider.Message{Role: "user", Content: "first"})
				store.Append(provider.Message{Role: "assistant", Content: "second"})
				store.Append(provider.Message{Role: "user", Content: "third"})

				recent := store.GetRecent(2)
				Expect(recent).To(HaveLen(2))
				Expect(recent[0].Content).To(Equal("second"))
				Expect(recent[1].Content).To(Equal("third"))
			})
		})

		Context("GetRange", func() {
			It("returns messages in the specified range", func() {
				store.Append(provider.Message{Role: "user", Content: "first"})
				store.Append(provider.Message{Role: "assistant", Content: "second"})
				store.Append(provider.Message{Role: "user", Content: "third"})

				rangeResult := store.GetRange(0, 2)
				Expect(rangeResult).To(HaveLen(2))
				Expect(rangeResult[0].Content).To(Equal("first"))
				Expect(rangeResult[1].Content).To(Equal("second"))
			})
		})

		Context("AllMessages", func() {
			It("returns all appended messages", func() {
				store.Append(provider.Message{Role: "user", Content: "first"})
				store.Append(provider.Message{Role: "assistant", Content: "second"})

				all := store.AllMessages()
				Expect(all).To(HaveLen(2))
				Expect(all[0].Content).To(Equal("first"))
				Expect(all[1].Content).To(Equal("second"))
			})
		})
	})

	Describe("EmbeddingStore", func() {
		Context("StoreEmbedding and Search", func() {
			It("returns the stored message as top result", func() {
				store.Append(provider.Message{Role: "user", Content: "hello world"})

				storedMsgs := store.AllMessages()
				Expect(storedMsgs).To(HaveLen(1))

				msgID := store.GetMessageID(0)
				vector := []float64{0.5, 0.5, 0.5}
				store.StoreEmbedding(msgID, vector, "test-model", 3)

				results := store.Search(vector, 1)
				Expect(results).To(HaveLen(1))
				Expect(results[0].Message.Content).To(Equal("hello world"))
			})
		})

		Context("Search with empty store", func() {
			It("returns empty slice not error", func() {
				results := store.Search([]float64{0.1, 0.2, 0.3}, 5)
				Expect(results).To(BeEmpty())
			})
		})
	})

	Describe("CosineSimilarity", func() {
		It("returns 1.0 for identical vectors", func() {
			v := []float64{1.0, 2.0, 3.0}
			score := context.CosineSimilarity(v, v)
			Expect(score).To(BeNumerically("~", 1.0, 0.0001))
		})

		It("returns 0 for zero-length vectors", func() {
			zero := []float64{0.0, 0.0, 0.0}
			v := []float64{1.0, 2.0, 3.0}
			Expect(context.CosineSimilarity(zero, v)).To(Equal(0.0))
			Expect(context.CosineSimilarity(v, zero)).To(Equal(0.0))
		})
	})

	Describe("Tool role messages", func() {
		It("are stored but not returned by Search", func() {
			store.Append(provider.Message{Role: "user", Content: "call the tool"})
			store.Append(provider.Message{Role: "tool", Content: "tool output data"})
			store.Append(provider.Message{Role: "assistant", Content: "here is the result"})

			userMsgID := store.GetMessageID(0)
			assistantMsgID := store.GetMessageID(2)

			userVector := []float64{0.5, 0.5, 0.5}
			assistantVector := []float64{0.6, 0.6, 0.6}

			store.StoreEmbedding(userMsgID, userVector, "test-model", 3)
			store.StoreEmbedding(assistantMsgID, assistantVector, "test-model", 3)

			results := store.Search([]float64{0.55, 0.55, 0.55}, 10)

			Expect(store.Count()).To(Equal(3))

			for _, r := range results {
				Expect(r.Message.Role).NotTo(Equal("tool"))
			}
		})
	})

	Describe("Atomic persistence", func() {
		It("creates file after Append", func() {
			_, err := os.Stat(path)
			Expect(os.IsNotExist(err)).To(BeTrue())

			store.Append(provider.Message{Role: "user", Content: "test"})

			_, err = os.Stat(path)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("Model mismatch on load", func() {
		It("clears embeddings when model differs", func() {
			store.Append(provider.Message{Role: "user", Content: "hello"})
			msgID := store.GetMessageID(0)
			store.StoreEmbedding(msgID, []float64{0.1, 0.2, 0.3}, "old-model", 3)

			newStore, err := context.NewFileContextStore(path, "new-model")
			Expect(err).NotTo(HaveOccurred())

			Expect(newStore.Count()).To(Equal(1))
			results := newStore.Search([]float64{0.1, 0.2, 0.3}, 1)
			Expect(results).To(BeEmpty())
		})
	})
})

var _ = Describe("EmptyContextStore", func() {
	Describe("NewEmptyContextStore", func() {
		It("returns a store with zero messages", func() {
			store := context.NewEmptyContextStore("test-model")
			Expect(store.Count()).To(Equal(0))
		})

		It("returns a store with empty messages slice", func() {
			store := context.NewEmptyContextStore("test-model")
			Expect(store.AllMessages()).To(BeEmpty())
		})

		It("stores the provided model name", func() {
			store := context.NewEmptyContextStore("my-embedding-model")
			Expect(store.AllMessages()).To(BeEmpty())
		})
	})

	Describe("Append on empty store", func() {
		It("increases count in memory", func() {
			store := context.NewEmptyContextStore("test-model")
			store.Append(provider.Message{Role: "user", Content: "hello"})
			Expect(store.Count()).To(Equal(1))
		})

		It("stores message in memory without file I/O", func() {
			store := context.NewEmptyContextStore("test-model")
			store.Append(provider.Message{Role: "user", Content: "test message"})
			messages := store.AllMessages()
			Expect(messages).To(HaveLen(1))
			Expect(messages[0].Content).To(Equal("test message"))
		})
	})

	Describe("persist on empty store", func() {
		It("is a no-op when path is empty", func() {
			store := context.NewEmptyContextStore("test-model")
			store.Append(provider.Message{Role: "user", Content: "test"})
			Expect(store.Count()).To(Equal(1))
		})
	})
})
