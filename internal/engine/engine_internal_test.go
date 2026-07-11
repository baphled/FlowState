package engine

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool/todo"
)

var _ = Describe("buildTodoContinuationMessage", func() {
	Context("when all items are pending", func() {
		It("returns the pending-only continuation message with explicit tool call demand", func() {
			items := []todo.Item{
				{Content: "Step one", Status: "pending", Priority: "high"},
				{Content: "Step two", Status: "pending", Priority: "medium"},
			}

			msg := buildTodoContinuationMessage(items)

			Expect(msg.Role).To(Equal("user"))
			Expect(msg.Content).To(ContainSubstring("You have incomplete tasks that still need to be completed"))
			Expect(msg.Content).To(ContainSubstring("- [pending] Step one (high priority)"))
			Expect(msg.Content).To(ContainSubstring("Resume working on these tasks now by calling tools"))
			Expect(msg.Content).To(ContainSubstring("Do NOT respond with prose"))
		})
	})

	Context("when items are in_progress and pending", func() {
		It("returns the active-task continuation message with queued tasks and tool call demand", func() {
			items := []todo.Item{
				{Content: "Active task", Status: "in_progress", Priority: "high"},
				{Content: "Pending task", Status: "pending", Priority: "medium"},
			}

			msg := buildTodoContinuationMessage(items)

			Expect(msg.Role).To(Equal("user"))
			Expect(msg.Content).To(ContainSubstring("You have an active task"))
			Expect(msg.Content).To(ContainSubstring("▶ \"Active task\""))
			Expect(msg.Content).To(ContainSubstring("○ \"Pending task\""))
			Expect(msg.Content).To(ContainSubstring("You must complete or cancel the active task now by calling the appropriate tools"))
			Expect(msg.Content).To(ContainSubstring("Do NOT respond with prose describing what you will do"))
			Expect(msg.Content).NotTo(ContainSubstring("You have incomplete tasks that still need to be completed"))
		})
	})

	Context("when the only item is in_progress", func() {
		It("returns the active-task continuation message without queued tasks but with tool call demand", func() {
			items := []todo.Item{
				{Content: "Active solo task", Status: "in_progress", Priority: "high"},
			}

			msg := buildTodoContinuationMessage(items)

			Expect(msg.Role).To(Equal("user"))
			Expect(msg.Content).To(ContainSubstring("You have an active task"))
			Expect(msg.Content).To(ContainSubstring("▶ \"Active solo task\""))
			Expect(msg.Content).NotTo(ContainSubstring("Additional queued tasks"))
			Expect(msg.Content).To(ContainSubstring("You must complete or cancel the active task now by calling the appropriate tools"))
			Expect(msg.Content).To(ContainSubstring("Do NOT respond with prose"))
		})
	})
})
