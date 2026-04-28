package engine_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
)

// fakeStreamer is a minimal streaming.Streamer implementation for tests.
type fakeStreamer struct{}

func (f *fakeStreamer) Stream(_ context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}

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
					_, found := manager.Get("get-task")
					return found
				}).Should(BeTrue())

				t, found := manager.Get("get-task")
				Expect(found).To(BeTrue())
				Expect(t.ID).To(Equal("get-task"))
			})
		})

		Context("when task does not exist", func() {
			It("returns zero value and false", func() {
				t, found := manager.Get("nonexistent")
				Expect(found).To(BeFalse())
				Expect(t.ID).To(BeEmpty())
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
		// EvictCompleted's new contract (per the BackgroundTaskManager
		// premature-eviction fix) waits BackgroundTaskEvictionGrace
		// after MarkAccessed before allowing a task to be evicted.
		// These specs assert the eviction *can* happen, so we shrink
		// the grace to zero up-front; the grace-window behaviour
		// itself gets its own dedicated specs further down.
		var origGrace time.Duration

		BeforeEach(func() {
			origGrace = engine.BackgroundTaskEvictionGrace
			engine.BackgroundTaskEvictionGrace = 0
		})

		AfterEach(func() {
			engine.BackgroundTaskEvictionGrace = origGrace
		})

		Context("when completed tasks exist", func() {
			It("removes terminal tasks from the map when they are marked as accessed", func() {
				ctx := context.Background()
				manager.Launch(ctx, "evict-done", "agent-1", "evict test", func(ctx context.Context) (string, error) {
					return "result", nil
				})

				Eventually(func() string {
					t, _ := manager.Get("evict-done")
					return t.Status.Load()
				}, "2s", "50ms").Should(Equal("completed"))

				Expect(manager.List()).To(HaveLen(1))

				// Mark task as accessed before eviction
				manager.MarkAccessed("evict-done")
				manager.EvictCompleted()

				Expect(manager.List()).To(BeEmpty())
				_, found := manager.Get("evict-done")
				Expect(found).To(BeFalse())
			})

			It("preserves completed tasks that have not been accessed", func() {
				ctx := context.Background()
				manager.Launch(ctx, "evict-done", "agent-1", "evict test", func(ctx context.Context) (string, error) {
					return "result", nil
				})

				Eventually(func() string {
					t, _ := manager.Get("evict-done")
					return t.Status.Load()
				}, "2s", "50ms").Should(Equal("completed"))

				Expect(manager.List()).To(HaveLen(1))

				// Do NOT mark as accessed - task should survive eviction
				manager.EvictCompleted()

				// Task should still be in the list
				Expect(manager.List()).To(HaveLen(1))
				_, found := manager.Get("evict-done")
				Expect(found).To(BeTrue())
			})
		})

		Context("when a task is accessed and eviction is deferred", func() {
			It("remains accessible until eviction is explicitly called", func() {
				ctx := context.Background()
				manager.Launch(ctx, "deferred-task", "agent-1", "deferred eviction test", func(ctx context.Context) (string, error) {
					return "deferred result", nil
				})

				Eventually(func() string {
					t, _ := manager.Get("deferred-task")
					return t.Status.Load()
				}, "2s", "50ms").Should(Equal("completed"))

				// Read the task via Get — simulates tool reading output
				t, found := manager.Get("deferred-task")
				Expect(found).To(BeTrue())
				Expect(t.Result).To(Equal("deferred result"))

				// Mark as accessed
				manager.MarkAccessed("deferred-task")

				// Task should still be retrievable before eviction runs
				t2, found2 := manager.Get("deferred-task")
				Expect(found2).To(BeTrue())
				Expect(t2.Result).To(Equal("deferred result"))

				// Now call EvictCompleted — task should be removed
				manager.EvictCompleted()

				_, found3 := manager.Get("deferred-task")
				Expect(found3).To(BeFalse())
				Expect(manager.List()).To(BeEmpty())
			})
		})

		Context("when running tasks exist alongside completed ones", func() {
			It("only removes accessed terminal tasks and preserves active/unaccessed ones", func() {
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

				// Mark finished task as accessed
				manager.MarkAccessed("evict-finished")
				manager.EvictCompleted()

				_, runningFound := manager.Get("evict-running")
				Expect(runningFound).To(BeTrue())
				_, finishedFound := manager.Get("evict-finished")
				Expect(finishedFound).To(BeFalse())

				close(slow)
			})
		})

		// New specs pinning the grace-window contract introduced to fix
		// the "task not found" cascade observed in session
		// 175b873e-5ee5-4917-b217-0efa6a4417d9: a lead delegated to
		// members, the eviction defer at end-of-tool-loop fired between
		// the lead's first read and second read of background_output,
		// and 12 of the lead's subsequent re-reads errored out.
		Context("when an accessed terminal task is still within its grace window", func() {
			It("preserves the task across multiple background_output re-reads", func() {
				engine.BackgroundTaskEvictionGrace = 5 * time.Second

				ctx := context.Background()
				manager.Launch(ctx, "evict-grace", "agent-1", "grace test", func(ctx context.Context) (string, error) {
					return "result", nil
				})

				Eventually(func() string {
					t, _ := manager.Get("evict-grace")
					return t.Status.Load()
				}, "2s", "50ms").Should(Equal("completed"))

				manager.MarkAccessed("evict-grace")
				manager.EvictCompleted()

				_, found := manager.Get("evict-grace")
				Expect(found).To(BeTrue(),
					"a grace-window-protected task must survive the first eviction sweep "+
						"so the lead's subsequent background_output(task_id) calls succeed")
			})

			It("evicts the task once the grace window expires", func() {
				// Override the grace to a tiny duration we can wait
				// past with a controlled time source. We use a real
				// sleep here (5ms) instead of mocking time because
				// the BackgroundTaskManager reads time.Now()
				// directly inside EvictCompleted; threading a clock
				// through would expand the diff beyond the eviction
				// fix.
				engine.BackgroundTaskEvictionGrace = 5 * time.Millisecond

				ctx := context.Background()
				manager.Launch(ctx, "evict-grace-expire", "agent-1", "grace expire", func(ctx context.Context) (string, error) {
					return "result", nil
				})

				Eventually(func() string {
					t, _ := manager.Get("evict-grace-expire")
					return t.Status.Load()
				}, "2s", "50ms").Should(Equal("completed"))

				manager.MarkAccessed("evict-grace-expire")
				time.Sleep(20 * time.Millisecond)
				manager.EvictCompleted()

				_, found := manager.Get("evict-grace-expire")
				Expect(found).To(BeFalse(),
					"once accessedAt + EvictionGrace has passed, the task must "+
						"finally evict so memory pressure stays bounded")
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

	Describe("ActiveCountForSession", func() {
		Context("when no tasks exist", func() {
			It("returns zero", func() {
				Expect(manager.ActiveCountForSession("session-a")).To(Equal(0))
			})
		})

		Context("when tasks belong to different sessions", func() {
			It("counts only active tasks for the specified session", func() {
				blockCh := make(chan struct{})

				ctxA := context.WithValue(context.Background(), session.IDKey{}, "session-a")
				ctxB := context.WithValue(context.Background(), session.IDKey{}, "session-b")

				manager.Launch(ctxA, "a-task-1", "agent-1", "session A task 1", func(ctx context.Context) (string, error) {
					<-blockCh
					return "done", nil
				})
				manager.Launch(ctxA, "a-task-2", "agent-1", "session A task 2", func(ctx context.Context) (string, error) {
					<-blockCh
					return "done", nil
				})
				manager.Launch(ctxB, "b-task-1", "agent-1", "session B task 1", func(ctx context.Context) (string, error) {
					<-blockCh
					return "done", nil
				})

				Eventually(func() int {
					return manager.ActiveCount()
				}, "2s", "50ms").Should(Equal(3))

				Expect(manager.ActiveCountForSession("session-a")).To(Equal(2))
				Expect(manager.ActiveCountForSession("session-b")).To(Equal(1))

				close(blockCh)

				Eventually(func() int {
					return manager.ActiveCount()
				}, "2s", "50ms").Should(Equal(0))
			})
		})

		Context("when a task has completed", func() {
			It("excludes completed tasks from the count", func() {
				ctx := context.WithValue(context.Background(), session.IDKey{}, "session-c")
				manager.Launch(ctx, "c-task-1", "agent-1", "completing task", func(ctx context.Context) (string, error) {
					return "done", nil
				})

				Eventually(func() string {
					t, _ := manager.Get("c-task-1")
					return t.Status.Load()
				}, "2s", "50ms").Should(Equal("completed"))

				Expect(manager.ActiveCountForSession("session-c")).To(Equal(0))
			})
		})

		Context("when session ID is unknown", func() {
			It("returns zero", func() {
				ctx := context.WithValue(context.Background(), session.IDKey{}, "session-d")
				manager.Launch(ctx, "d-task-1", "agent-1", "known session task", func(ctx context.Context) (string, error) {
					time.Sleep(50 * time.Millisecond)
					return "done", nil
				})

				Eventually(func() int {
					return manager.ActiveCount()
				}, "2s", "50ms").Should(BeNumerically(">=", 1))

				Expect(manager.ActiveCountForSession("unknown-session")).To(Equal(0))
			})
		})
	})

	Describe("Per-Key Concurrency Limiting", func() {
		Context("when tasks have the same concurrency key", func() {
			It("limits concurrent running tasks to MaxPerKey", func() {
				ctx := context.Background()

				blockCh := make(chan struct{})
				startedCh := make(chan struct{}, 5)

				for i := range 5 {
					taskID := "key-task-" + string(rune('0'+i))
					manager.Launch(ctx, taskID, "anthropic", "test task", func(ctx context.Context) (string, error) {
						startedCh <- struct{}{}
						<-blockCh
						return "done", nil
					})
				}

				started := 0
				deadline := time.Now().Add(1 * time.Second)
				for started < 3 && time.Now().Before(deadline) {
					select {
					case <-startedCh:
						started++
					case <-time.After(100 * time.Millisecond):
					}
				}

				Expect(started).To(Equal(3))

				time.Sleep(100 * time.Millisecond)

				activeCount := 0
				allTasks := manager.List()
				for _, t := range allTasks {
					if t.Status.Load() == "running" {
						activeCount++
					}
				}
				Expect(activeCount).To(Equal(3))

				close(blockCh)

				Eventually(func() int {
					return manager.ActiveCount()
				}).Should(Equal(0))
			})
		})

		Context("when tasks have different concurrency keys", func() {
			It("enforces separate limits per key", func() {
				ctx := context.Background()

				blockCh1 := make(chan struct{})
				blockCh2 := make(chan struct{})
				countCh := make(chan int, 100)

				for i := range 3 {
					taskID := "anthropic-task-" + string(rune('0'+i))
					manager.Launch(ctx, taskID, "anthropic", "test task", func(ctx context.Context) (string, error) {
						countCh <- manager.ActiveCount()
						<-blockCh1
						return "done", nil
					})
				}

				for i := range 3 {
					taskID := "openai-task-" + string(rune('0'+i))
					manager.Launch(ctx, taskID, "openai", "test task", func(ctx context.Context) (string, error) {
						countCh <- manager.ActiveCount()
						<-blockCh2
						return "done", nil
					})
				}

				time.Sleep(500 * time.Millisecond)

				Eventually(func() int {
					return manager.ActiveCount()
				}).Should(Equal(6))

				close(blockCh1)
				close(blockCh2)

				Eventually(func() int {
					return manager.ActiveCount()
				}).Should(Equal(0))
			})
		})

		Context("when total concurrent tasks exceed MaxTotal", func() {
			It("enforces total concurrency limit", func() {
				ctx := context.Background()

				blockCh := make(chan struct{})
				countCh := make(chan int, 100)

				for i := range 55 {
					taskID := "total-task-" + string(rune('0'+(i%10)))
					manager.Launch(ctx, taskID, "provider-"+string(rune('a'+(i/20))), "test task", func(ctx context.Context) (string, error) {
						countCh <- manager.ActiveCount()
						<-blockCh
						return "done", nil
					})
				}

				time.Sleep(500 * time.Millisecond)

				maxConcurrent := 0
				for len(countCh) > 0 {
					count := <-countCh
					if count > maxConcurrent {
						maxConcurrent = count
					}
				}

				Expect(maxConcurrent).To(BeNumerically("<=", 50))

				close(blockCh)

				Eventually(func() int {
					return manager.ActiveCount()
				}).Should(Equal(0))
			})
		})

		Context("ConcurrencyKey field", func() {
			It("is set on launched tasks", func() {
				ctx := context.Background()
				task := manager.Launch(ctx, "keyed-task", "my-agent", "test", func(ctx context.Context) (string, error) {
					return "ok", nil
				})

				Expect(task.ConcurrencyKey).To(Equal("my-agent"))
			})
		})
	})

	Describe("ParentSessionID threading", func() {
		Context("when session ID is in context", func() {
			It("sets ParentSessionID on the task", func() {
				ctx := context.WithValue(context.Background(), session.IDKey{}, "ses-123")
				task := manager.Launch(ctx, "sess-task", "agent-1", "session test", func(ctx context.Context) (string, error) {
					return "ok", nil
				})

				Expect(task.ParentSessionID).To(Equal("ses-123"))
			})
		})

		Context("when no session ID is in context", func() {
			It("leaves ParentSessionID empty", func() {
				ctx := context.Background()
				task := manager.Launch(ctx, "no-sess-task", "agent-1", "no session test", func(ctx context.Context) (string, error) {
					return "ok", nil
				})

				Expect(task.ParentSessionID).To(BeEmpty())
			})
		})
	})

	Describe("Completion notification injection", func() {
		Context("when session manager and parent session ID are set", func() {
			It("injects a completion notification into the session manager", func() {
				streamer := &fakeStreamer{}
				sessionMgr := session.NewManager(streamer)
				manager.WithSessionManager(sessionMgr)

				ctx := context.WithValue(context.Background(), session.IDKey{}, "notif-sess")
				manager.Launch(ctx, "notif-task", "agent-1", "notification test", func(ctx context.Context) (string, error) {
					return "task output", nil
				})

				Eventually(func() string {
					t, _ := manager.Get("notif-task")
					return t.Status.Load()
				}, "2s", "50ms").Should(Equal("completed"))

				var notifications []streaming.CompletionNotificationEvent
				var err error
				Eventually(func() []streaming.CompletionNotificationEvent {
					notifications, err = sessionMgr.GetNotifications("notif-sess")
					Expect(err).NotTo(HaveOccurred())
					return notifications
				}, "2s", "50ms").Should(HaveLen(1))

				Expect(notifications).To(HaveLen(1))
				Expect(notifications[0].TaskID).To(Equal("notif-task"))
				Expect(notifications[0].Agent).To(Equal("agent-1"))
				Expect(notifications[0].Status).To(Equal("completed"))
			})
		})

		Context("when session manager is nil", func() {
			It("does not panic", func() {
				ctx := context.WithValue(context.Background(), session.IDKey{}, "no-mgr-sess")
				manager.Launch(ctx, "no-mgr-task", "agent-1", "no manager test", func(ctx context.Context) (string, error) {
					return "ok", nil
				})

				Eventually(func() string {
					t, _ := manager.Get("no-mgr-task")
					return t.Status.Load()
				}, "2s", "50ms").Should(Equal("completed"))
			})
		})
	})

	Describe("CompletionSubscriber", func() {
		Context("when a task completes", func() {
			It("sends a notification on the subscriber channel", func() {
				ch := make(chan streaming.CompletionNotificationEvent, 1)
				manager.SetCompletionSubscriber(ch)

				streamer := &fakeStreamer{}
				sessionMgr := session.NewManager(streamer)
				manager.WithSessionManager(sessionMgr)

				ctx := context.WithValue(context.Background(), session.IDKey{}, "sub-sess")
				manager.Launch(ctx, "sub-task", "agent-1", "subscriber test", func(ctx context.Context) (string, error) {
					return "sub output", nil
				})

				Eventually(func() string {
					t, _ := manager.Get("sub-task")
					return t.Status.Load()
				}, "2s", "50ms").Should(Equal("completed"))

				var notif streaming.CompletionNotificationEvent
				Eventually(ch, "2s", "50ms").Should(Receive(&notif))
				Expect(notif.TaskID).To(Equal("sub-task"))
				Expect(notif.Agent).To(Equal("agent-1"))
			})
		})

		Context("when no subscriber is set", func() {
			It("does not block completion", func() {
				ctx := context.WithValue(context.Background(), session.IDKey{}, "no-sub-sess")
				streamer := &fakeStreamer{}
				sessionMgr := session.NewManager(streamer)
				manager.WithSessionManager(sessionMgr)

				manager.Launch(ctx, "no-sub-task", "agent-1", "no subscriber test", func(ctx context.Context) (string, error) {
					return "ok", nil
				})

				Eventually(func() string {
					t, _ := manager.Get("no-sub-task")
					return t.Status.Load()
				}, "2s", "50ms").Should(Equal("completed"))
			})
		})
	})
})
