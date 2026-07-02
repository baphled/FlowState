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

var _ = Describe("TodoClearTool", func() {
	var (
		ct    tool.Tool
		store *todotool.MemoryStore
	)

	BeforeEach(func() {
		store = todotool.NewMemoryStore()
		ct = todotool.NewClear(store)

		Expect(store.Set("sess-123", []todotool.Item{
			{Content: "first step", Status: "completed", Priority: "high"},
			{Content: "last step", Status: "completed", Priority: "medium"},
		})).To(Succeed())
	})

	Describe("Name", func() {
		It("returns todo_clear", func() {
			Expect(ct.Name()).To(Equal("todo_clear"))
		})
	})

	Describe("Description", func() {
		It("returns a non-empty description", func() {
			Expect(ct.Description()).NotTo(BeEmpty())
		})
	})

	Describe("Schema", func() {
		It("declares an object type with empty properties and no required fields", func() {
			s := ct.Schema()
			Expect(s.Type).To(Equal("object"))
			Expect(s.Properties).To(BeEmpty())
			Expect(s.Required).To(BeEmpty())
		})
	})

	Describe("Execute", func() {
		Context("when the session already has a todo list", func() {
			It("wipes the stored list and returns an empty JSON array", func() {
				result, err := ct.Execute(sessionCtx(), tool.Input{
					Name:      "todo_clear",
					Arguments: map[string]interface{}{},
				})

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(Equal("[]"))

				var todos []todotool.Item
				Expect(json.Unmarshal([]byte(result.Output), &todos)).To(Succeed())
				Expect(todos).To(BeEmpty())

				Expect(store.Get("sess-123")).To(BeEmpty())
			})
		})

		Context("when the session ID is missing from context", func() {
			It("returns an error mentioning the session ID", func() {
				_, err := ct.Execute(context.Background(), tool.Input{
					Name:      "todo_clear",
					Arguments: map[string]interface{}{},
				})

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("session ID"))
			})
		})
	})
})

var _ = Describe("TodoClearUnblocksTodowrite", func() {
	It("allows a fresh todowrite to create a new list after clearing", func() {
		store := todotool.NewMemoryStore()
		writeTool := todotool.New(store)
		clearTool := todotool.NewClear(store)
		ctx := context.WithValue(context.Background(), session.IDKey{}, "sess-unblock")

		_, err := writeTool.Execute(ctx, tool.Input{
			Name: "todowrite",
			Arguments: map[string]interface{}{
				"todos": []interface{}{
					map[string]interface{}{
						"content":  "first",
						"status":   "completed",
						"priority": "high",
					},
				},
			},
		})
		Expect(err).NotTo(HaveOccurred())

		_, err = clearTool.Execute(ctx, tool.Input{
			Name:      "todo_clear",
			Arguments: map[string]interface{}{},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(store.Get("sess-unblock")).To(BeEmpty())

		_, err = writeTool.Execute(ctx, tool.Input{
			Name: "todowrite",
			Arguments: map[string]interface{}{
				"todos": []interface{}{
					map[string]interface{}{
						"content":  "fresh task",
						"status":   "pending",
						"priority": "medium",
					},
				},
			},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(store.Get("sess-unblock")).To(HaveLen(1))
	})
})
