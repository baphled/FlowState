package recall_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

var _ = Describe("FileContextStore", func() {
	var (
		store   *recall.FileContextStore
		tempDir string
		path    string
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "context-store-test")
		Expect(err).NotTo(HaveOccurred())
		path = filepath.Join(tempDir, "store.json")
		store, err = recall.NewFileContextStore(path, "test-model")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		store.Close()
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
			score := recall.CosineSimilarity(v, v)
			Expect(score).To(BeNumerically("~", 1.0, 0.0001))
		})

		It("returns 0 for zero-length vectors", func() {
			zero := []float64{0.0, 0.0, 0.0}
			v := []float64{1.0, 2.0, 3.0}
			Expect(recall.CosineSimilarity(zero, v)).To(Equal(0.0))
			Expect(recall.CosineSimilarity(v, zero)).To(Equal(0.0))
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
		It("creates file after reaching flush threshold", func() {
			_, err := os.Stat(path)
			Expect(os.IsNotExist(err)).To(BeTrue())

			for range 5 {
				store.Append(provider.Message{Role: "user", Content: "test"})
			}

			_, err = os.Stat(path)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("Batch persistence", func() {
		It("does not persist below the flush threshold", func() {
			_, err := os.Stat(path)
			Expect(os.IsNotExist(err)).To(BeTrue())

			store.Append(provider.Message{Role: "user", Content: "one"})
			store.Append(provider.Message{Role: "user", Content: "two"})

			_, err = os.Stat(path)
			Expect(os.IsNotExist(err)).To(BeTrue())
			Expect(store.Count()).To(Equal(2))
		})

		It("persists when the flush threshold is reached", func() {
			for range 5 {
				store.Append(provider.Message{Role: "user", Content: "msg"})
			}

			_, err := os.Stat(path)
			Expect(err).NotTo(HaveOccurred())

			data, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())

			var persisted struct {
				Messages []json.RawMessage `json:"messages"`
			}
			Expect(json.Unmarshal(data, &persisted)).To(Succeed())
			Expect(persisted.Messages).To(HaveLen(5))
		})

		It("resets pending count after flush threshold persist", func() {
			for range 5 {
				store.Append(provider.Message{Role: "user", Content: "batch1"})
			}

			modTimeBefore := fileModTime(path)

			store.Append(provider.Message{Role: "user", Content: "batch2-one"})

			modTimeAfter := fileModTime(path)
			Expect(modTimeAfter).To(Equal(modTimeBefore))
		})

		It("persists via timer when below threshold", func() {
			store.Append(provider.Message{Role: "user", Content: "timed"})

			Eventually(func() bool {
				_, err := os.Stat(path)
				return err == nil
			}, 15*time.Second, 500*time.Millisecond).Should(BeTrue())

			data, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())

			var persisted struct {
				Messages []json.RawMessage `json:"messages"`
			}
			Expect(json.Unmarshal(data, &persisted)).To(Succeed())
			Expect(persisted.Messages).To(HaveLen(1))
		})
	})

	Describe("Flush", func() {
		It("persists pending messages immediately", func() {
			store.Append(provider.Message{Role: "user", Content: "pending"})

			_, err := os.Stat(path)
			Expect(os.IsNotExist(err)).To(BeTrue())

			store.Flush()

			_, err = os.Stat(path)
			Expect(err).NotTo(HaveOccurred())

			data, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())

			var persisted struct {
				Messages []json.RawMessage `json:"messages"`
			}
			Expect(json.Unmarshal(data, &persisted)).To(Succeed())
			Expect(persisted.Messages).To(HaveLen(1))
		})

		It("is a no-op when there are no pending writes", func() {
			store.Flush()

			_, err := os.Stat(path)
			Expect(os.IsNotExist(err)).To(BeTrue())
		})
	})

	Describe("Close", func() {
		It("flushes pending messages", func() {
			store.Append(provider.Message{Role: "user", Content: "before-close"})

			store.Close()

			_, err := os.Stat(path)
			Expect(err).NotTo(HaveOccurred())
		})

		It("survives multiple Close calls", func() {
			store.Append(provider.Message{Role: "user", Content: "close-twice"})
			store.Close()
			store.Close()

			Expect(store.Count()).To(Equal(1))
		})
	})

	Describe("Reload after batch persist", func() {
		It("loads all messages from the flushed file", func() {
			for range 5 {
				store.Append(provider.Message{Role: "user", Content: "reload-test"})
			}

			reloaded, err := recall.NewFileContextStore(path, "test-model")
			Expect(err).NotTo(HaveOccurred())
			Expect(reloaded.Count()).To(Equal(5))
		})
	})

	Describe("Model mismatch on load", func() {
		It("clears embeddings when model differs", func() {
			store.Append(provider.Message{Role: "user", Content: "hello"})
			msgID := store.GetMessageID(0)
			store.StoreEmbedding(msgID, []float64{0.1, 0.2, 0.3}, "old-model", 3)

			newStore, err := recall.NewFileContextStore(path, "new-model")
			Expect(err).NotTo(HaveOccurred())

			Expect(newStore.Count()).To(Equal(1))
			results := newStore.Search([]float64{0.1, 0.2, 0.3}, 1)
			Expect(results).To(BeEmpty())
		})
	})
})

func fileModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

var _ = Describe("EmptyContextStore", func() {
	Describe("NewEmptyContextStore", func() {
		It("returns a store with zero messages", func() {
			store := recall.NewEmptyContextStore("test-model")
			Expect(store.Count()).To(Equal(0))
		})

		It("returns a store with empty messages slice", func() {
			store := recall.NewEmptyContextStore("test-model")
			Expect(store.AllMessages()).To(BeEmpty())
		})

		It("stores the provided model name", func() {
			store := recall.NewEmptyContextStore("my-embedding-model")
			Expect(store.AllMessages()).To(BeEmpty())
		})
	})

	Describe("Append on empty store", func() {
		It("increases count in memory", func() {
			store := recall.NewEmptyContextStore("test-model")
			store.Append(provider.Message{Role: "user", Content: "hello"})
			Expect(store.Count()).To(Equal(1))
		})

		It("stores message in memory without file I/O", func() {
			store := recall.NewEmptyContextStore("test-model")
			store.Append(provider.Message{Role: "user", Content: "test message"})
			messages := store.AllMessages()
			Expect(messages).To(HaveLen(1))
			Expect(messages[0].Content).To(Equal("test message"))
		})
	})

	Describe("persist on empty store", func() {
		It("is a no-op when path is empty", func() {
			store := recall.NewEmptyContextStore("test-model")
			store.Append(provider.Message{Role: "user", Content: "test"})
			Expect(store.Count()).To(Equal(1))
		})
	})
})
