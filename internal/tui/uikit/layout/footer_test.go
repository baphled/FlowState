package layout_test

import (
	"github.com/baphled/flowstate/internal/tui/uikit/layout"
	"github.com/baphled/flowstate/internal/ui/themes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Footer", func() {
	var (
		footer *layout.Footer
		theme  themes.Theme
	)

	BeforeEach(func() {
		theme = themes.NewDefaultTheme()
		footer = layout.NewFooter(80).WithTheme(theme)
	})

	Describe("NewFooter", func() {
		It("creates a footer with the given width", func() {
			Expect(footer).NotTo(BeNil())
		})

		It("starts with empty status", func() {
			Expect(footer.GetStatusMessage()).To(BeEmpty())
		})

		It("starts with empty mode", func() {
			Expect(footer.GetModeContext()).To(BeEmpty())
		})
	})

	Describe("WithStatus", func() {
		It("sets the status message", func() {
			footer.WithStatus("3/10 events")
			Expect(footer.GetStatusMessage()).To(Equal("3/10 events"))
		})
	})

	Describe("WithMode", func() {
		It("sets the mode context", func() {
			footer.WithMode("Capture Mode: Timeline")
			Expect(footer.GetModeContext()).To(Equal("Capture Mode: Timeline"))
		})
	})

	Describe("WithHelp", func() {
		It("sets the help text", func() {
			footer.WithHelp("Press q to quit")
			view := footer.View()
			Expect(stripAnsi(view)).To(ContainSubstring("Press q to quit"))
		})
	})

	Describe("SetWidth", func() {
		It("updates the width", func() {
			footer.SetWidth(120)
			footer.WithStatus("test")
			view := footer.View()
			Expect(view).NotTo(BeEmpty())
		})
	})

	Describe("SetHeight", func() {
		It("updates the height", func() {
			footer.SetHeight(3)
			footer.WithStatus("test")
			view := footer.View()
			Expect(view).NotTo(BeEmpty())
		})
	})

	Describe("SetShowStatus", func() {
		It("hides status when set to false", func() {
			footer.WithStatus("hidden")
			footer.SetShowStatus(false)
			view := footer.View()
			Expect(stripAnsi(view)).NotTo(ContainSubstring("hidden"))
		})
	})

	Describe("SetShowMode", func() {
		It("hides mode when set to false", func() {
			footer.WithMode("hidden mode")
			footer.SetShowMode(false)
			view := footer.View()
			Expect(stripAnsi(view)).NotTo(ContainSubstring("hidden mode"))
		})
	})

	Describe("SetShowHelp", func() {
		It("shows help when enabled with text", func() {
			footer.WithHelp("help text")
			footer.SetShowHelp(true)
			view := footer.View()
			Expect(stripAnsi(view)).To(ContainSubstring("help text"))
		})

		It("hides help when set to false", func() {
			footer.WithHelp("help text")
			footer.SetShowHelp(false)
			view := footer.View()
			Expect(stripAnsi(view)).NotTo(ContainSubstring("help text"))
		})
	})

	Describe("View", func() {
		Context("with zero width", func() {
			It("returns empty string", func() {
				footer.SetWidth(0)
				Expect(footer.View()).To(BeEmpty())
			})
		})

		Context("with status only", func() {
			It("renders status message", func() {
				footer.WithStatus("5/20 items")
				view := footer.View()
				Expect(stripAnsi(view)).To(ContainSubstring("5/20 items"))
			})
		})

		Context("with mode only", func() {
			It("renders mode context", func() {
				footer.WithMode("Insert Mode")
				view := footer.View()
				Expect(stripAnsi(view)).To(ContainSubstring("Insert Mode"))
			})
		})

		Context("with both status and mode", func() {
			It("renders both on the same line", func() {
				footer.WithStatus("3/10")
				footer.WithMode("Timeline")
				view := footer.View()
				stripped := stripAnsi(view)
				Expect(stripped).To(ContainSubstring("3/10"))
				Expect(stripped).To(ContainSubstring("Timeline"))
			})
		})

		Context("with status, mode, and help", func() {
			It("renders all three", func() {
				footer.WithStatus("status")
				footer.WithMode("mode")
				footer.WithHelp("help")
				view := footer.View()
				stripped := stripAnsi(view)
				Expect(stripped).To(ContainSubstring("status"))
				Expect(stripped).To(ContainSubstring("mode"))
				Expect(stripped).To(ContainSubstring("help"))
			})
		})

		Context("without theme", func() {
			It("uses default theme", func() {
				f := layout.NewFooter(80)
				f.WithStatus("no theme")
				view := f.View()
				Expect(view).NotTo(BeEmpty())
			})
		})

		Context("with empty content", func() {
			It("renders empty when no status/mode/help set", func() {
				view := footer.View()
				Expect(view).To(BeEmpty())
			})
		})
	})
})
