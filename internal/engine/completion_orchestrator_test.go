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
// A per-session hang gate lets tests block SendMessage for a specific session
// to simulate a wedged provider without affecting other sessions.
type fakeSessionSender struct {
	mu              sync.Mutex
	sendCalls       []string // session IDs of SendMessage calls
	notifications   map[string][]streaming.CompletionNotificationEvent
	ensuredSessions []string
	hangBySession   map[string]chan struct{}
	ctxBySession    map[string]context.Context //nolint:containedctx // test-only fixture for inspecting the deadline triggerRePrompt picked
}

func newFakeSessionSender() *fakeSessionSender {
	return &fakeSessionSender{
		notifications: make(map[string][]streaming.CompletionNotificationEvent),
		hangBySession: make(map[string]chan struct{}),
		ctxBySession:  make(map[string]context.Context),
	}
}

// hangSession arranges for the next SendMessage call for sessionID to block
// until the returned release channel is closed (or the call's context fires).
func (f *fakeSessionSender) hangSession(sessionID string) chan struct{} {
	release := make(chan struct{})
	f.mu.Lock()
	f.hangBySession[sessionID] = release
	f.mu.Unlock()
	return release
}

func (f *fakeSessionSender) SendMessage(ctx context.Context, sessionID string, _ string) (<-chan provider.StreamChunk, error) {
	f.mu.Lock()
	f.sendCalls = append(f.sendCalls, sessionID)
	f.ctxBySession[sessionID] = ctx
	hang := f.hangBySession[sessionID]
	f.mu.Unlock()

	if hang != nil {
		select {
		case <-hang:
		case <-ctx.Done():
		}
	}

	ch := make(chan provider.StreamChunk, 2)
	ch <- provider.StreamChunk{Content: "re-prompt response"}
	ch <- provider.StreamChunk{Done: true}
	close(ch)
	return ch, nil
}

