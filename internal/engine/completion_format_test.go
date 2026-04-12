package engine_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/streaming"
)

var _ = Describe("FormatCompletionReminder", func() {
	It("includes task ID, agent, and duration", func() {
		result := engine.FormatCompletionReminder("abc-123", "qa-engineer", "10s")
		Expect(result).To(ContainSubstring("abc-123"))
		Expect(result).To(ContainSubstring("qa-engineer"))
		Expect(result).To(ContainSubstring("10s"))
		Expect(result).To(ContainSubstring("background_output"))
		Expect(result).To(ContainSubstring("<system-reminder>"))
		Expect(result).To(ContainSubstring("</system-reminder>"))
	})

	It("includes the task_id parameter for background_output", func() {
		result := engine.FormatCompletionReminder("task-xyz", "explorer", "5s")
		Expect(result).To(ContainSubstring(`task_id="task-xyz"`))
	})
})

var _ = Describe("FormatCompletionReminders", func() {
	It("returns empty string for empty notifications", func() {
		result := engine.FormatCompletionReminders(nil)
		Expect(result).To(BeEmpty())
	})

	It("formats a single notification", func() {
		notifications := []streaming.CompletionNotificationEvent{
			{TaskID: "task-1", Agent: "explorer", Duration: 10 * time.Second, Status: "completed"},
		}
		result := engine.FormatCompletionReminders(notifications)
		Expect(result).To(ContainSubstring("task-1"))
		Expect(result).To(ContainSubstring("explorer"))
	})

	It("formats multiple notifications joined by newlines", func() {
		notifications := []streaming.CompletionNotificationEvent{
			{TaskID: "task-1", Agent: "explorer", Duration: 10 * time.Second},
			{TaskID: "task-2", Agent: "librarian", Duration: 20 * time.Second},
		}
		result := engine.FormatCompletionReminders(notifications)
		Expect(result).To(ContainSubstring("task-1"))
		Expect(result).To(ContainSubstring("task-2"))
		Expect(result).To(ContainSubstring("explorer"))
		Expect(result).To(ContainSubstring("librarian"))
	})
})
