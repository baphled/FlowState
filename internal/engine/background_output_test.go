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

var _ = Describe("BackgroundOutputTool", func() {
	var (
		manager *engine.BackgroundTaskManager
		botTool *engine.BackgroundOutputTool
		ctx     context.Context
	)

	BeforeEach(func() {
		manager = engine.NewBackgroundTaskManager()
		botTool = engine.NewBackgroundOutputTool(manager)
		ctx = context.Background()
	})

	Describe("Name", func() {
		It("returns 'background_output'", func() {
			Expect(botTool.Name()).To(Equal("background_output"))
		})
	})

	Describe("Description", func() {
		It("returns a non-empty description", func() {
			Expect(botTool.Description()).NotTo(BeEmpty())
		})
	})

	Describe("Schema", func() {
		It("includes task_id as required property", func() {
			schema := botTool.Schema()
			Expect(schema.Required).To(ContainElement("task_id"))
		})

		It("includes task_id property", func() {
			schema := botTool.Schema()
			Expect(schema.Properties).To(HaveKey("task_id"))
		})

		It("includes optional block property", func() {
			schema := botTool.Schema()
			Expect(schema.Properties).To(HaveKey("block"))
		})

		It("includes optional timeout property", func() {
			schema := botTool.Schema()
			Expect(schema.Properties).To(HaveKey("timeout"))
		})

		It("includes optional full_session property", func() {
			schema := botTool.Schema()
			Expect(schema.Properties).To(HaveKey("full_session"))
		})
	})

	Describe("Execute", func() {
		Context("when task_id is missing", func() {
			It("returns an error", func() {
				input := tool.Input{
					Name:      "background_output",
					Arguments: map[string]interface{}{},
				}
				_, err := botTool.Execute(ctx, input)
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when task is not found", func() {
			It("returns an error", func() {
				input := tool.Input{
					Name: "background_output",
					Arguments: map[string]interface{}{
						"task_id": "nonexistent-task",
					},
				}
				_, err := botTool.Execute(ctx, input)
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when task is running and block=false", func() {
			It("returns task status immediately", func() {
				task := manager.Launch(ctx, "task-1", "agent-1", "test task", func(ctx context.Context) (string, error) {
					time.Sleep(100 * time.Millisecond)
					return "done", nil
				})

				input := tool.Input{
					Name: "background_output",
					Arguments: map[string]interface{}{
						"task_id": task.ID,
						"block":   false,
					},
				}

				result, err := botTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())

				var output map[string]interface{}
				err = json.Unmarshal([]byte(result.Output), &output)
				Expect(err).NotTo(HaveOccurred())
				Expect(output).To(HaveKey("task_id"))
				Expect(output).To(HaveKey("status"))
			})
		})

		Context("when task is completed", func() {
			It("returns completed status with result", func() {
				task := manager.Launch(ctx, "task-2", "agent-2", "test task", func(ctx context.Context) (string, error) {
					return "result content", nil
				})

				time.Sleep(50 * time.Millisecond)

				input := tool.Input{
					Name: "background_output",
					Arguments: map[string]interface{}{
						"task_id": task.ID,
					},
				}

				result, err := botTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())

				var output map[string]interface{}
				err = json.Unmarshal([]byte(result.Output), &output)
				Expect(err).NotTo(HaveOccurred())
				Expect(output["status"]).To(Equal("completed"))
				Expect(output).To(HaveKey("result"))
			})
		})

		Context("when block=true and task completes", func() {
			It("polls until task completes", func() {
				task := manager.Launch(ctx, "task-3", "agent-3", "test task", func(ctx context.Context) (string, error) {
					time.Sleep(50 * time.Millisecond)
					return "blocking result", nil
				})

				input := tool.Input{
					Name: "background_output",
					Arguments: map[string]interface{}{
						"task_id": task.ID,
						"block":   true,
						"timeout": 5000,
					},
				}

				result, err := botTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())

				var output map[string]interface{}
				err = json.Unmarshal([]byte(result.Output), &output)
				Expect(err).NotTo(HaveOccurred())
				Expect(output["status"]).To(Equal("completed"))
			})
		})

		Context("when block=true and timeout is exceeded", func() {
			It("returns timeout error", func() {
				task := manager.Launch(ctx, "task-4", "agent-4", "test task", func(ctx context.Context) (string, error) {
					time.Sleep(1 * time.Second)
					return "result", nil
				})

				input := tool.Input{
					Name: "background_output",
					Arguments: map[string]interface{}{
						"task_id": task.ID,
						"block":   true,
						"timeout": 100,
					},
				}

				_, err := botTool.Execute(ctx, input)
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when full_session=true", func() {
			It("includes full_session flag in result", func() {
				task := manager.Launch(ctx, "task-5", "agent-5", "test task", func(ctx context.Context) (string, error) {
					return "session result", nil
				})

				time.Sleep(50 * time.Millisecond)

				input := tool.Input{
					Name: "background_output",
					Arguments: map[string]interface{}{
						"task_id":      task.ID,
						"full_session": true,
					},
				}

				result, err := botTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())

				var output map[string]interface{}
				err = json.Unmarshal([]byte(result.Output), &output)
				Expect(err).NotTo(HaveOccurred())
				Expect(output).To(HaveKey("full_session"))
				Expect(output["full_session"]).To(BeTrue())
			})
		})
	})
})
