package chat_test

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tui/components/notification"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

var _ = Describe("notification wiring", func() {
	var intent *chat.Intent

	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
		intent = chat.NewIntent(chat.IntentConfig{
			AgentID:      "test-agent",
			SessionID:    "test-session",
			ProviderName: "openai",
			ModelName:    "gpt-4o",
			TokenBudget:  4096,
		})
	})

	AfterEach(func() {
		chat.SetRunningInTestsForTest(false)
	})

	Describe("NewIntent initialisation", func() {
		It("creates a notification manager", func() {
			Expect(intent.NotificationManagerForTest()).NotTo(BeNil())
		})

		It("starts with no active notifications", func() {
			Expect(intent.NotificationsViewForTest()).To(BeEmpty())
		})
	})

	Describe("Init", func() {
		It("skips notification tick initialisation in tests", func() {
			cmd := intent.Init()
			Expect(cmd).To(BeNil())
		})
	})

	Describe("notification.TickMsg handling", func() {
		It("returns a tick command from Update", func() {
			cmd := intent.Update(notification.TickMsg{})
			Expect(cmd).NotTo(BeNil())
		})
	})

	Describe("DelegationInfo triggers notification", func() {
		It("adds a notification when delegation starts", func() {
			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{
				DelegationInfo: &provider.DelegationInfo{
					ChainID:     "chain-1",
					TargetAgent: "qa-agent",
					Status:      "started",
				},
			})

			active := intent.NotificationManagerForTest().Active()
			Expect(active).To(HaveLen(1))
			Expect(active[0].Title).To(Equal("Delegation"))
			Expect(active[0].Message).To(Equal("qa-agent started"))
			Expect(active[0].Level).To(Equal(notification.LevelInfo))
		})

		It("adds a success notification when delegation completes", func() {
			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{
				DelegationInfo: &provider.DelegationInfo{
					ChainID:     "chain-2",
					TargetAgent: "senior-engineer",
					Status:      "completed",
				},
			})

			active := intent.NotificationManagerForTest().Active()
			Expect(active).To(HaveLen(1))
			Expect(active[0].Level).To(Equal(notification.LevelSuccess))
			Expect(active[0].Message).To(Equal("senior-engineer completed"))
		})

		It("adds an error notification when delegation fails", func() {
			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{
				DelegationInfo: &provider.DelegationInfo{
					ChainID:     "chain-3",
					TargetAgent: "writer",
					Status:      "failed",
				},
			})

			active := intent.NotificationManagerForTest().Active()
			Expect(active).To(HaveLen(1))
			Expect(active[0].Level).To(Equal(notification.LevelError))
			Expect(active[0].Message).To(Equal("writer failed"))
		})

		It("preserves existing view delegation handling", func() {
			intent.HandleStreamChunkForTest(chat.StreamChunkMsg{
				DelegationInfo: &provider.DelegationInfo{
					ChainID:     "chain-4",
					TargetAgent: "devops",
					Status:      "completed",
				},
			})

			messages := intent.AllViewMessagesForTest()
			found := false
			for _, msg := range messages {
				if msg.Role == "system" && strings.Contains(msg.Content, "devops") {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue(), "view.HandleDelegation should still produce a system message")
		})
	})

	Describe("streaming event type handling", func() {
		Context("plan_artifact event", func() {
			It("adds a notification and system message", func() {
				intent.Update(chat.StreamChunkMsg{
					EventType: streaming.EventTypePlanArtifact,
					Content:   "Plan generated successfully",
				})

				active := intent.NotificationManagerForTest().Active()
				Expect(active).To(HaveLen(1))
				Expect(active[0].Title).To(Equal("Plan Artifact"))
				Expect(active[0].Message).To(Equal("Plan generated successfully"))
				Expect(active[0].Level).To(Equal(notification.LevelInfo))

				messages := intent.AllViewMessagesForTest()
				found := false
				for _, msg := range messages {
					if msg.Role == "system" && strings.Contains(msg.Content, "Plan Artifact") {
						found = true
						break
					}
				}
				Expect(found).To(BeTrue())
			})
		})

		Context("review_verdict event", func() {
			It("adds a warning notification and system message", func() {
				intent.Update(chat.StreamChunkMsg{
					EventType: streaming.EventTypeReviewVerdict,
					Content:   "PASS with minor issues",
				})

				active := intent.NotificationManagerForTest().Active()
				Expect(active).To(HaveLen(1))
				Expect(active[0].Title).To(Equal("Review Verdict"))
				Expect(active[0].Level).To(Equal(notification.LevelWarning))

				messages := intent.AllViewMessagesForTest()
				found := false
				for _, msg := range messages {
					if msg.Role == "system" && strings.Contains(msg.Content, "Review Verdict") {
						found = true
						break
					}
				}
				Expect(found).To(BeTrue())
			})
		})

		Context("status_transition event", func() {
			It("adds a notification and system message", func() {
				intent.Update(chat.StreamChunkMsg{
					EventType: streaming.EventTypeStatusTransition,
					Content:   "planning → executing",
				})

				active := intent.NotificationManagerForTest().Active()
				Expect(active).To(HaveLen(1))
				Expect(active[0].Title).To(Equal("Status Transition"))
				Expect(active[0].Message).To(Equal("planning → executing"))

				messages := intent.AllViewMessagesForTest()
				found := false
				for _, msg := range messages {
					if msg.Role == "system" && strings.Contains(msg.Content, "Status Transition") {
						found = true
						break
					}
				}
				Expect(found).To(BeTrue())
			})
		})

		It("continues reading stream when Next is present", func() {
			cmd := intent.Update(chat.StreamChunkMsg{
				EventType: streaming.EventTypePlanArtifact,
				Content:   "plan content",
				Next: func() tea.Msg {
					return chat.StreamChunkMsg{Done: true}
				},
			})

			Expect(cmd).NotTo(BeNil())
		})
	})

	Describe("streamingEventMeta", func() {
		It("returns Plan Artifact with info level for plan_artifact", func() {
			title, level := chat.StreamingEventMetaForTest(streaming.EventTypePlanArtifact)
			Expect(title).To(Equal("Plan Artifact"))
			Expect(level).To(Equal(notification.LevelInfo))
		})

		It("returns Review Verdict with warning level for review_verdict", func() {
			title, level := chat.StreamingEventMetaForTest(streaming.EventTypeReviewVerdict)
			Expect(title).To(Equal("Review Verdict"))
			Expect(level).To(Equal(notification.LevelWarning))
		})

		It("returns Status Transition with info level for status_transition", func() {
			title, level := chat.StreamingEventMetaForTest(streaming.EventTypeStatusTransition)
			Expect(title).To(Equal("Status Transition"))
			Expect(level).To(Equal(notification.LevelInfo))
		})

		It("returns Event with info level for unknown event types", func() {
			title, level := chat.StreamingEventMetaForTest("unknown_event")
			Expect(title).To(Equal("Event"))
			Expect(level).To(Equal(notification.LevelInfo))
		})
	})

	Describe("notification overlay in View", func() {
		It("includes notification content when notifications are active", func() {
			intent.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

			intent.NotificationManagerForTest().Add(notification.Notification{
				ID:        "test-notif",
				Title:     "Test",
				Message:   "test notification",
				Level:     notification.LevelInfo,
				Duration:  5 * time.Second,
				CreatedAt: time.Now(),
			})

			view := intent.View()
			Expect(view).To(ContainSubstring("Test"))
			Expect(view).To(ContainSubstring("test notification"))
		})
	})
})
