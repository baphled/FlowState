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

var _ = Describe("TodoAppendTool", func() {
	var (
		a     tool.Tool
		store *todotool.MemoryStore
	)

	BeforeEach(func() {
		store = todotool.NewMemoryStore()
		a = todotool.NewAppend(store)

		Expect(store.Set("sess-123", []todotool.Item{
			{Content: "First task", Status: "pending", Priority: "high"},
			{Content: "Second task", Status: "in_progress", Priority: "medium"},
		})).To(Succeed())
	})

	Describe("Name", func() {
		It("returns todo_append", func() {
			Expect(a.Name()).To(Equal("todo_append"))
		})
	})

	Describe("Description", func() {
		It("returns a non-empty description", func() {
			Expect(a.Description()).NotTo(BeEmpty())
		})
	})

	Describe("Schema", func() {
		It("requires content and exposes optional status and priority", func() {
			s := a.Schema()
			Expect(s.Type).To(Equal("object"))
			Expect(s.Properties).To(HaveKey("content"))
			Expect(s.Properties).To(HaveKey("status"))
			Expect(s.Properties).To(HaveKey("priority"))
			Expect(s.Required).To(ConsistOf("content"))
		})
	})

	Describe("Execute", func() {
		Context("when appending to a non-empty list", func() {
			It("adds the item at the end and returns the full list", func() {
				result, err := a.Execute(sessionCtx(), tool.Input{
					Name: "todo_append",
					Arguments: map[string]interface{}{
						"content":  "Third task",
						"status":   "pending",
						"priority": "low",
					},
				})

				Expect(err).NotTo(HaveOccurred())
				var todos []todotool.Item
				Expect(json.Unmarshal([]byte(result.Output), &todos)).To(Succeed())
				Expect(todos).To(HaveLen(3))
				Expect(todos[2].Content).To(Equal("Third task"))
				Expect(todos[2].Status).To(Equal("pending"))
				Expect(todos[2].Priority).To(Equal("low"))

				Expect(todos[0].Content).To(Equal("First task"))
				Expect(todos[1].Content).To(Equal("Second task"))
			})
		})

		Context("when appending with defaults", func() {
			It("defaults status to pending and priority to medium", func() {
				result, err := a.Execute(sessionCtx(), tool.Input{
					Name: "todo_append",
					Arguments: map[string]interface{}{
						"content": "Default task",
					},
				})

				Expect(err).NotTo(HaveOccurred())
				var todos []todotool.Item
				Expect(json.Unmarshal([]byte(result.Output), &todos)).To(Succeed())
				Expect(todos[2].Status).To(Equal("pending"))
				Expect(todos[2].Priority).To(Equal("medium"))
			})
		})

		Context("when appending to an empty list", func() {
			It("creates a single-item list", func() {
				Expect(store.Set("sess-empty", []todotool.Item{})).To(Succeed())
				ctx := context.WithValue(context.Background(), session.IDKey{}, "sess-empty")

				result, err := a.Execute(ctx, tool.Input{
					Name: "todo_append",
					Arguments: map[string]interface{}{
						"content": "Only task",
					},
				})

				Expect(err).NotTo(HaveOccurred())
				var todos []todotool.Item
				Expect(json.Unmarshal([]byte(result.Output), &todos)).To(Succeed())
				Expect(todos).To(HaveLen(1))
				Expect(todos[0].Content).To(Equal("Only task"))
			})
		})

		Context("when content is missing", func() {
			It("returns an error mentioning content", func() {
				_, err := a.Execute(sessionCtx(), tool.Input{
					Name:      "todo_append",
					Arguments: map[string]interface{}{},
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("content"))
			})
		})

		Context("when content is empty", func() {
			It("returns an error", func() {
				_, err := a.Execute(sessionCtx(), tool.Input{
					Name: "todo_append",
					Arguments: map[string]interface{}{
						"content": "",
					},
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("content"))
			})
		})

		Context("when session ID is missing from context", func() {
			It("returns an error", func() {
				_, err := a.Execute(context.Background(), tool.Input{
					Name: "todo_append",
					Arguments: map[string]interface{}{
						"content": "Task",
					},
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("session ID"))
			})
		})
	})
})
