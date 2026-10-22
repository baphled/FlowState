package todo_test

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
	todotool "github.com/baphled/flowstate/internal/tool/todo"
)

var _ = Describe("TodoInsertTool", func() {
	var (
		it    tool.Tool
		store *todotool.MemoryStore
	)

	BeforeEach(func() {
		store = todotool.NewMemoryStore()
		it = todotool.NewInsert(store)

		Expect(store.Set("sess-123", []todotool.Item{
			{Content: "Alpha", Status: "completed", Priority: "high"},
			{Content: "Gamma", Status: "pending", Priority: "medium"},
		})).To(Succeed())
	})

	Describe("Name", func() {
		It("returns todo_insert", func() {
			Expect(it.Name()).To(Equal("todo_insert"))
		})
	})

	Describe("Description", func() {
		It("returns a non-empty description", func() {
			Expect(it.Description()).NotTo(BeEmpty())
		})
	})

	Describe("Schema", func() {
		It("requires index and content", func() {
			s := it.Schema()
			Expect(s.Type).To(Equal("object"))
			Expect(s.Properties).To(HaveKey("index"))
			Expect(s.Properties).To(HaveKey("content"))
			Expect(s.Required).To(ConsistOf("index", "content"))
		})
	})

	Describe("Execute", func() {
		Context("when inserting at the beginning", func() {
			It("prepends the item and shifts others down", func() {
				result, err := it.Execute(sessionCtx(), tool.Input{
					Name: "todo_insert",
					Arguments: map[string]interface{}{
						"index":    float64(0),
						"content":  "Pre-task",
						"priority": "low",
					},
				})

				Expect(err).NotTo(HaveOccurred())
				var todos []todotool.Item
				Expect(json.Unmarshal([]byte(result.Output), &todos)).To(Succeed())
				Expect(todos).To(HaveLen(3))
				Expect(todos[0].Content).To(Equal("Pre-task"))
				Expect(todos[1].Content).To(Equal("Alpha"))
				Expect(todos[2].Content).To(Equal("Gamma"))
			})
		})

		Context("when inserting in the middle", func() {
			It("places the item at the index and shifts the rest", func() {
				result, err := it.Execute(sessionCtx(), tool.Input{
					Name: "todo_insert",
					Arguments: map[string]interface{}{
						"index":   float64(1),
						"content": "Beta",
						"status":  "in_progress",
					},
				})

				Expect(err).NotTo(HaveOccurred())
				var todos []todotool.Item
				Expect(json.Unmarshal([]byte(result.Output), &todos)).To(Succeed())
				Expect(todos).To(HaveLen(3))
				Expect(todos[0].Content).To(Equal("Alpha"))
				Expect(todos[1].Content).To(Equal("Beta"))
				Expect(todos[1].Status).To(Equal("in_progress"))
				Expect(todos[2].Content).To(Equal("Gamma"))
			})
		})

		Context("when inserting at the end (index == len)", func() {
			It("appends the item", func() {
				result, err := it.Execute(sessionCtx(), tool.Input{
					Name: "todo_insert",
					Arguments: map[string]interface{}{
						"index":   float64(2),
						"content": "Delta",
					},
				})

				Expect(err).NotTo(HaveOccurred())
				var todos []todotool.Item
				Expect(json.Unmarshal([]byte(result.Output), &todos)).To(Succeed())
				Expect(todos).To(HaveLen(3))
				Expect(todos[2].Content).To(Equal("Delta"))
			})
		})

		Context("when inserting with defaults", func() {
			It("defaults status to pending and priority to medium", func() {
				result, err := it.Execute(sessionCtx(), tool.Input{
					Name: "todo_insert",
					Arguments: map[string]interface{}{
						"index":   float64(0),
						"content": "Default insert",
					},
				})

				Expect(err).NotTo(HaveOccurred())
				var todos []todotool.Item
				Expect(json.Unmarshal([]byte(result.Output), &todos)).To(Succeed())
				Expect(todos[0].Status).To(Equal("pending"))
				Expect(todos[0].Priority).To(Equal("medium"))
			})
		})

		Context("when index is out of range (too large)", func() {
			It("returns an error and does not mutate the list", func() {
				_, err := it.Execute(sessionCtx(), tool.Input{
					Name: "todo_insert",
					Arguments: map[string]interface{}{
						"index":   float64(99),
						"content": "Bad",
					},
				})

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("out of range"))

				stored := store.Get("sess-123")
				Expect(stored).To(HaveLen(2))
			})
		})

		Context("when index is negative", func() {
			It("returns an error", func() {
				_, err := it.Execute(sessionCtx(), tool.Input{
					Name: "todo_insert",
					Arguments: map[string]interface{}{
						"index":   float64(-1),
						"content": "Bad",
					},
				})

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("out of range"))
			})
		})

		Context("when content is missing", func() {
			It("returns an error mentioning content", func() {
				_, err := it.Execute(sessionCtx(), tool.Input{
					Name: "todo_insert",
					Arguments: map[string]interface{}{
						"index": float64(0),
					},
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("content"))
			})
		})

		Context("when index is missing", func() {
			It("returns an error mentioning index", func() {
				_, err := it.Execute(sessionCtx(), tool.Input{
					Name: "todo_insert",
					Arguments: map[string]interface{}{
						"content": "Task",
					},
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("index"))
			})
		})

		Context("when session ID is missing from context", func() {
			It("returns an error", func() {
				_, err := it.Execute(context.Background(), tool.Input{
					Name: "todo_insert",
					Arguments: map[string]interface{}{
						"index":   float64(0),
						"content": "Task",
					},
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("session ID"))
			})
		})

		Context("when inserting into an empty list at index 0", func() {
			It("creates a single-item list", func() {
				Expect(store.Set("sess-empty", []todotool.Item{})).To(Succeed())
				ctx := context.WithValue(context.Background(), session.IDKey{}, "sess-empty")

				result, err := it.Execute(ctx, tool.Input{
					Name: "todo_insert",
					Arguments: map[string]interface{}{
						"index":   float64(0),
						"content": "First",
					},
				})

				Expect(err).NotTo(HaveOccurred())
				var todos []todotool.Item
				Expect(json.Unmarshal([]byte(result.Output), &todos)).To(Succeed())
				Expect(todos).To(HaveLen(1))
				Expect(todos[0].Content).To(Equal("First"))
			})
		})
	})
})
