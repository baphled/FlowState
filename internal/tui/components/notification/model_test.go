package notification_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/components/notification"
)

var _ = Describe("InMemoryManager", func() {
	It("returns no active notifications after the duration expires", func() {
		manager := notification.NewInMemoryManager()
		now := time.Now()

		manager.Add(notification.Notification{
			ID:        "notification-1",
			Title:     "Saved",
			Message:   "Your changes were saved.",
			Level:     notification.LevelSuccess,
			Duration:  time.Second,
			CreatedAt: now.Add(-2 * time.Second),
		})

		Expect(manager.Active()).To(BeEmpty())
		Expect(manager.Expired()).To(HaveLen(1))
	})
})
