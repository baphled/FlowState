package notification_test

import (
	"time"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tui/components/notification"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Component", func() {
	var (
		mgr  *notification.InMemoryManager
		comp *notification.Component
	)

	BeforeEach(func() {
		mgr = notification.NewInMemoryManager()
		comp = notification.NewComponent(mgr)
	})

	It("renders empty string when no notifications", func() {
		Expect(comp.View()).To(BeEmpty())
	})

	It("renders message for a single notification", func() {
		mgr.Add(notification.Notification{
			ID:        "1",
			Title:     "Test",
			Message:   "hello",
			Level:     notification.LevelInfo,
			Duration:  5 * time.Second,
			CreatedAt: time.Now(),
		})
		Expect(comp.View()).To(ContainSubstring("hello"))
	})

	It("renders all active notifications", func() {
		mgr.Add(notification.Notification{
			ID:        "1",
			Title:     "T1",
			Message:   "first",
			Level:     notification.LevelInfo,
			Duration:  5 * time.Second,
			CreatedAt: time.Now(),
		})
		mgr.Add(notification.Notification{
			ID:        "2",
			Title:     "T2",
			Message:   "second",
			Level:     notification.LevelSuccess,
			Duration:  5 * time.Second,
			CreatedAt: time.Now(),
		})
		view := comp.View()
		Expect(view).To(ContainSubstring("first"))
		Expect(view).To(ContainSubstring("second"))
	})

	Describe("AddCategoryModelSwapNotification", func() {
		It("renders category, original → chosen, and reason as a warning toast", func() {
			comp.AddCategoryModelSwapNotification(
				"quick", "claude-haiku-4.5", "claude-sonnet-4-6",
				`matches tool-incapable pattern "claude-haiku*"`,
			)

			active := mgr.Active()
			Expect(active).To(HaveLen(1))
			Expect(active[0].Title).To(Equal("Model auto-promoted"))
			Expect(active[0].Message).To(ContainSubstring("quick:"))
			Expect(active[0].Message).To(ContainSubstring("claude-haiku-4.5 → claude-sonnet-4-6"))
			Expect(active[0].Message).To(ContainSubstring(`tool-incapable pattern "claude-haiku*"`))
			Expect(active[0].Level).To(Equal(notification.LevelWarning))
		})

		It("renders an empty category as the (uncategorised) placeholder", func() {
			comp.AddCategoryModelSwapNotification("", "a", "b", "")

			active := mgr.Active()
			Expect(active).To(HaveLen(1))
			Expect(active[0].Message).To(ContainSubstring("(uncategorised):"))
		})

		It("omits the parenthetical reason when none was supplied", func() {
			comp.AddCategoryModelSwapNotification("quick", "a", "b", "")

			active := mgr.Active()
			Expect(active).To(HaveLen(1))
			Expect(active[0].Message).To(Equal("quick: a → b"))
		})
	})

	Describe("AddDelegationNotification", func() {
		It("adds info notification for started status", func() {
			comp.AddDelegationNotification(&provider.DelegationInfo{
				TargetAgent: "qa-agent",
				Status:      "started",
				ChainID:     "chain-1",
			})
			Expect(mgr.Active()).To(HaveLen(1))
			Expect(mgr.Active()[0].Level).To(Equal(notification.LevelInfo))
		})

		It("adds success notification for completed status", func() {
			comp.AddDelegationNotification(&provider.DelegationInfo{
				TargetAgent: "qa-agent",
				Status:      "completed",
				ChainID:     "chain-1",
			})
			Expect(mgr.Active()).To(HaveLen(1))
			Expect(mgr.Active()[0].Level).To(Equal(notification.LevelSuccess))
		})

		It("adds error notification for failed status", func() {
			comp.AddDelegationNotification(&provider.DelegationInfo{
				TargetAgent: "qa-agent",
				Status:      "failed",
				ChainID:     "chain-1",
			})
			Expect(mgr.Active()).To(HaveLen(1))
			Expect(mgr.Active()[0].Level).To(Equal(notification.LevelError))
		})
	})
})
