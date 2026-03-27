package toast_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/components/toast"
)

var _ = Describe("InMemoryManager", func() {
	It("returns no active toasts after the duration expires", func() {
		manager := toast.NewInMemoryManager()
		now := time.Now()

		manager.Add(toast.Toast{
			ID:        "toast-1",
			Title:     "Saved",
			Message:   "Your changes were saved.",
			Level:     toast.LevelSuccess,
			Duration:  time.Second,
			CreatedAt: now.Add(-2 * time.Second),
		})

		Expect(manager.Active()).To(BeEmpty())
		Expect(manager.Expired()).To(HaveLen(1))
	})
})
