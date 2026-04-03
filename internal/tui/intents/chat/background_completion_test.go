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

var _ = Describe("background task completion", func() {
	var intent *chat.Intent

	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
		DeferCleanup(func() { chat.SetRunningInTestsForTest(false) })

		intent = chat.NewIntent(chat.IntentConfig{
			AgentID:      "test-agent",
			SessionID:    "test-session",
			ProviderName: "test-provider",
			ModelName:    "test-model",
			TokenBudget:  4096,
		})
	})

	Describe("handleBackgroundTaskCompleted", func() {
		It("adds a system message for each completed task", func() {
			msg := chat.BackgroundTaskCompletedMsg{
				TaskID:      "task-1",
				Agent:       "explore",
				Description: "investigate codebase",
				Duration:    "5s",
				Status:      "completed",
			}

			intent.HandleBackgroundTaskCompletedForTest(msg)

			messages := intent.AllViewMessagesForTest()
			Expect(messages).NotTo(BeEmpty())
			lastMsg := messages[len(messages)-1]
			Expect(lastMsg.Role).To(Equal("system"))
			Expect(lastMsg.Content).To(ContainSubstring("BACKGROUND TASK COMPLETE"))
			Expect(lastMsg.Content).To(ContainSubstring("task-1"))
			Expect(lastMsg.Content).To(ContainSubstring("explore"))
		})

		Context("when background tasks are still running", func() {
			It("does not re-trigger LLM stream", func() {
				bgMgr := engine.NewBackgroundTaskManager()
				bgMgr.Launch(context.Background(), "active-task", "some-agent", "still running", func(ctx context.Context) (string, error) {
					<-ctx.Done()
					return "", ctx.Err()
				})
				DeferCleanup(func() { bgMgr.CancelAll() })

				intent.SetBackgroundManagerForTest(bgMgr)

				msg := chat.BackgroundTaskCompletedMsg{
					TaskID:      "task-done",
					Agent:       "explore",
					Description: "finished investigation",
					Duration:    "3s",
					Status:      "completed",
				}

				cmd := intent.HandleBackgroundTaskCompletedForTest(msg)

				Expect(bgMgr.ActiveCount()).To(BeNumerically(">", 0))

				messages := intent.AllViewMessagesForTest()
				Expect(messages).NotTo(BeEmpty())
				lastMsg := messages[len(messages)-1]
				Expect(lastMsg.Content).To(ContainSubstring("BACKGROUND TASK COMPLETE"))

				Expect(intent.IsStreaming()).To(BeFalse())

				if cmd != nil {
					_ = cmd
				}
			})
		})

		Context("when the last background task finishes", func() {
			It("re-triggers LLM stream when no tasks remain", func() {
				bgMgr := engine.NewBackgroundTaskManager()
				intent.SetBackgroundManagerForTest(bgMgr)

				Expect(bgMgr.ActiveCount()).To(Equal(0))

				msg := chat.BackgroundTaskCompletedMsg{
					TaskID:      "task-last",
					Agent:       "explore",
					Description: "final investigation",
					Duration:    "2s",
					Status:      "completed",
				}

				cmd := intent.HandleBackgroundTaskCompletedForTest(msg)

				Expect(cmd).NotTo(BeNil())

				Expect(intent.IsStreaming()).To(BeTrue())
			})
		})

		Context("when no background manager is set", func() {
			It("re-triggers LLM stream (assumes all done)", func() {
				Expect(intent.BackgroundManagerForTest()).To(BeNil())

				msg := chat.BackgroundTaskCompletedMsg{
					TaskID:      "task-orphan",
					Agent:       "explore",
					Description: "orphan task",
					Duration:    "1s",
					Status:      "completed",
				}

				cmd := intent.HandleBackgroundTaskCompletedForTest(msg)

				Expect(cmd).NotTo(BeNil())
				Expect(intent.IsStreaming()).To(BeTrue())
			})
		})
	})

	Describe("SetBackgroundManager", func() {
		It("stores the manager reference", func() {
			bgMgr := engine.NewBackgroundTaskManager()
			intent.SetBackgroundManager(bgMgr)
			Expect(intent.BackgroundManagerForTest()).To(Equal(bgMgr))
		})
	})

	Describe("SetCompletionChannel", func() {
		It("stores the channel reference", func() {
			ch := make(chan streaming.CompletionNotificationEvent, 64)
			intent.SetCompletionChannel(ch)
			Expect(intent.CompletionChanForTest()).NotTo(BeNil())
		})
	})

	Describe("formatCompletionReminder", func() {
		It("includes task ID, agent, and duration", func() {
			msg := chat.BackgroundTaskCompletedMsg{
				TaskID:      "abc-123",
				Agent:       "qa-engineer",
				Description: "run tests",
				Duration:    "10s",
				Status:      "completed",
			}

			result := chat.FormatCompletionReminderForTest(msg)
			Expect(result).To(ContainSubstring("abc-123"))
			Expect(result).To(ContainSubstring("qa-engineer"))
			Expect(result).To(ContainSubstring("10s"))
			Expect(result).To(ContainSubstring("background_output"))
		})
	})

	Describe("notifyCompletionSubscriber blocking send", func() {
		It("delivers completion to subscriber without dropping", func() {
			bgMgr := engine.NewBackgroundTaskManager()
			ch := make(chan streaming.CompletionNotificationEvent, 64)
			bgMgr.SetCompletionSubscriber(ch)

			ctx := context.WithValue(context.Background(), session.IDKey{}, "test-session")
			done := make(chan struct{})
			go func() {
				defer close(done)
				bgMgr.Launch(ctx, "notify-test", "agent-1", "test task", func(_ context.Context) (string, error) {
					return "result", nil
				})
			}()

			Eventually(func() int {
				return len(ch)
			}, 5*time.Second, 50*time.Millisecond).Should(BeNumerically(">=", 1))

			notif := <-ch
			Expect(notif.TaskID).To(Equal("notify-test"))
			Expect(notif.Status).To(Equal("completed"))
		})
	})
})
