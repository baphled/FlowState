package engine_test

import (
	"context"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
)

// fakeSessionSender records SendMessage calls and returns a controllable stream.
type fakeSessionSender struct {
	mu              sync.Mutex
	sendCalls       []string // session IDs of SendMessage calls
	notifications   map[string][]streaming.CompletionNotificationEvent
	ensuredSessions []string
}

func newFakeSessionSender() *fakeSessionSender {
	return &fakeSessionSender{
		notifications: make(map[string][]streaming.CompletionNotificationEvent),
	}
}

func (f *fakeSessionSender) SendMessage(_ context.Context, sessionID string, _ string) (<-chan provider.StreamChunk, error) {
	f.mu.Lock()
	f.sendCalls = append(f.sendCalls, sessionID)
	f.mu.Unlock()

	ch := make(chan provider.StreamChunk, 2)
	ch <- provider.StreamChunk{Content: "re-prompt response"}
	ch <- provider.StreamChunk{Done: true}
	close(ch)
	return ch, nil
}

func (f *fakeSessionSender) GetNotifications(sessionID string) ([]streaming.CompletionNotificationEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	notifs := f.notifications[sessionID]
	delete(f.notifications, sessionID)
	return notifs, nil
}

func (f *fakeSessionSender) EnsureSession(sessionID, agentID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensuredSessions = append(f.ensuredSessions, sessionID)
}

func (f *fakeSessionSender) addNotification(sessionID string, n streaming.CompletionNotificationEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notifications[sessionID] = append(f.notifications[sessionID], n)
}

func (f *fakeSessionSender) getSendCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.sendCalls...)
}

// fakeBroker records Publish calls and drains the chunks channel.
type fakeBroker struct {
	mu       sync.Mutex
	sessions []string
}

func (b *fakeBroker) Publish(sessionID string, chunks <-chan provider.StreamChunk) {
	b.mu.Lock()
	b.sessions = append(b.sessions, sessionID)
	b.mu.Unlock()

	for range chunks { //nolint:revive // intentional drain
	}
}

func (b *fakeBroker) getPublishedSessions() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string{}, b.sessions...)
}

