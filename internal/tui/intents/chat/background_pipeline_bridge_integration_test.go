package chat_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

var _ = Describe("Background Pipeline Bridge Integration", Label("integration"), func() {
	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
		DeferCleanup(func() { chat.SetRunningInTestsForTest(false) })
	})

	Describe("waitForCompletion channel bridge", func() {
		It("converts a CompletionNotificationEvent into BackgroundTaskCompletedMsg", func() {
			ch := make(chan streaming.CompletionNotificationEvent, 1)
			ch <- streaming.CompletionNotificationEvent{
				TaskID:      "task-bridge-1",
				Agent:       "explore",
				Description: "inspect codebase",
				Duration:    3 * time.Second,
				Status:      "completed",
			}

			intent := chat.NewIntent(chat.IntentConfig{
				AgentID:      "test-agent",
				SessionID:    "test-session",
				ProviderName: "test-provider",
				ModelName:    "test-model",
				TokenBudget:  4096,
			})
			intent.SetCompletionChannel(ch)

			msg := intent.WaitForCompletionForTest()

			completed, ok := msg.(chat.BackgroundTaskCompletedMsg)
			Expect(ok).To(BeTrue())
			Expect(completed.TaskID).To(Equal("task-bridge-1"))
			Expect(completed.Agent).To(Equal("explore"))
			Expect(completed.Status).To(Equal("completed"))
			Expect(completed.Duration).To(Equal("3s"))
		})

		It("returns nil without blocking when the channel is closed", func() {
			ch := make(chan streaming.CompletionNotificationEvent)
			close(ch)

			intent := chat.NewIntent(chat.IntentConfig{
				AgentID:      "test-agent",
				SessionID:    "test-session",
				ProviderName: "test-provider",
				ModelName:    "test-model",
				TokenBudget:  4096,
			})
			intent.SetCompletionChannel(ch)

			msg := intent.WaitForCompletionForTest()

			Expect(msg).To(BeNil())
		})

		It("bridges a failed task notification correctly", func() {
			ch := make(chan streaming.CompletionNotificationEvent, 1)
			ch <- streaming.CompletionNotificationEvent{
				TaskID:      "task-fail-99",
				Agent:       "builder",
				Description: "build the thing",
				Duration:    500 * time.Millisecond,
				Status:      "failed",
			}

			intent := chat.NewIntent(chat.IntentConfig{
				AgentID:      "test-agent",
				SessionID:    "test-session",
				ProviderName: "test-provider",
				ModelName:    "test-model",
				TokenBudget:  4096,
			})
			intent.SetCompletionChannel(ch)

			msg := intent.WaitForCompletionForTest()

			completed, ok := msg.(chat.BackgroundTaskCompletedMsg)
			Expect(ok).To(BeTrue())
			Expect(completed.Status).To(Equal("failed"))
			Expect(completed.TaskID).To(Equal("task-fail-99"))
		})
	})

	Describe("handleBackgroundTaskCompleted re-queues waitForCompletion", func() {
		It("re-queues when completionChan is set and tasks are still pending", func() {
			ch := make(chan streaming.CompletionNotificationEvent, 64)
			bgMgr := engine.NewBackgroundTaskManager()
			bgMgr.Launch(context.Background(), "long-running", "some-agent", "running", func(ctx context.Context) (string, error) {
				<-ctx.Done()
				return "", ctx.Err()
			})
			DeferCleanup(func() { bgMgr.CancelAll() })

			intent := chat.NewIntent(chat.IntentConfig{
				AgentID:      "test-agent",
				SessionID:    "test-session",
				ProviderName: "test-provider",
				ModelName:    "test-model",
				TokenBudget:  4096,
			})
			intent.SetCompletionChannel(ch)
			intent.SetBackgroundManagerForTest(bgMgr)

			msg := chat.BackgroundTaskCompletedMsg{
				TaskID:   "first-done",
				Agent:    "explore",
				Duration: "1s",
				Status:   "completed",
			}

			cmd := intent.HandleBackgroundTaskCompletedForTest(msg)

			Expect(cmd).NotTo(BeNil())
			Expect(intent.CompletionChanForTest()).NotTo(BeNil())
		})
	})

	Describe("formatCompletionReminder", func() {
		It("produces a system-reminder string with expected structure", func() {
			msg := chat.BackgroundTaskCompletedMsg{
				TaskID:      "reminder-task-001",
				Agent:       "qa-engineer",
				Description: "run test suite",
				Duration:    "45s",
				Status:      "completed",
			}

			result := chat.FormatCompletionReminderForTest(msg)

			Expect(result).To(ContainSubstring("<system-reminder>"))
			Expect(result).To(ContainSubstring("</system-reminder>"))
			Expect(result).To(ContainSubstring("BACKGROUND TASK COMPLETE"))
			Expect(result).To(ContainSubstring("reminder-task-001"))
			Expect(result).To(ContainSubstring("qa-engineer"))
			Expect(result).To(ContainSubstring("45s"))
			Expect(result).To(ContainSubstring("background_output"))
		})

		It("includes the task ID in the background_output call reference", func() {
			msg := chat.BackgroundTaskCompletedMsg{
				TaskID:   "unique-task-xyz",
				Agent:    "researcher",
				Duration: "10s",
				Status:   "completed",
			}

			result := chat.FormatCompletionReminderForTest(msg)

			Expect(result).To(ContainSubstring(`"unique-task-xyz"`))
		})
	})

	Describe("live BackgroundTaskManager subscriber channel to intent processing", func() {
		It("delivers a successful task completion through the full bridge pipeline", func() {
			ch := make(chan streaming.CompletionNotificationEvent, 64)
			bgMgr := engine.NewBackgroundTaskManager()
			bgMgr.SetCompletionSubscriber(ch)

			intent := chat.NewIntent(chat.IntentConfig{
				AgentID:      "test-agent",
				SessionID:    "test-session",
				ProviderName: "test-provider",
				ModelName:    "test-model",
				TokenBudget:  4096,
			})
			intent.SetCompletionChannel(ch)
			intent.SetBackgroundManagerForTest(bgMgr)

			ctx := context.WithValue(context.Background(), session.IDKey{}, "test-session")
			bgMgr.Launch(ctx, "live-success-task", "explore", "live test", func(_ context.Context) (string, error) {
				return "success result", nil
			})

			Eventually(func() int {
				return len(ch)
			}, 8*time.Second, 50*time.Millisecond).Should(BeNumerically(">=", 1))

			msg := intent.WaitForCompletionForTest()

			completed, ok := msg.(chat.BackgroundTaskCompletedMsg)
			Expect(ok).To(BeTrue())
			Expect(completed.Status).To(Equal("completed"))
			Expect(completed.TaskID).To(Equal("live-success-task"))
		})

		It("delivers a failed task completion through the full bridge pipeline", func() {
			ch := make(chan streaming.CompletionNotificationEvent, 64)
			bgMgr := engine.NewBackgroundTaskManager()
			bgMgr.SetCompletionSubscriber(ch)

			intent := chat.NewIntent(chat.IntentConfig{
				AgentID:      "test-agent",
				SessionID:    "test-session",
				ProviderName: "test-provider",
				ModelName:    "test-model",
				TokenBudget:  4096,
			})
			intent.SetCompletionChannel(ch)
			intent.SetBackgroundManagerForTest(bgMgr)

			ctx := context.WithValue(context.Background(), session.IDKey{}, "test-session")
			bgMgr.Launch(ctx, "live-fail-task", "builder", "will fail", func(_ context.Context) (string, error) {
				return "", context.DeadlineExceeded
			})

			Eventually(func() int {
				return len(ch)
			}, 8*time.Second, 50*time.Millisecond).Should(BeNumerically(">=", 1))

			msg := intent.WaitForCompletionForTest()

			completed, ok := msg.(chat.BackgroundTaskCompletedMsg)
			Expect(ok).To(BeTrue())
			Expect(completed.TaskID).To(Equal("live-fail-task"))
		})
	})
})
