package layout_test

import (
	"github.com/baphled/flowstate/internal/tui/uikit/layout"
	"github.com/baphled/flowstate/internal/ui/themes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Header", func() {
	var (
		header *layout.Header
		theme  themes.Theme
	)

	BeforeEach(func() {
		theme = themes.NewDefaultTheme()
		header = layout.NewHeader("Test Title", 80).WithTheme(theme)
	})

	Describe("NewHeader", func() {
		It("creates a header with the given title", func() {
			Expect(header.GetTitle()).To(Equal("Test Title"))
		})

		It("starts with empty subtitle", func() {
			Expect(header.GetSubtitle()).To(BeEmpty())
		})

		It("starts with no breadcrumbs", func() {
			Expect(header.GetBreadcrumbs()).To(BeNil())
		})
	})

	Describe("WithSubtitle", func() {
		It("sets the subtitle", func() {
			header.WithSubtitle("A description")
			Expect(header.GetSubtitle()).To(Equal("A description"))
		})
	})

	Describe("WithBreadcrumbs", func() {
		It("sets breadcrumbs", func() {
			header.WithBreadcrumbs([]string{"Home", "Settings"})
			Expect(header.GetBreadcrumbs()).To(Equal([]string{"Home", "Settings"}))
		})
	})

	Describe("AddBreadcrumb", func() {
		It("appends a breadcrumb", func() {
			header.WithBreadcrumbs([]string{"Home"})
			header.AddBreadcrumb("Settings")
			Expect(header.GetBreadcrumbs()).To(Equal([]string{"Home", "Settings"}))
		})
	})

	Describe("ClearBreadcrumbs", func() {
		It("removes all breadcrumbs", func() {
			header.WithBreadcrumbs([]string{"Home", "Settings"})
			header.ClearBreadcrumbs()
			Expect(header.GetBreadcrumbs()).To(BeEmpty())
		})
	})

	Describe("SetWidth", func() {
		It("updates the width", func() {
			header.SetWidth(120)
			view := header.View()
			Expect(view).NotTo(BeEmpty())
		})
	})

	Describe("SetHeight", func() {
		It("updates the height", func() {
			header.SetHeight(3)
			view := header.View()
			Expect(view).NotTo(BeEmpty())
		})
	})

	Describe("View", func() {
		It("renders the title", func() {
			view := header.View()
			Expect(stripAnsi(view)).To(ContainSubstring("Test Title"))
		})

		Context("with subtitle", func() {
			BeforeEach(func() {
				header.WithSubtitle("A subtitle")
			})

			It("renders the subtitle", func() {
				view := header.View()
				Expect(stripAnsi(view)).To(ContainSubstring("A subtitle"))
			})
		})

		Context("with breadcrumbs", func() {
			BeforeEach(func() {
				header.WithBreadcrumbs([]string{"Home", "Settings"})
			})

			It("renders breadcrumbs", func() {
				view := header.View()
				Expect(stripAnsi(view)).To(ContainSubstring("Home"))
				Expect(stripAnsi(view)).To(ContainSubstring("Settings"))
			})
		})

		Context("with border", func() {
			It("renders a border", func() {
				view := header.WithBorder().View()
				Expect(view).NotTo(BeEmpty())
			})
		})

		Context("with zero width", func() {
			It("returns empty string", func() {
				header.SetWidth(0)
				Expect(header.View()).To(BeEmpty())
			})
		})

		Context("with very long title", func() {
			It("truncates the title", func() {
				longTitle := ""
				for range 200 {
					longTitle += "X"
				}
				h := layout.NewHeader(longTitle, 80).WithTheme(theme)
				view := h.View()
				Expect(view).NotTo(BeEmpty())
			})
		})

		Context("without theme", func() {
			It("uses default theme", func() {
				h := layout.NewHeader("No Theme", 80)
				view := h.View()
				Expect(view).NotTo(BeEmpty())
			})
		})
	})

	Describe("GetClickedBreadcrumbIndex", func() {
		Context("with no breadcrumbs", func() {
			It("returns -1", func() {
				Expect(header.GetClickedBreadcrumbIndex(0, 0)).To(Equal(-1))
			})
		})

		Context("with breadcrumbs", func() {
			BeforeEach(func() {
				header.WithBreadcrumbs([]string{"Home", "Settings"})
			})

			It("returns index for first breadcrumb", func() {
				Expect(header.GetClickedBreadcrumbIndex(0, 0)).To(Equal(0))
			})

			It("returns -1 for position beyond all breadcrumbs", func() {
				Expect(header.GetClickedBreadcrumbIndex(200, 0)).To(Equal(-1))
			})
		})
	})
})
