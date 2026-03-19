package widgets_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
)

var _ = Describe("StatusIndicator", func() {
	var th theme.Theme

	BeforeEach(func() {
		th = theme.Default()
	})

	Describe("NewStatusIndicator", func() {
		It("creates an indicator in idle state", func() {
			si := widgets.NewStatusIndicator(th)
			Expect(si).NotTo(BeNil())
			Expect(si.IsActive()).To(BeFalse())
		})
	})

	Describe("SetActive", func() {
		It("activates the indicator", func() {
			si := widgets.NewStatusIndicator(th)
			si.SetActive(true)
			Expect(si.IsActive()).To(BeTrue())
		})

		It("deactivates the indicator", func() {
			si := widgets.NewStatusIndicator(th)
			si.SetActive(true)
			si.SetActive(false)
			Expect(si.IsActive()).To(BeFalse())
		})
	})

	Describe("Render", func() {
		Context("when idle", func() {
			It("returns empty string", func() {
				si := widgets.NewStatusIndicator(th)
				Expect(si.Render()).To(BeEmpty())
			})
		})

		Context("when active", func() {
			It("includes streaming text", func() {
				si := widgets.NewStatusIndicator(th)
				si.SetActive(true)
				output := si.Render()
				Expect(output).To(ContainSubstring("Thinking"))
			})

			It("includes a spinner frame", func() {
				si := widgets.NewStatusIndicator(th)
				si.SetActive(true)
				output := si.Render()
				Expect(output).NotTo(BeEmpty())
			})
		})

		Context("with nil theme", func() {
			It("renders without panic", func() {
				si := widgets.NewStatusIndicator(nil)
				si.SetActive(true)
				output := si.Render()
				Expect(output).To(ContainSubstring("Thinking"))
			})
		})
	})

	Describe("Tick", func() {
		It("advances the spinner frame", func() {
			si := widgets.NewStatusIndicator(th)
			si.SetActive(true)
			first := si.Render()
			si.Tick()
			second := si.Render()
			Expect(first).NotTo(BeEmpty())
			Expect(second).NotTo(BeEmpty())
		})
	})
})
