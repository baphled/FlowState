package chat_test

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/baphled/flowstate/internal/tui/views/chat"
)

var _ = Describe("CollapsibleDelegationBlock", func() {
	var (
		info  *provider.DelegationInfo
		block *chat.CollapsibleDelegationBlock
		th    theme.Theme
	)

	BeforeEach(func() {
		th = theme.Default()
		now := time.Now()
		info = &provider.DelegationInfo{
			TargetAgent:  "qa-agent",
			ModelName:    "fast",
			ProviderName: "ollama",
			Status:       "running",
			Description:  "Running tests",
			ToolCalls:    3,
			LastTool:     "bash",
			StartedAt:    &now,
		}
		block = chat.NewCollapsibleDelegationBlock(info, th)
	})

	It("renders collapsed as single line by default", func() {
		rendered := block.Render()

		newlineCount := strings.Count(rendered, "\n")
		Expect(newlineCount).To(Equal(0))

		Expect(rendered).To(ContainSubstring("qa-agent"))
		Expect(rendered).To(ContainSubstring("running"))
	})

	It("renders expanded as multiple lines", func() {
		block.Toggle()
		rendered := block.Render()

		newlineCount := strings.Count(rendered, "\n")
		Expect(newlineCount).To(BeNumerically(">", 2))

		Expect(rendered).To(ContainSubstring("qa-agent"))
		Expect(rendered).To(ContainSubstring("fast"))
		Expect(rendered).To(ContainSubstring("ollama"))
		Expect(rendered).To(ContainSubstring("running"))
	})

	It("toggles expanded state", func() {
		Expect(block.IsExpanded()).To(BeFalse())

		block.Toggle()
		Expect(block.IsExpanded()).To(BeTrue())

		block.Toggle()
		Expect(block.IsExpanded()).To(BeFalse())
	})

	It("stores Y position", func() {
		block.SetYPosition(42)
		Expect(block.YPosition).To(Equal(42))
	})

	It("updates Height after expanded render", func() {
		block.Toggle()
		rendered := block.Render()

		Expect(block.Height).To(BeNumerically(">", 1))

		newlineCount := strings.Count(rendered, "\n")
		expectedHeight := newlineCount + 1
		Expect(block.Height).To(Equal(expectedHeight))
	})

	It("stores frame number", func() {
		block.SetFrame(3)

		rendered := block.Render()

		Expect(rendered).NotTo(BeEmpty())
	})

	It("renders collapsed format with spinner and status", func() {
		rendered := block.Render()

		parts := strings.Fields(rendered)
		Expect(len(parts)).To(BeNumerically(">=", 3))

		Expect(rendered).To(ContainSubstring("qa-agent"))
		Expect(rendered).To(ContainSubstring("[running]"))
	})

	Context("when expanded with timestamp", func() {
		BeforeEach(func() {
			block.Toggle()
		})

		It("displays elapsed time", func() {
			rendered := block.Render()

			Expect(rendered).To(ContainSubstring("Elapsed"))
		})

		It("displays agent and model information", func() {
			rendered := block.Render()

			Expect(rendered).To(ContainSubstring("Agent"))
			Expect(rendered).To(ContainSubstring("Model"))
			Expect(rendered).To(ContainSubstring("qa-agent"))
			Expect(rendered).To(ContainSubstring("fast"))
		})

		It("displays tool call count and last tool", func() {
			rendered := block.Render()

			Expect(rendered).To(ContainSubstring("Tools"))
			Expect(rendered).To(ContainSubstring("bash"))
		})
	})

	Context("when nil theme is provided", func() {
		BeforeEach(func() {
			block = chat.NewCollapsibleDelegationBlock(info, nil)
		})

		It("renders without crashing", func() {
			rendered := block.Render()
			Expect(rendered).NotTo(BeEmpty())
		})

		It("renders collapsed as single line", func() {
			rendered := block.Render()
			newlineCount := strings.Count(rendered, "\n")
			Expect(newlineCount).To(Equal(0))
		})
	})

	Context("when no StartedAt timestamp", func() {
		BeforeEach(func() {
			info.StartedAt = nil
			block = chat.NewCollapsibleDelegationBlock(info, th)
			block.Toggle()
		})

		It("displays 0s elapsed time", func() {
			rendered := block.Render()
			Expect(rendered).To(ContainSubstring("0s"))
		})
	})
})
