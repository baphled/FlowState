package engine_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/engine"
)

var _ = Describe("BackgroundTaskManager", func() {
	var manager *engine.BackgroundTaskManager

	BeforeEach(func() {
		manager = engine.NewBackgroundTaskManager()
	})

	Describe("NewBackgroundTaskManager", func() {
		It("creates a new manager with empty task map", func() {
			Expect(manager).NotTo(BeNil())
			Expect(manager.ActiveCount()).To(Equal(0))
			Expect(manager.List()).To(BeEmpty())
		})
	})

	Describe("Launch", func() {
		Context("when launching a successful task", func() {
			It("completes the task and stores the result", func() {
				ctx := context.Background()
				task := manager.Launch(ctx, "task-1", "agent-1", "test task", func(ctx context.Context) (string, error) {
					time.Sleep(10 * time.Millisecond)
					return "task result", nil
				})

				Expect(task).NotTo(BeNil())
				Expect(task.ID).To(Equal("task-1"))
				Expect(task.AgentID).To(Equal("agent-1"))
				Expect(task.Description).To(Equal("test task"))

				Eventually(func() string {
					return task.Status.Load()
				}, "2s", "100ms").Should(SatisfyAny(Equal("running"), Equal("completed")))

				Eventually(func() string {
					t, _ := manager.Get("task-1")
					return t.Status.Load()
				}, "2s", "100ms").Should(Equal("completed"))

				t, found := manager.Get("task-1")
				Expect(found).To(BeTrue())
				Expect(t.Result).To(Equal("task result"))
				Expect(t.Error).ToNot(HaveOccurred())
			})
		})

		Context("when launching a failing task", func() {
			It("stores the error", func() {
				ctx := context.Background()
				expectedErr := errors.New("task failed")
				manager.Launch(ctx, "task-fail", "agent-1", "failing task", func(ctx context.Context) (string, error) {
					return "", expectedErr
				})

				Eventually(func() string {
					t, _ := manager.Get("task-fail")
					return t.Status.Load()
				}).Should(Equal("failed"))

				t, found := manager.Get("task-fail")
				Expect(found).To(BeTrue())
				Expect(t.Error).To(MatchError(expectedErr))
				Expect(t.Result).To(BeEmpty())
			})
		})

		Context("when context is cancelled", func() {
			It("marks the task as cancelled", func() {
				ctx, cancel := context.WithCancel(context.Background())
				manager.Launch(ctx, "task-cancel", "agent-1", "cancellable task", func(ctx context.Context) (string, error) {
					<-ctx.Done()
					return "", ctx.Err()
				})

				Eventually(func() string {
					t, _ := manager.Get("task-cancel")
					return t.Status.Load()
				}).Should(Equal("running"))

				cancel()

				Eventually(func() string {
					t, _ := manager.Get("task-cancel")
					return t.Status.Load()
				}).Should(Equal("cancelled"))
			})
		})

		Context("when context is already cancelled", func() {
			It("marks the task as cancelled immediately", func() {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				manager.Launch(ctx, "task-already-cancelled", "agent-1", "already cancelled task", func(ctx context.Context) (string, error) {
					return "", nil
				})

				Eventually(func() string {
					t, _ := manager.Get("task-already-cancelled")
					return t.Status.Load()
				}).Should(Equal("cancelled"))
			})
		})

		Context("with concurrent launches", func() {
			It("handles multiple concurrent tasks without races", func() {
				ctx := context.Background()
				const taskCount = 10

				for i := range taskCount {
					taskID := "concurrent-task-" + string(rune('0'+i))
					manager.Launch(ctx, taskID, "agent-1", "concurrent task", func(ctx context.Context) (string, error) {
						time.Sleep(5 * time.Millisecond)
						return "done", nil
					})
				}

				Eventually(func() int {
					return manager.ActiveCount()
				}).Should(Equal(taskCount))

				Eventually(func() int {
					completed := 0
					for _, t := range manager.List() {
						if t.Status.Load() == "completed" {
							completed++
						}
					}
					return completed
				}).Should(Equal(taskCount))
			})
		})
	})

	Describe("Get", func() {
		Context("when task exists", func() {
			It("returns the task and true", func() {
				ctx := context.Background()
				manager.Launch(ctx, "get-task", "agent-1", "get test", func(ctx context.Context) (string, error) {
					return "result", nil
				})

				Eventually(func() bool {
					t, found := manager.Get("get-task")
					return found && t != nil
				}).Should(BeTrue())

				t, found := manager.Get("get-task")
				Expect(found).To(BeTrue())
				Expect(t.ID).To(Equal("get-task"))
			})
		})

		Context("when task does not exist", func() {
			It("returns nil and false", func() {
				t, found := manager.Get("nonexistent")
				Expect(t).To(BeNil())
				Expect(found).To(BeFalse())
			})
		})
	})

	Describe("Cancel", func() {
		Context("when task is running", func() {
			It("cancels the context and marks the task", func() {
				ctx := context.Background()
				manager.Launch(ctx, "cancel-task", "agent-1", "cancel test", func(ctx context.Context) (string, error) {
					<-ctx.Done()
					return "", ctx.Err()
				})

				Eventually(func() string {
					t, _ := manager.Get("cancel-task")
					return t.Status.Load()
				}).Should(Equal("running"))

				err := manager.Cancel("cancel-task")
				Expect(err).NotTo(HaveOccurred())

				Eventually(func() string {
					t, _ := manager.Get("cancel-task")
					return t.Status.Load()
				}).Should(Equal("cancelled"))
			})
		})

		Context("when task does not exist", func() {
			It("returns an error", func() {
				err := manager.Cancel("nonexistent")
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when task is already completed", func() {
			It("returns an error", func() {
				ctx := context.Background()
				manager.Launch(ctx, "done-task", "agent-1", "done test", func(ctx context.Context) (string, error) {
					return "result", nil
				})

				Eventually(func() string {
					t, _ := manager.Get("done-task")
					return t.Status.Load()
				}).Should(Equal("completed"))

				err := manager.Cancel("done-task")
				Expect(err).To(HaveOccurred())
			})
		})
	})

	Describe("List", func() {
		Context("when tasks exist", func() {
			It("returns all tasks", func() {
				ctx := context.Background()
				manager.Launch(ctx, "list-1", "agent-1", "task 1", func(ctx context.Context) (string, error) {
					return "", nil
				})
				manager.Launch(ctx, "list-2", "agent-1", "task 2", func(ctx context.Context) (string, error) {
					return "", nil
				})

				Eventually(func() int {
					return len(manager.List())
				}).Should(BeNumerically(">=", 2))

				tasks := manager.List()
				Expect(tasks).To(HaveLen(2))

				taskIDs := make(map[string]bool)
				for _, t := range tasks {
					taskIDs[t.ID] = true
					_ = t.Status.Load()
				}
				Expect(taskIDs).To(HaveKey("list-1"))
				Expect(taskIDs).To(HaveKey("list-2"))
			})
		})

		Context("when no tasks exist", func() {
			It("returns an empty slice", func() {
				tasks := manager.List()
				Expect(tasks).To(BeEmpty())
			})
		})
	})

	Describe("EvictCompleted", func() {
		Context("when completed tasks exist", func() {
			It("removes terminal tasks from the map", func() {
				ctx := context.Background()
				manager.Launch(ctx, "evict-done", "agent-1", "evict test", func(ctx context.Context) (string, error) {
					return "result", nil
				})

				Eventually(func() string {
					t, _ := manager.Get("evict-done")
					return t.Status.Load()
				}, "2s", "50ms").Should(Equal("completed"))

				Expect(manager.List()).To(HaveLen(1))

				manager.EvictCompleted()

				Expect(manager.List()).To(BeEmpty())
				_, found := manager.Get("evict-done")
				Expect(found).To(BeFalse())
			})
		})

		Context("when running tasks exist alongside completed ones", func() {
			It("only removes terminal tasks", func() {
				ctx := context.Background()
				slow := make(chan struct{})
				manager.Launch(ctx, "evict-running", "agent-1", "running task", func(ctx context.Context) (string, error) {
					<-slow
					return "done", nil
				})
				manager.Launch(ctx, "evict-finished", "agent-1", "finished task", func(ctx context.Context) (string, error) {
					return "done", nil
				})

				Eventually(func() string {
					t, _ := manager.Get("evict-finished")
					return t.Status.Load()
				}, "2s", "50ms").Should(Equal("completed"))

				manager.EvictCompleted()

				_, runningFound := manager.Get("evict-running")
				Expect(runningFound).To(BeTrue())
				_, finishedFound := manager.Get("evict-finished")
				Expect(finishedFound).To(BeFalse())

				close(slow)
			})
		})
	})

	Describe("ActiveCount", func() {
		Context("with running tasks", func() {
			It("counts only running tasks", func() {
				ctx := context.Background()

				manager.Launch(ctx, "active-1", "agent-1", "active 1", func(ctx context.Context) (string, error) {
					time.Sleep(50 * time.Millisecond)
					return "done", nil
				})
				manager.Launch(ctx, "active-2", "agent-1", "active 2", func(ctx context.Context) (string, error) {
					time.Sleep(50 * time.Millisecond)
					return "done", nil
				})

				Eventually(func() int {
					return manager.ActiveCount()
				}).Should(Equal(2))

				Eventually(func() int {
					return manager.ActiveCount()
				}).Should(Equal(0))
			})
		})

		Context("with no tasks", func() {
			It("returns zero", func() {
				Expect(manager.ActiveCount()).To(Equal(0))
			})
		})
	})
})