func (f *fakeSessionSender) getCtxFor(sessionID string) context.Context {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ctxBySession[sessionID]
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

// blockingFakeBroker blocks Publish until release is closed, simulating a
// long-running re-prompt stream. Used to test pending re-enqueue.
type blockingFakeBroker struct {
	mu       sync.Mutex
	sessions []string
	release  chan struct{}
}

func (b *blockingFakeBroker) Publish(sessionID string, chunks <-chan provider.StreamChunk) {
	b.mu.Lock()
	b.sessions = append(b.sessions, sessionID)
	b.mu.Unlock()

	go func() {
		for range chunks { //nolint:revive // intentional drain
		}
	}()

	<-b.release
}

func (b *blockingFakeBroker) getPublishedSessions() []string {
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
		defer func() { _ = recover() }()
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

	Describe("pending re-enqueue after in-flight re-prompt", func() {
		It("re-processes the session when a completion arrives during a re-prompt", func() {
			// Stop the default orchestrator so only our blocking one is listening.
			orch.Stop()

			// Use a fresh bus so the stopped orchestrator's handlers can't fire.
			localBus := eventbus.NewEventBus()
			bgMgr.SetEventBus(localBus)

			// Use a blocking broker that holds the first re-prompt stream open
			// long enough for a second task to complete and fire its event.
			blockingBroker := &blockingFakeBroker{release: make(chan struct{})}
			blockingOrch := engine.NewCompletionOrchestrator(bgMgr, sender, localBus, blockingBroker)
			blockingOrch.Start()
			defer blockingOrch.Stop()

			// Seed a notification so the first re-prompt has something to send.
			sender.addNotification("sess-pending", streaming.CompletionNotificationEvent{
				TaskID: "task-p1", Agent: "explorer", Duration: time.Second,
			})

			ctx := context.WithValue(context.Background(), session.IDKey{}, "sess-pending")
			bgMgr.Launch(ctx, "task-p1", "explorer", "first", func(ctx context.Context) (string, error) {
				return "done", nil
			})

			Eventually(func() int {
				return len(blockingBroker.getPublishedSessions())
			}, "2s", "50ms").Should(Equal(1))

			// First re-prompt is now blocked inside Publish. Launch a second
			// task; its completion event should be marked pending because
			// rePrompting[sess-pending] is true.
			sender.addNotification("sess-pending", streaming.CompletionNotificationEvent{
				TaskID: "task-p2", Agent: "librarian", Duration: time.Second,
			})
			bgMgr.Launch(ctx, "task-p2", "librarian", "second", func(ctx context.Context) (string, error) {
				return "done", nil
			})

			Eventually(func() string {
				t, _ := bgMgr.Get("task-p2")
				return t.Status.Load()
			}, "2s", "50ms").Should(Equal("completed"))

			// Release the first re-prompt so the defer fires and the pending
			// completion is re-enqueued.
			close(blockingBroker.release)

			// The second re-prompt should eventually happen.
			Eventually(func() int {
				return len(sender.getSendCalls())
			}, "3s", "50ms").Should(BeNumerically(">=", 2))
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

	// H4 — drainLoop must NOT block on triggerRePrompt: a wedged provider for
	// one session must not silently drop completion notifications for every
	// other session. The drain goroutine's only contract is "pick events off
	// completionCh and dispatch the re-prompt onto a worker"; the re-prompt
	// itself runs concurrently with a per-call deadline.
	Describe("H4 — drain loop does not block on a wedged re-prompt", func() {
		It("dispatches re-prompts for session B while session A's provider is wedged", func() {
			// Session A — provider hangs forever (until release fires).
			releaseA := sender.hangSession("sess-A")
			defer close(releaseA) // unblock at test teardown so the goroutine exits cleanly

			sender.addNotification("sess-A", streaming.CompletionNotificationEvent{
				TaskID: "task-A", Agent: "explorer", Duration: time.Second,
			})
			ctxA := context.WithValue(context.Background(), session.IDKey{}, "sess-A")
			bgMgr.Launch(ctxA, "task-A", "explorer", "wedged session", func(ctx context.Context) (string, error) {
				return "done", nil
			})

			// Wait for A's SendMessage to be in flight (sender records the call
			// before blocking). This proves the orchestrator has dispatched A's
			// re-prompt and is now wedged inside SendMessage.
			Eventually(func() []string {
				return sender.getSendCalls()
			}, "2s", "50ms").Should(ContainElement("sess-A"))

			// Now fire a completion for session B. With H4 unfixed, the drain
			// goroutine is wedged inside A's synchronous triggerRePrompt and B's
			// re-prompt never dispatches. With H4 fixed, B should be dispatched
			// concurrently within seconds.
			sender.addNotification("sess-B", streaming.CompletionNotificationEvent{
				TaskID: "task-B", Agent: "librarian", Duration: time.Second,
			})
			ctxB := context.WithValue(context.Background(), session.IDKey{}, "sess-B")
			bgMgr.Launch(ctxB, "task-B", "librarian", "fast session", func(ctx context.Context) (string, error) {
				return "done", nil
			})

			Eventually(func() []string {
				return sender.getSendCalls()
			}, "3s", "50ms").Should(ContainElement("sess-B"),
				"session B's re-prompt must dispatch while session A is wedged inside SendMessage")
		})

		It("applies a per-re-prompt deadline so a wedged provider eventually unblocks", func() {
			// Session A — provider hangs; we never close its release. The fix
			// must impose a deadline on the SendMessage context so the inner
			// hang select returns on ctx.Done(), the re-prompt goroutine exits,
			// and the CAS flag is cleared. We can verify this by observing the
			// CAS flag clear (a follow-up notification triggers another
			// SendMessage call once the first re-prompt's defer has run).
			releaseA := sender.hangSession("sess-deadline")
			_ = releaseA // intentionally never released — deadline must save us

			orchTight := engine.NewCompletionOrchestrator(bgMgr, sender, bus, broker)
			engine.SetRePromptTimeout(orchTight, 200*time.Millisecond)
			orchTight.Start()
			defer orchTight.Stop()

			// Stop the default orch so only orchTight handles events.
			orch.Stop()

			sender.addNotification("sess-deadline", streaming.CompletionNotificationEvent{
				TaskID: "task-d1", Agent: "explorer", Duration: time.Second,
			})
			ctx := context.WithValue(context.Background(), session.IDKey{}, "sess-deadline")
			bgMgr.Launch(ctx, "task-d1", "explorer", "first", func(ctx context.Context) (string, error) {
				return "done", nil
			})

			// First SendMessage should be invoked.
			Eventually(func() []string {
				return sender.getSendCalls()
			}, "2s", "50ms").Should(ContainElement("sess-deadline"))

			// The first SendMessage's context must carry a deadline (proves the
			// fix wires WithTimeout into triggerRePrompt rather than the bare
			// context.Background() we had before).
			Eventually(func() bool {
				c := sender.getCtxFor("sess-deadline")
				if c == nil {
					return false
				}
				_, ok := c.Deadline()
				return ok
			}, "2s", "50ms").Should(BeTrue(),
				"triggerRePrompt's context must have a deadline so a wedged provider is recoverable")

			// After the deadline fires, the CAS flag must clear so subsequent
			// completions trigger a new re-prompt. Drop a fresh notification
			// + task and confirm a second SendMessage call is observed.
			sender.addNotification("sess-deadline", streaming.CompletionNotificationEvent{
				TaskID: "task-d2", Agent: "explorer", Duration: time.Second,
			})
			bgMgr.Launch(ctx, "task-d2", "explorer", "second", func(ctx context.Context) (string, error) {
				return "done", nil
			})

			Eventually(func() int {
				calls := sender.getSendCalls()
				count := 0
				for _, s := range calls {
					if s == "sess-deadline" {
						count++
					}
				}
				return count
			}, "3s", "50ms").Should(BeNumerically(">=", 2),
				"after the per-re-prompt deadline trips, the CAS flag must clear so new completions can re-prompt")
		})
	})
})
