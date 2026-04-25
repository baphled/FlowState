package chat_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tui/views/chat"
)

var _ = Describe("ChatView CollapsibleDelegationBlock integration", Label("integration"), func() {
	var view *chat.View

	BeforeEach(func() {
		view = chat.NewView()
		view.SetMarkdownRenderer(func(c string, _ int) string { return c })
	})

	Describe("rendering CollapsibleDelegationBlock for active delegation", func() {
		Context("when delegation is running", func() {
			It("renders the target agent name in content area", func() {
				view.HandleDelegation(&provider.DelegationInfo{
					SourceAgent:  "coordinator",
					TargetAgent:  "qa-agent",
					Status:       "running",
					ModelName:    "claude-opus-4-5",
					ProviderName: "anthropic",
				})
				content := view.RenderContent(80)

				Expect(content).To(ContainSubstring("qa-agent"))
			})

			It("renders the running status indicator", func() {
				view.HandleDelegation(&provider.DelegationInfo{
					SourceAgent: "coordinator",
					TargetAgent: "worker",
					Status:      "running",
					ModelName:   "gpt-4o",
				})
				content := view.RenderContent(80)

				Expect(content).To(ContainSubstring("running"))
			})
		})

		Context("when delegation completes", func() {
			It("commits a system message with CollapsibleDelegationBlock content", func() {
				view.HandleDelegation(&provider.DelegationInfo{
					SourceAgent: "coordinator",
					TargetAgent: "planner",
					Status:      "completed",
					ModelName:   "claude-opus-4-5",
					Description: "Plan created",
				})

				msgs := view.Messages()
				Expect(msgs).NotTo(BeEmpty())
				Expect(msgs[len(msgs)-1].Role).To(Equal("system"))
			})

			It("completed delegation block includes target agent in message content", func() {
				view.HandleDelegation(&provider.DelegationInfo{
					SourceAgent: "coordinator",
					TargetAgent: "qa-agent",
					Status:      "completed",
					ModelName:   "fast",
					Description: "Tests passed",
				})

				msgs := view.Messages()
				lastMsg := msgs[len(msgs)-1]
				Expect(lastMsg.Content).To(ContainSubstring("qa-agent"))
			})
		})

		Context("when delegation fails", func() {
			It("commits a system message when delegation fails", func() {
				view.HandleDelegation(&provider.DelegationInfo{
					SourceAgent: "coordinator",
					TargetAgent: "planner",
					Status:      "failed",
					ModelName:   "claude-opus-4-5",
					Description: "Timed out",
				})

				msgs := view.Messages()
				Expect(msgs).NotTo(BeEmpty())
				Expect(msgs[len(msgs)-1].Role).To(Equal("system"))
			})

			It("failed delegation message content includes the failure indicator", func() {
				view.HandleDelegation(&provider.DelegationInfo{
					SourceAgent: "coordinator",
					TargetAgent: "executor",
					Status:      "failed",
					ModelName:   "llama3.2",
					Description: "Context exceeded",
				})

				msgs := view.Messages()
				lastMsg := msgs[len(msgs)-1]
				Expect(lastMsg.Content).To(ContainSubstring("✗"))
			})
		})
	})

	Describe("view integrates message footer with delegation block", func() {
		Context("when an assistant partial precedes a delegation mid-turn", func() {
			It("does NOT render the model footer above the delegation block (mid-turn footer suppressed)", func() {
				// Mid-turn footer suppression: FlushPartialResponse
				// commits the partial without ModelID so the footer
				// only appears on the FINAL assistant message of a
				// turn (via finaliseChunk on Done). This keeps inline
				// tool / delegation widgets from rendering below a
				// stale footer — the "Reading… below the footer"
				// symptom the user reported.
				view.SetModelID("claude-sonnet-4-20250514")
				view.SetStreaming(true, "thinking about delegation plan")
				view.FlushPartialResponse()

				view.HandleDelegation(&provider.DelegationInfo{
					SourceAgent: "coordinator",
					TargetAgent: "qa-agent",
					Status:      "running",
					ModelName:   "fast",
				})

				content := view.RenderContent(80)

				Expect(content).NotTo(ContainSubstring("claude-sonnet-4-20250514"),
					"mid-turn footer must be suppressed; ModelID renders only on the final message of a turn")
				Expect(content).To(ContainSubstring("qa-agent"),
					"the delegation block still renders without a footer interrupting it")
			})
		})

		Context("when streaming with a partial response and active delegation", func() {
			It("renders both streaming response text and delegation block", func() {
				view.HandleDelegation(&provider.DelegationInfo{
					SourceAgent: "coordinator",
					TargetAgent: "planner",
					Status:      "running",
					ModelName:   "claude-opus-4-5",
				})
				view.SetStreaming(true, "coordinating...")

				content := view.RenderContent(80)

				Expect(content).To(ContainSubstring("planner"))
				Expect(content).To(ContainSubstring("coordinating..."))
			})
		})
	})

	Describe("ToggleActiveDelegationBlock", func() {
		Context("when an active delegation is present", func() {
			It("does not panic when toggling", func() {
				view.HandleDelegation(&provider.DelegationInfo{
					SourceAgent: "coordinator",
					TargetAgent: "worker",
					Status:      "running",
					ModelName:   "fast",
				})
				view.RenderContent(80)

				Expect(func() { view.ToggleActiveDelegationBlock() }).NotTo(Panic())
			})
		})

		Context("when no active delegation exists", func() {
			It("does not panic when toggling with nil delegation", func() {
				Expect(func() { view.ToggleActiveDelegationBlock() }).NotTo(Panic())
			})
		})
	})
})
