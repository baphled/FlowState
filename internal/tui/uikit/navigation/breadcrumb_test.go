package navigation

import (
	"testing"

	"github.com/baphled/flowstate/internal/ui/themes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBreadcrumb(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Breadcrumb Suite")
}

var _ = Describe("BreadcrumbBar", func() {
	var bar *BreadcrumbBar
	var theme themes.Theme

	BeforeEach(func() {
		theme = themes.NewDefaultTheme()
		bar = NewBreadcrumbBar(80, false).WithTheme(theme)
	})

	Describe("NewBreadcrumbBar", func() {
		It("creates a breadcrumb bar with correct initial state", func() {
			b := NewBreadcrumbBar(100, true)
			Expect(b).NotTo(BeNil())
			Expect(b.width).To(Equal(100))
			Expect(b.boxed).To(BeTrue())
			Expect(b.showIcon).To(BeTrue())
			Expect(b.crumbs).To(BeEmpty())
		})
	})

	Describe("WithTheme", func() {
		It("sets the theme and returns the bar for chaining", func() {
			result := bar.WithTheme(theme)
			Expect(result).To(Equal(bar))
			Expect(bar.theme).To(Equal(theme))
		})
	})

	Describe("AddCrumb", func() {
		It("adds a breadcrumb to the trail", func() {
			crumb := Breadcrumb{Label: "Home", Icon: IconHome}
			bar.AddCrumb(crumb)
			Expect(bar.crumbs).To(HaveLen(1))
			Expect(bar.crumbs[0]).To(Equal(crumb))
		})

		It("supports chaining multiple crumbs", func() {
			bar.AddCrumb(Breadcrumb{Label: "Home", Icon: IconHome}).
				AddCrumb(Breadcrumb{Label: "Settings", Icon: IconConfigure})
			Expect(bar.crumbs).To(HaveLen(2))
		})
	})

	Describe("SetCrumbs", func() {
		It("replaces the breadcrumb trail", func() {
			crumbs := []Breadcrumb{
				{Label: "Home", Icon: IconHome},
				{Label: "Settings", Icon: IconConfigure},
			}
			bar.SetCrumbs(crumbs)
			Expect(bar.crumbs).To(Equal(crumbs))
		})
	})

	Describe("SetWidth", func() {
		It("sets the width and returns the bar for chaining", func() {
			result := bar.SetWidth(120)
			Expect(result).To(Equal(bar))
			Expect(bar.width).To(Equal(120))
		})
	})

	Describe("SetBoxed", func() {
		It("sets the boxed flag and returns the bar for chaining", func() {
			result := bar.SetBoxed(true)
			Expect(result).To(Equal(bar))
			Expect(bar.boxed).To(BeTrue())
		})
	})

	Describe("ShowIcons", func() {
		It("controls icon visibility and returns the bar for chaining", func() {
			result := bar.ShowIcons(false)
			Expect(result).To(Equal(bar))
			Expect(bar.showIcon).To(BeFalse())
		})
	})

	Describe("View", func() {
		It("returns empty string when no crumbs are set", func() {
			view := bar.View()
			Expect(view).To(Equal(""))
		})

		It("renders a single breadcrumb", func() {
			bar.AddCrumb(Breadcrumb{Label: "Home", Icon: IconHome})
			view := bar.View()
			Expect(view).To(ContainSubstring("Home"))
		})

		It("renders multiple breadcrumbs with separator", func() {
			bar.AddCrumb(Breadcrumb{Label: "Home", Icon: IconHome}).
				AddCrumb(Breadcrumb{Label: "Settings", Icon: IconConfigure})
			view := bar.View()
			Expect(view).To(ContainSubstring("Home"))
			Expect(view).To(ContainSubstring("Settings"))
		})

		It("hides icons when ShowIcons is false", func() {
			bar.ShowIcons(false).
				AddCrumb(Breadcrumb{Label: "Home", Icon: IconHome})
			view := bar.View()
			Expect(view).To(ContainSubstring("Home"))
			Expect(view).NotTo(ContainSubstring(IconHome))
		})

		It("renders with box when boxed is true", func() {
			bar.SetBoxed(true).
				AddCrumb(Breadcrumb{Label: "Home", Icon: IconHome})
			view := bar.View()
			Expect(view).To(ContainSubstring("Home"))
		})

		It("truncates breadcrumbs when they exceed width", func() {
			bar.SetWidth(20).
				AddCrumb(Breadcrumb{Label: "Home", Icon: IconHome}).
				AddCrumb(Breadcrumb{Label: "Settings", Icon: IconConfigure}).
				AddCrumb(Breadcrumb{Label: "Advanced", Icon: IconConfigure})
			view := bar.View()
			Expect(view).To(ContainSubstring("Home"))
			Expect(view).To(ContainSubstring("Advanced"))
		})

		It("uses default theme when theme is nil", func() {
			barNoTheme := NewBreadcrumbBar(80, false)
			barNoTheme.AddCrumb(Breadcrumb{Label: "Home", Icon: IconHome})
			view := barNoTheme.View()
			Expect(view).To(ContainSubstring("Home"))
		})

		It("renders truncated breadcrumbs without icons", func() {
			bar.SetWidth(20).ShowIcons(false).
				AddCrumb(Breadcrumb{Label: "Home", Icon: IconHome}).
				AddCrumb(Breadcrumb{Label: "Settings", Icon: IconConfigure}).
				AddCrumb(Breadcrumb{Label: "Advanced", Icon: IconConfigure})
			view := bar.View()
			Expect(view).To(ContainSubstring("Home"))
			Expect(view).To(ContainSubstring("Advanced"))
		})

		It("handles breadcrumbs with empty icons", func() {
			bar.AddCrumb(Breadcrumb{Label: "Home", Icon: ""}).
				AddCrumb(Breadcrumb{Label: "Settings", Icon: ""})
			view := bar.View()
			Expect(view).To(ContainSubstring("Home"))
			Expect(view).To(ContainSubstring("Settings"))
		})
	})

	Describe("GetIconForIntent", func() {
		It("returns the correct icon for known intents", func() {
			Expect(GetIconForIntent("home")).To(Equal(IconHome))
			Expect(GetIconForIntent("capture_event")).To(Equal(IconCaptureEvent))
			Expect(GetIconForIntent("configure_system")).To(Equal(IconConfigure))
		})

		It("returns empty string for unknown intents", func() {
			Expect(GetIconForIntent("unknown_intent")).To(Equal(""))
		})
	})

	Describe("CreateBreadcrumbTrail", func() {
		It("creates a breadcrumb trail from intent specifications", func() {
			trail := CreateBreadcrumbTrail(
				struct {
					Label  string
					Intent string
				}{Label: "Home", Intent: "home"},
				struct {
					Label  string
					Intent string
				}{Label: "Settings", Intent: "configure_system"},
			)
			Expect(trail).To(HaveLen(2))
			Expect(trail[0].Label).To(Equal("Home"))
			Expect(trail[0].Icon).To(Equal(IconHome))
			Expect(trail[1].Label).To(Equal("Settings"))
			Expect(trail[1].Icon).To(Equal(IconConfigure))
		})
	})

	Describe("renderTruncated edge cases", func() {
		It("handles single crumb gracefully", func() {
			bar.SetWidth(20).
				AddCrumb(Breadcrumb{Label: "Home", Icon: IconHome})
			view := bar.View()
			Expect(view).To(ContainSubstring("Home"))
		})

		It("renders truncated with multiple crumbs and no icons", func() {
			bar.SetWidth(20).ShowIcons(false).
				AddCrumb(Breadcrumb{Label: "Home", Icon: IconHome}).
				AddCrumb(Breadcrumb{Label: "Settings", Icon: IconConfigure}).
				AddCrumb(Breadcrumb{Label: "Advanced", Icon: IconConfigure})
			view := bar.View()
			Expect(view).To(ContainSubstring("Home"))
			Expect(view).To(ContainSubstring("Advanced"))
		})
	})
})
