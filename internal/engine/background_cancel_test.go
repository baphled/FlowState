package engine_test

import (
	"context"
	"encoding/json"
	"time"

	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/tool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("BackgroundCancelTool", func() {
	var (
		manager *engine.BackgroundTaskManager
		bctTool *engine.BackgroundCancelTool
		ctx     context.Context
	)

	BeforeEach(func() {
		manager = engine.NewBackgroundTaskManager()
		bctTool = engine.NewBackgroundCancelTool(manager)
		ctx = context.Background()
	})

	Describe("Name", func() {
		It("returns 'background_cancel'", func() {
			Expect(bctTool.Name()).To(Equal("background_cancel"))
		})
	})

	Describe("Description", func() {
		It("returns a non-empty description", func() {
			Expect(bctTool.Description()).NotTo(BeEmpty())
		})
	})

	Describe("Schema", func() {
		It("includes task_id as optional property", func() {
			schema := bctTool.Schema()
			Expect(schema.Properties).To(HaveKey("task_id"))
		})

		It("includes all as optional property", func() {
			schema := bctTool.Schema()
			Expect(schema.Properties).To(HaveKey("all"))
		})

		It("does not require any properties", func() {
			schema := bctTool.Schema()
			Expect(schema.Required).To(BeEmpty())
		})
	})

	Describe("Execute", func() {
		Context("when cancelling a specific running task by task_id", func() {
			It("returns cancelled task ID", func() {
				task := manager.Launch(ctx, "task-1", "agent-1", "test task", func(ctx context.Context) (string, error) {
					time.Sleep(500 * time.Millisecond)
					return "result", nil
				})

				input := tool.Input{
					Name: "background_cancel",
					Arguments: map[string]interface{}{
						"task_id": task.ID,
					},
				}

				result, err := bctTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())

				var output map[string]interface{}
				err = json.Unmarshal([]byte(result.Output), &output)
				Expect(err).NotTo(HaveOccurred())

				cancelled, ok := output["cancelled"].([]interface{})
				Expect(ok).To(BeTrue())
				Expect(cancelled).To(HaveLen(1))
				Expect(cancelled[0]).To(Equal(task.ID))
			})
		})

		Context("when cancelling a specific pending task", func() {
			It("returns cancelled task ID", func() {
				task := manager.Launch(ctx, "task-2", "agent-2", "test task", func(ctx context.Context) (string, error) {
					time.Sleep(500 * time.Millisecond)
					return "result", nil
				})

				input := tool.Input{
					Name: "background_cancel",
					Arguments: map[string]interface{}{
						"task_id": task.ID,
					},
				}

				result, err := bctTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())

				var output map[string]interface{}
				err = json.Unmarshal([]byte(result.Output), &output)
				Expect(err).NotTo(HaveOccurred())

				cancelled, ok := output["cancelled"].([]interface{})
				Expect(ok).To(BeTrue())
				Expect(cancelled).To(HaveLen(1))
				Expect(cancelled[0]).To(Equal(task.ID))
			})
		})

		Context("when cancelling a non-existent task", func() {
			It("returns an error", func() {
				input := tool.Input{
					Name: "background_cancel",
					Arguments: map[string]interface{}{
						"task_id": "nonexistent-task",
					},
				}

				_, err := bctTool.Execute(ctx, input)
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when cancelling an already-completed task", func() {
			It("returns an error", func() {
				task := manager.Launch(ctx, "task-3", "agent-3", "test task", func(ctx context.Context) (string, error) {
					return "result", nil
				})

				time.Sleep(50 * time.Millisecond)

				input := tool.Input{
					Name: "background_cancel",
					Arguments: map[string]interface{}{
						"task_id": task.ID,
					},
				}

				_, err := bctTool.Execute(ctx, input)
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when all=true", func() {
			It("cancels all running and pending tasks", func() {
				task1 := manager.Launch(ctx, "task-4", "agent-4", "test task 1", func(ctx context.Context) (string, error) {
					time.Sleep(500 * time.Millisecond)
					return "result", nil
				})

				task2 := manager.Launch(ctx, "task-5", "agent-5", "test task 2", func(ctx context.Context) (string, error) {
					time.Sleep(500 * time.Millisecond)
					return "result", nil
				})

				task3 := manager.Launch(ctx, "task-6", "agent-6", "test task 3", func(ctx context.Context) (string, error) {
					time.Sleep(500 * time.Millisecond)
					return "result", nil
				})

				input := tool.Input{
					Name: "background_cancel",
					Arguments: map[string]interface{}{
						"all": true,
					},
				}

				result, err := bctTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())

				var output map[string]interface{}
				err = json.Unmarshal([]byte(result.Output), &output)
				Expect(err).NotTo(HaveOccurred())

				cancelled, ok := output["cancelled"].([]interface{})
				Expect(ok).To(BeTrue())
				Expect(cancelled).To(HaveLen(3))
				Expect(cancelled).To(ContainElement(task1.ID))
				Expect(cancelled).To(ContainElement(task2.ID))
				Expect(cancelled).To(ContainElement(task3.ID))
			})
		})

		Context("when all=true and no tasks are running", func() {
			It("returns empty list of cancelled tasks", func() {
				input := tool.Input{
					Name: "background_cancel",
					Arguments: map[string]interface{}{
						"all": true,
					},
				}

				result, err := bctTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())

				var output map[string]interface{}
				err = json.Unmarshal([]byte(result.Output), &output)
				Expect(err).NotTo(HaveOccurred())

				cancelled, ok := output["cancelled"].([]interface{})
				Expect(ok).To(BeTrue())
				Expect(cancelled).To(BeEmpty())
			})
		})

		Context("when neither task_id nor all is provided", func() {
			It("returns an error", func() {
				input := tool.Input{
					Name:      "background_cancel",
					Arguments: map[string]interface{}{},
				}

				_, err := bctTool.Execute(ctx, input)
				Expect(err).To(HaveOccurred())
			})
		})
	})
})
