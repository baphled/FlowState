package todo_test

import (
	"context"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
	todotool "github.com/baphled/flowstate/internal/tool/todo"
)

var _ = Describe("Todo Store Concurrency", func() {
	Describe("MemoryStore Apply under concurrent batched updates", func() {
		It("preserves all patches when multiple goroutines update different items simultaneously", func() {
			store := todotool.NewMemoryStore()
			items := make([]todotool.Item, 10)
			for i := range items {
				items[i] = todotool.Item{
					Content:  "Task",
					Status:   "pending",
					Priority: "medium",
				}
			}
			Expect(store.Set("concurrent-test", items)).To(Succeed())

			updateTool := todotool.NewUpdate(store)
			ctx := sessionCtxWithID("concurrent-test")

			var wg sync.WaitGroup
			for i := 0; i < 10; i++ {
				wg.Add(1)
				go func(idx int) {
					defer wg.Done()
					_, err := updateTool.Execute(ctx, tool.Input{
						Name: "todo_update",
						Arguments: map[string]interface{}{
							"index":  float64(idx),
							"status": "completed",
						},
					})
					Expect(err).NotTo(HaveOccurred())
				}(i)
			}
			wg.Wait()

			final := store.Get("concurrent-test")
			Expect(final).To(HaveLen(10))
			for i, item := range final {
				Expect(item.Status).To(Equal("completed"),
					"item %d should be completed but is %s — race condition lost an update", i, item.Status)
			}
		})

		It("preserves all patches when batched in groups of 3 (matching executeToolCallBatch pattern)", func() {
			store := todotool.NewMemoryStore()
			items := make([]todotool.Item, 6)
			for i := range items {
				items[i] = todotool.Item{
					Content:  "Task",
					Status:   "pending",
					Priority: "medium",
				}
			}
			Expect(store.Set("batch-test", items)).To(Succeed())

			updateTool := todotool.NewUpdate(store)
			ctx := sessionCtxWithID("batch-test")

			for batchStart := 0; batchStart < 6; batchStart += 3 {
				var wg sync.WaitGroup
				for i := batchStart; i < batchStart+3 && i < 6; i++ {
					wg.Add(1)
					go func(idx int) {
						defer wg.Done()
						_, err := updateTool.Execute(ctx, tool.Input{
							Name: "todo_update",
							Arguments: map[string]interface{}{
								"index":  float64(idx),
								"status": "completed",
							},
						})
						Expect(err).NotTo(HaveOccurred())
					}(i)
				}
				wg.Wait()
			}

			final := store.Get("batch-test")
			Expect(final).To(HaveLen(6))
			for i, item := range final {
				Expect(item.Status).To(Equal("completed"),
					"item %d should be completed but is %s", i, item.Status)
			}
		})
	})

	Describe("MemoryStore Apply with append + update concurrency", func() {
		It("does not lose appends when concurrent updates are in flight", func() {
			store := todotool.NewMemoryStore()
			Expect(store.Set("mixed-test", []todotool.Item{
				{Content: "Original", Status: "pending", Priority: "high"},
			})).To(Succeed())

			updateTool := todotool.NewUpdate(store)
			appendTool := todotool.NewAppend(store)
			ctx := sessionCtxWithID("mixed-test")

			var wg sync.WaitGroup

			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := updateTool.Execute(ctx, tool.Input{
					Name: "todo_update",
					Arguments: map[string]interface{}{
						"index":  float64(0),
						"status": "completed",
					},
				})
				Expect(err).NotTo(HaveOccurred())
			}()

			for i := 0; i < 3; i++ {
				wg.Add(1)
				go func(n int) {
					defer wg.Done()
					_, err := appendTool.Execute(ctx, tool.Input{
						Name: "todo_append",
						Arguments: map[string]interface{}{
							"content": "Appended task",
						},
					})
					Expect(err).NotTo(HaveOccurred())
				}(i)
			}

			wg.Wait()

			final := store.Get("mixed-test")
			Expect(final).To(HaveLen(4), "1 original + 3 appends should survive concurrent update")
			Expect(final[0].Status).To(Equal("completed"))
		})
	})
})

func sessionCtxWithID(id string) context.Context {
	return context.WithValue(context.Background(), session.IDKey{}, id)
}
