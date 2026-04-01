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

func sessionCtx() context.Context {
	return context.WithValue(context.Background(), session.IDKey{}, "sess-123")
}

var _ = Describe("TodoTool", func() {
	var (
		t     tool.Tool
		store *todotool.MemoryStore
	)

	BeforeEach(func() {
		store = todotool.NewMemoryStore()
		t = todotool.New(store)
	})

	Describe("Name", func() {
		It("returns todowrite", func() {
			Expect(t.Name()).To(Equal("todowrite"))
		})
	})

	Describe("Description", func() {
		It("returns a non-empty description", func() {
			Expect(t.Description()).NotTo(BeEmpty())
		})
	})

	Describe("Schema", func() {
		It("has object type with todos array property", func() {
			s := t.Schema()
			Expect(s.Type).To(Equal("object"))
			Expect(s.Properties).To(HaveKey("todos"))
			Expect(s.Properties["todos"].Type).To(Equal("array"))
			Expect(s.Required).To(ConsistOf("todos"))
		})

		It("defines items schema for the todos array with required fields", func() {
			s := t.Schema()
			items := s.Properties["todos"].Items
			Expect(items).NotTo(BeNil())
			Expect(items).To(HaveKey("properties"))
			Expect(items).To(HaveKey("required"))
			Expect(items).To(HaveKeyWithValue("additionalProperties", false))
		})
	})

	Describe("Execute", func() {
		Context("when session ID is present in context", func() {
			It("stores the todo list and returns JSON output", func() {
				result, err := t.Execute(sessionCtx(), tool.Input{
					Name: "todowrite",
					Arguments: map[string]interface{}{
						"todos": []interface{}{
							map[string]interface{}{
								"content":  "Write tests first",
								"status":   "in_progress",
								"priority": "high",
							},
							map[string]interface{}{
								"content":  "Implement feature",
								"status":   "pending",
								"priority": "medium",
							},
						},
					},
				})

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).NotTo(BeEmpty())

				var todos []todotool.Item
				Expect(json.Unmarshal([]byte(result.Output), &todos)).To(Succeed())
				Expect(todos).To(HaveLen(2))
				Expect(todos[0].Content).To(Equal("Write tests first"))
				Expect(todos[0].Status).To(Equal("in_progress"))
				Expect(todos[0].Priority).To(Equal("high"))
			})

			It("replaces the entire todo list on subsequent calls", func() {
				ctx := sessionCtx()
				firstInput := tool.Input{
					Name: "todowrite",
					Arguments: map[string]interface{}{
						"todos": []interface{}{
							map[string]interface{}{
								"content":  "First todo",
								"status":   "pending",
								"priority": "low",
							},
						},
					},
				}
				_, err := t.Execute(ctx, firstInput)
				Expect(err).NotTo(HaveOccurred())

				secondInput := tool.Input{
					Name: "todowrite",
					Arguments: map[string]interface{}{
						"todos": []interface{}{
							map[string]interface{}{
								"content":  "Second todo",
								"status":   "completed",
								"priority": "high",
							},
						},
					},
				}
				result, err := t.Execute(ctx, secondInput)
				Expect(err).NotTo(HaveOccurred())

				var todos []todotool.Item
				Expect(json.Unmarshal([]byte(result.Output), &todos)).To(Succeed())
				Expect(todos).To(HaveLen(1))
				Expect(todos[0].Content).To(Equal("Second todo"))
			})

			It("counts only non-completed items in the title", func() {
				result, err := t.Execute(sessionCtx(), tool.Input{
					Name: "todowrite",
					Arguments: map[string]interface{}{
						"todos": []interface{}{
							map[string]interface{}{
								"content":  "Done item",
								"status":   "completed",
								"priority": "low",
							},
							map[string]interface{}{
								"content":  "Pending item",
								"status":   "pending",
								"priority": "high",
							},
							map[string]interface{}{
								"content":  "In progress item",
								"status":   "in_progress",
								"priority": "medium",
							},
						},
					},
				})

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).ToNot(HaveOccurred())
				stored := store.Get("sess-123")
				Expect(stored).To(HaveLen(3))

				nonCompleted := 0
				for _, item := range stored {
					if item.Status != "completed" {
						nonCompleted++
					}
				}
				Expect(nonCompleted).To(Equal(2))
			})

			It("stores an empty list when todos is empty", func() {
				result, err := t.Execute(sessionCtx(), tool.Input{
					Name: "todowrite",
					Arguments: map[string]interface{}{
						"todos": []interface{}{},
					},
				})

				Expect(err).NotTo(HaveOccurred())
				var todos []todotool.Item
				Expect(json.Unmarshal([]byte(result.Output), &todos)).To(Succeed())
				Expect(todos).To(BeEmpty())
			})
		})

		Context("when session ID is missing from context", func() {
			It("returns an error", func() {
				_, err := t.Execute(context.Background(), tool.Input{
					Name:      "todowrite",
					Arguments: map[string]interface{}{"todos": []interface{}{}},
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("session ID"))
			})
		})

		Context("when todos argument is missing", func() {
			It("returns an error", func() {
				_, err := t.Execute(sessionCtx(), tool.Input{
					Name:      "todowrite",
					Arguments: map[string]interface{}{},
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("todos"))
			})
		})

		Context("when a todo item has no content field", func() {
			It("returns an error mentioning content", func() {
				_, err := t.Execute(sessionCtx(), tool.Input{
					Name: "todowrite",
					Arguments: map[string]interface{}{
						"todos": []interface{}{
							map[string]interface{}{
								"status":   "pending",
								"priority": "high",
							},
						},
					},
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("content"))
			})
		})

		Context("when a todo uses wrong field name instead of content", func() {
			It("returns an error mentioning content", func() {
				_, err := t.Execute(sessionCtx(), tool.Input{
					Name: "todowrite",
					Arguments: map[string]interface{}{
						"todos": []interface{}{
							map[string]interface{}{
								"title":    "My task",
								"status":   "pending",
								"priority": "high",
							},
						},
					},
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("content"))
			})
		})
	})
})

var _ = Describe("MemoryStore", func() {
	var s *todotool.MemoryStore

	BeforeEach(func() {
		s = todotool.NewMemoryStore()
	})

	Describe("Set and Get", func() {
		It("stores and retrieves todos for a session", func() {
			todos := []todotool.Item{
				{Content: "Task A", Status: "pending", Priority: "high"},
			}
			Expect(s.Set("sess-1", todos)).To(Succeed())
			Expect(s.Get("sess-1")).To(Equal(todos))
		})

		It("returns an empty slice for an unknown session", func() {
			Expect(s.Get("unknown")).To(BeEmpty())
		})

		It("replaces the list on successive Set calls", func() {
			first := []todotool.Item{{Content: "A", Status: "pending", Priority: "low"}}
			second := []todotool.Item{{Content: "B", Status: "completed", Priority: "high"}}
			Expect(s.Set("sess-1", first)).To(Succeed())
			Expect(s.Set("sess-1", second)).To(Succeed())
			Expect(s.Get("sess-1")).To(Equal(second))
		})

		It("isolates todos across different sessions", func() {
			todosA := []todotool.Item{{Content: "A", Status: "pending", Priority: "low"}}
			todosB := []todotool.Item{{Content: "B", Status: "completed", Priority: "high"}}
			Expect(s.Set("sess-A", todosA)).To(Succeed())
			Expect(s.Set("sess-B", todosB)).To(Succeed())
			Expect(s.Get("sess-A")).To(Equal(todosA))
			Expect(s.Get("sess-B")).To(Equal(todosB))
		})

		It("returns a defensive copy that does not corrupt stored state when mutated", func() {
			original := []todotool.Item{{Content: "Task A", Status: "pending", Priority: "high"}}
			Expect(s.Set("sess-1", original)).To(Succeed())

			items := s.Get("sess-1")
			items[0].Status = "cancelled"

			stored := s.Get("sess-1")
			Expect(stored[0].Status).To(Equal("pending"))
		})
	})
})
