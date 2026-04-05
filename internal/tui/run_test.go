package tui_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/tui"
	"github.com/baphled/flowstate/internal/tui/intents/agentpicker"
)

var _ = Describe("BuildStartIntent", func() {
	var registry *agent.Registry

	BeforeEach(func() {
		registry = agent.NewRegistry()
		registry.Register(&agent.Manifest{ID: "planner", Name: "Planner"})
		registry.Register(&agent.Manifest{ID: "executor", Name: "Executor"})
	})

	Context("when agentID is empty", func() {
		It("returns an AgentPickerIntent", func() {
			intent := tui.BuildStartIntent("", registry)
			Expect(intent).NotTo(BeNil())
			_, ok := intent.(*agentpicker.Intent)
			Expect(ok).To(BeTrue())
		})

		It("includes all registry agents in the picker", func() {
			intent := tui.BuildStartIntent("", registry)
			view := intent.View()
			Expect(view).To(ContainSubstring("Planner"))
			Expect(view).To(ContainSubstring("Executor"))
		})
	})

	Context("when agentID is not empty", func() {
		It("returns nil so Run uses the chat intent directly", func() {
			result := tui.BuildStartIntent("planner", registry)
			Expect(result).To(BeNil())
		})
	})

	Context("when registry is nil and agentID is empty", func() {
		It("returns an AgentPickerIntent with an empty list", func() {
			intent := tui.BuildStartIntent("", nil)
			Expect(intent).NotTo(BeNil())
			_, ok := intent.(*agentpicker.Intent)
			Expect(ok).To(BeTrue())
		})
	})
})