var _ = Describe("CompletionOrchestrator", func() {
	var (
		bgMgr  *engine.BackgroundTaskManager
		sender *fakeSessionSender
		bus    *eventbus.EventBus
		broker *fakeBroker
		orch   *engine.CompletionOrchestrator
	)

	BeforeEach(func() {
		bus = eventbus.NewEventBus()
		bgMgr = engine.NewBackgroundTaskManager()
		bgMgr.SetEventBus(bus)
		sender = newFakeSessionSender()
		broker = &fakeBroker{}
		orch = engine.NewCompletionOrchestrator(bgMgr, sender, bus, broker)
		orch.Start()
	})

	AfterEach(func() {
		orch.Stop()
	})

	Describe("re-prompting when all tasks complete", func() {
		It("triggers a re-prompt when all tasks for a session finish", func() {
			sender.addNotification("sess-1", streaming.CompletionNotificationEvent{
				TaskID: "task-1", Agent: "explorer", Duration: 5 * time.Second, Status: "completed",
			})

			// Launch and immediately complete a task
			ctx := context.WithValue(context.Background(), session.IDKey{}, "sess-1")
			bgMgr.Launch(ctx, "task-1", "explorer", "explore code", func(ctx context.Context) (string, error) {
				return "done", nil
			})

			Eventually(func() string {
				t, _ := bgMgr.Get("task-1")
				return t.Status.Load()
			}, "2s", "50ms").Should(Equal("completed"))

			// The orchestrator should trigger a re-prompt via SendMessage
			Eventually(func() []string {
				return sender.getSendCalls()
			}, "2s", "50ms").Should(HaveLen(1))

			Expect(sender.getSendCalls()[0]).To(Equal("sess-1"))

			// Broker should have received the stream
			Eventually(func() []string {
				return broker.getPublishedSessions()
			}, "2s", "50ms").Should(ContainElement("sess-1"))
		})
	})

	Describe("skipping when tasks are still active", func() {
		It("does not re-prompt when other tasks are still running", func() {
			blockCh := make(chan struct{})

			ctx := context.WithValue(context.Background(), session.IDKey{}, "sess-2")
			// Launch two tasks — one completes, one blocks
			bgMgr.Launch(ctx, "task-2a", "explorer", "quick task", func(ctx context.Context) (string, error) {
				return "done", nil
			})
			bgMgr.Launch(ctx, "task-2b", "librarian", "slow task", func(ctx context.Context) (string, error) {
				<-blockCh
				return "done", nil
			})

			// Wait for the quick task to complete
			Eventually(func() string {
				t, _ := bgMgr.Get("task-2a")
				return t.Status.Load()
			}, "2s", "50ms").Should(Equal("completed"))

			// Give the orchestrator time to process — should NOT trigger
			Consistently(func() []string {
				return sender.getSendCalls()
			}, "500ms", "50ms").Should(BeEmpty())

			close(blockCh)
		})
	})

	Describe("CAS flag prevents duplicate re-prompts", func() {
		It("does not trigger multiple re-prompts for the same session", func() {
			sender.addNotification("sess-3", streaming.CompletionNotificationEvent{
				TaskID: "task-3a", Agent: "explorer", Duration: 5 * time.Second,
			})
			sender.addNotification("sess-3", streaming.CompletionNotificationEvent{
				TaskID: "task-3b", Agent: "librarian", Duration: 3 * time.Second,
			})

			ctx := context.WithValue(context.Background(), session.IDKey{}, "sess-3")
			bgMgr.Launch(ctx, "task-3a", "explorer", "task a", func(ctx context.Context) (string, error) {
				return "done", nil
			})
			bgMgr.Launch(ctx, "task-3b", "librarian", "task b", func(ctx context.Context) (string, error) {
				return "done", nil
			})

			// Wait for both to complete
			Eventually(func() int {
				return bgMgr.ActiveCountForSession("sess-3")
			}, "2s", "50ms").Should(Equal(0))

			// Should only send ONE re-prompt (both notifications batched)
			Eventually(func() []string {
				return sender.getSendCalls()
			}, "2s", "50ms").Should(HaveLen(1))

			// Give extra time to confirm no duplicate
			Consistently(func() int {
				return len(sender.getSendCalls())
			}, "500ms", "50ms").Should(Equal(1))
		})
	})

	Describe("re-prompt depth limit", func() {
		It("stops re-prompting after max depth is reached", func() {
			ctx := context.WithValue(context.Background(), session.IDKey{}, "sess-4")

			// Trigger 4 sequential completions (max is 3)
			for i := range 4 {
				taskID := "task-4-" + string(rune('a'+i))
				sender.addNotification("sess-4", streaming.CompletionNotificationEvent{
					TaskID: taskID, Agent: "explorer", Duration: time.Second,
				})
				bgMgr.Launch(ctx, taskID, "explorer", "task", func(ctx context.Context) (string, error) {
					return "done", nil
				})

				Eventually(func() string {
					t, _ := bgMgr.Get(taskID)
					return t.Status.Load()
				}, "2s", "50ms").Should(Equal("completed"))

				// Give orchestrator time to process
				time.Sleep(100 * time.Millisecond)
			}

			// Should have at most 3 re-prompts
			Eventually(func() int {
				return len(sender.getSendCalls())
			}, "2s", "50ms").Should(BeNumerically("<=", 3))
		})
	})

	Describe("ResetRePromptCount", func() {
		It("allows re-prompting again after reset", func() {
			ctx := context.WithValue(context.Background(), session.IDKey{}, "sess-5")

			// Exhaust the re-prompt budget
			for i := range 3 {
				taskID := "task-5-" + string(rune('a'+i))
				sender.addNotification("sess-5", streaming.CompletionNotificationEvent{
					TaskID: taskID, Agent: "explorer", Duration: time.Second,
				})
				bgMgr.Launch(ctx, taskID, "explorer", "task", func(ctx context.Context) (string, error) {
					return "done", nil
				})

				Eventually(func() string {
					t, _ := bgMgr.Get(taskID)
					return t.Status.Load()
				}, "2s", "50ms").Should(Equal("completed"))

				time.Sleep(100 * time.Millisecond)
			}

			initialCalls := len(sender.getSendCalls())

			// Reset the counter
			orch.ResetRePromptCount("sess-5")

			// Now another completion should trigger a re-prompt
			sender.addNotification("sess-5", streaming.CompletionNotificationEvent{
				TaskID: "task-5-d", Agent: "explorer", Duration: time.Second,
			})
			bgMgr.Launch(ctx, "task-5-d", "explorer", "post-reset task", func(ctx context.Context) (string, error) {
				return "done", nil
			})

			Eventually(func() int {
				return len(sender.getSendCalls())
			}, "2s", "50ms").Should(BeNumerically(">", initialCalls))
		})
	})

	Describe("no-op when broker is nil", func() {
		It("still re-prompts and drains the stream without a broker", func() {
			orchNoBroker := engine.NewCompletionOrchestrator(bgMgr, sender, bus, nil)
			orchNoBroker.Start()
			defer orchNoBroker.Stop()

			sender.addNotification("sess-6", streaming.CompletionNotificationEvent{
				TaskID: "task-6", Agent: "explorer", Duration: time.Second,
			})

			ctx := context.WithValue(context.Background(), session.IDKey{}, "sess-6")
			bgMgr.Launch(ctx, "task-6", "explorer", "no broker task", func(ctx context.Context) (string, error) {
				return "done", nil
			})

			Eventually(func() string {
				t, _ := bgMgr.Get("task-6")
				return t.Status.Load()
			}, "2s", "50ms").Should(Equal("completed"))

			Eventually(func() []string {
				return sender.getSendCalls()
			}, "2s", "50ms").Should(ContainElement("sess-6"))
		})
	})

	Describe("tasks without parent session", func() {
		It("does not attempt re-prompt for orphan tasks", func() {
			// Launch without session ID in context
			bgMgr.Launch(context.Background(), "orphan-task", "explorer", "orphan", func(ctx context.Context) (string, error) {
				return "done", nil
			})

			Eventually(func() string {
				t, _ := bgMgr.Get("orphan-task")
				return t.Status.Load()
			}, "2s", "50ms").Should(Equal("completed"))

			Consistently(func() []string {
				return sender.getSendCalls()
			}, "500ms", "50ms").Should(BeEmpty())
		})
	})
})
