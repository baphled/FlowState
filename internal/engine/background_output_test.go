package engine_test

import (
	"context"
	"encoding/json"
	"io"
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

		Context("when context is cancelled during blocking poll", func() {
			It("returns promptly instead of waiting for timeout", func() {
				task := manager.Launch(ctx, "poll-cancel-test", "agent-cancel", "cancel test", func(ctx context.Context) (string, error) {
					time.Sleep(10 * time.Second) // never completes
					return "done", nil
				})

				cancelCtx, cancel := context.WithCancel(context.Background())
				// Cancel after 100ms
				go func() {
					time.Sleep(100 * time.Millisecond)
					cancel()
				}()

				input := tool.Input{
					Name: "background_output",
					Arguments: map[string]interface{}{
						"task_id": task.ID,
						"block":   true,
						"timeout": float64(30000), // 30s timeout - should NOT wait this long
					},
				}
				start := time.Now()
				_, err := botTool.Execute(cancelCtx, input)
				elapsed := time.Since(start)

				// Should return within ~500ms, NOT wait 30 seconds
				Expect(err).To(HaveOccurred()) // timeout error since task never completes
				Expect(elapsed).To(BeNumerically("<", 2*time.Second))
			})
		})

		Context("regression: multiple sequential background_output calls", func() {
			It("successfully retrieves multiple different tasks without eviction conflicts", func() {
				// Launch two background tasks
				task1 := manager.Launch(ctx, "task-1", "agent-1", "first task", func(ctx context.Context) (string, error) {
					return "result 1", nil
				})
				task2 := manager.Launch(ctx, "task-2", "agent-2", "second task", func(ctx context.Context) (string, error) {
					return "result 2", nil
				})

				// Wait for both to complete
				Eventually(func() string {
					t, _ := manager.Get(task1.ID)
					return t.Status.Load()
				}, time.Second).Should(Equal("completed"))
				Eventually(func() string {
					t, _ := manager.Get(task2.ID)
					return t.Status.Load()
				}, time.Second).Should(Equal("completed"))

				// First background_output call
				input1 := tool.Input{
					Name: "background_output",
					Arguments: map[string]interface{}{
						"task_id": task1.ID,
					},
				}
				result1, err1 := botTool.Execute(ctx, input1)
				Expect(err1).NotTo(HaveOccurred())
				Expect(result1.Output).NotTo(BeEmpty())

				// Simulate eviction (as would happen after each tool result in the engine)
				manager.EvictCompleted()

				// Second background_output call should succeed (task2 is still not accessed)
				// but it should NOT have been evicted by the first call
				input2 := tool.Input{
					Name: "background_output",
					Arguments: map[string]interface{}{
						"task_id": task2.ID,
					},
				}
				result2, err2 := botTool.Execute(ctx, input2)
				Expect(err2).NotTo(HaveOccurred())
				Expect(result2.Output).NotTo(BeEmpty())

				// Verify task2 is now marked as accessed
				task2Updated, found := manager.Get(task2.ID)
				Expect(found).To(BeTrue())
				Expect(task2Updated.Status.Load()).To(Equal("completed"))
			})
		})
	})
})

// stubAutoresearchRunner is a test double for engine.AutoresearchRunner.
type stubAutoresearchRunner struct {
	result engine.AutoresearchResult
	err    error
}

func (s *stubAutoresearchRunner) RunAutoresearch(
	ctx context.Context,
	opts engine.AutoresearchOpts,
	out io.Writer,
) (engine.AutoresearchResult, error) {
	return s.result, s.err
}

var _ = Describe("AutoresearchRunTool", func() {
	var (
		manager *engine.BackgroundTaskManager
		artTool *engine.AutoresearchRunTool
		stub    *stubAutoresearchRunner
		ctx     context.Context
	)

	BeforeEach(func() {
		manager = engine.NewBackgroundTaskManager()
		stub = &stubAutoresearchRunner{
			result: engine.AutoresearchResult{
				RunID:             "stub-run",
				TerminationReason: "max-trials",
				TotalTrials:       1,
				Converged:         false,
				BestScore:         1.0,
			},
		}
		artTool = engine.NewAutoresearchRunTool(manager, stub)
		ctx = context.Background()
	})

	Describe("Name", func() {
		It("returns 'autoresearch_run'", func() {
			Expect(artTool.Name()).To(Equal("autoresearch_run"))
		})
	})

	Describe("Schema", func() {
		It("requires surface, driver_script, evaluator_script", func() {
			schema := artTool.Schema()
			Expect(schema.Required).To(ContainElement("surface"))
			Expect(schema.Required).To(ContainElement("driver_script"))
			Expect(schema.Required).To(ContainElement("evaluator_script"))
		})

		It("includes optional max_trials, time_budget, metric_direction, run_id", func() {
			schema := artTool.Schema()
			Expect(schema.Properties).To(HaveKey("max_trials"))
			Expect(schema.Properties).To(HaveKey("time_budget"))
			Expect(schema.Properties).To(HaveKey("metric_direction"))
			Expect(schema.Properties).To(HaveKey("run_id"))
		})
	})

	Context("AutoresearchRunTool", func() {
		It("Execute returns task_id and status=running immediately", func() {
			input := tool.Input{
				Name: "autoresearch_run",
				Arguments: map[string]any{
					"surface":          "/some/surface.md",
					"driver_script":    "/some/driver.sh",
					"evaluator_script": "/some/scorer.sh",
				},
			}
			result, err := artTool.Execute(ctx, input)
			Expect(err).NotTo(HaveOccurred())

			var output map[string]string
			Expect(json.Unmarshal([]byte(result.Output), &output)).To(Succeed())
			Expect(output).To(HaveKey("task_id"))
			Expect(output["task_id"]).NotTo(BeEmpty())
			Expect(output["status"]).To(Equal("running"))
		})

		It("returns an error when surface is missing", func() {
			input := tool.Input{
				Name: "autoresearch_run",
				Arguments: map[string]any{
					"driver_script":    "/some/driver.sh",
					"evaluator_script": "/some/scorer.sh",
				},
			}
			_, err := artTool.Execute(ctx, input)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("surface"))
		})

		It("returns an error when driver_script is missing", func() {
			input := tool.Input{
				Name: "autoresearch_run",
				Arguments: map[string]any{
					"surface":          "/some/surface.md",
					"evaluator_script": "/some/scorer.sh",
				},
			}
			_, err := artTool.Execute(ctx, input)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("driver_script"))
		})

		It("returns an error when evaluator_script is missing", func() {
			input := tool.Input{
				Name: "autoresearch_run",
				Arguments: map[string]any{
					"surface":       "/some/surface.md",
					"driver_script": "/some/driver.sh",
				},
			}
			_, err := artTool.Execute(ctx, input)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("evaluator_script"))
		})

		It("uses provided run_id as task_id", func() {
			input := tool.Input{
				Name: "autoresearch_run",
				Arguments: map[string]any{
					"surface":          "/some/surface.md",
					"driver_script":    "/some/driver.sh",
					"evaluator_script": "/some/scorer.sh",
					"run_id":           "explicit-run-id",
				},
			}
			result, err := artTool.Execute(ctx, input)
			Expect(err).NotTo(HaveOccurred())

			var output map[string]string
			Expect(json.Unmarshal([]byte(result.Output), &output)).To(Succeed())
			Expect(output["task_id"]).To(Equal("explicit-run-id"))
		})
	})

	Describe("CanDelegate", func() {
		It("returns true", func() {
			Expect(artTool.CanDelegate()).To(BeTrue())
		})
	})
})
