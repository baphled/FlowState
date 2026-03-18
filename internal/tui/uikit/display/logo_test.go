package display_test

import (
	"regexp"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/uikit/display"
	"github.com/baphled/flowstate/internal/ui/themes"
)

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripAnsi(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

var _ = Describe("Logo", func() {
	var logo *display.Logo

	BeforeEach(func() {
		logo = display.NewLogo(false, 80)
	})

	Describe("NewLogo", func() {
		It("creates a non-nil logo", func() {
			Expect(logo).NotTo(BeNil())
		})
	})

	Describe("WithTheme", func() {
		It("sets the theme", func() {
			theme := themes.NewDefaultTheme()
			result := logo.WithTheme(theme)
			Expect(result).To(Equal(logo))
		})
	})

	Describe("WithTagline", func() {
		It("sets the tagline", func() {
			result := logo.WithTagline("My Tagline")
			Expect(result).To(Equal(logo))
		})
	})

	Describe("WithVersion", func() {
		It("sets the version", func() {
			result := logo.WithVersion("v2.0.0")
			Expect(result).To(Equal(logo))
		})
	})

	Describe("SetWidth", func() {
		It("updates the width", func() {
			logo.SetWidth(120)
			view := logo.ViewStatic()
			Expect(view).NotTo(BeEmpty())
		})
	})

	Describe("ShowTagline", func() {
		It("hides tagline when false", func() {
			logo.ShowTagline(false)
			view := stripAnsi(logo.ViewStatic())
			Expect(view).NotTo(ContainSubstring("AI Agent"))
		})
	})

	Describe("ShowVersion", func() {
		It("hides version when false", func() {
			logo.ShowVersion(false)
			view := stripAnsi(logo.ViewStatic())
			Expect(view).NotTo(ContainSubstring("dev"))
		})
	})

	Describe("Init", func() {
		Context("when not animated", func() {
			It("returns nil", func() {
				cmd := logo.Init()
				Expect(cmd).To(BeNil())
			})
		})

		Context("when animated", func() {
			BeforeEach(func() {
				logo = display.NewLogo(true, 80)
			})

			It("returns a tick command", func() {
				cmd := logo.Init()
				Expect(cmd).NotTo(BeNil())
			})
		})
	})

	Describe("Update", func() {
		Context("with a non-TickMsg", func() {
			It("returns model and nil cmd", func() {
				_, cmd := logo.Update(tea.KeyMsg{Type: tea.KeyEnter})
				Expect(cmd).To(BeNil())
			})
		})

		Context("with animated logo and TickMsg", func() {
			BeforeEach(func() {
				logo = display.NewLogo(true, 80)
				logo.Init()
			})

			It("updates fade progress", func() {
				_, cmd := logo.Update(display.TickMsg{})
				Expect(cmd).NotTo(BeNil())
			})
		})
	})

	Describe("View", func() {
		It("renders the ASCII art", func() {
			view := logo.View()
			Expect(view).NotTo(BeEmpty())
		})
	})

	Describe("ViewStatic", func() {
		It("renders the logo at full opacity", func() {
			view := logo.ViewStatic()
			Expect(stripAnsi(view)).To(ContainSubstring("██"))
		})

		It("includes default tagline", func() {
			view := logo.ViewStatic()
			Expect(stripAnsi(view)).To(ContainSubstring("AI Agent Platform"))
		})

		It("includes default version", func() {
			view := logo.ViewStatic()
			Expect(stripAnsi(view)).To(ContainSubstring("dev"))
		})

		Context("with custom tagline and version", func() {
			BeforeEach(func() {
				logo.WithTagline("Custom Tagline").WithVersion("v1.0.0")
			})

			It("renders custom tagline", func() {
				view := stripAnsi(logo.ViewStatic())
				Expect(view).To(ContainSubstring("Custom Tagline"))
			})

			It("renders custom version", func() {
				view := stripAnsi(logo.ViewStatic())
				Expect(view).To(ContainSubstring("v1.0.0"))
			})
		})

		Context("with theme", func() {
			It("renders with theme colours", func() {
				theme := themes.NewDefaultTheme()
				view := logo.WithTheme(theme).ViewStatic()
				Expect(view).NotTo(BeEmpty())
			})
		})
	})

	Describe("GetHeight", func() {
		It("returns logo art height plus tagline and version", func() {
			height := logo.GetHeight()
			Expect(height).To(Equal(display.LogoArtHeight + 3))
		})

		Context("without tagline", func() {
			It("returns reduced height", func() {
				logo.ShowTagline(false)
				height := logo.GetHeight()
				Expect(height).To(Equal(display.LogoArtHeight + 1))
			})
		})

		Context("without version", func() {
			It("returns reduced height", func() {
				logo.ShowVersion(false)
				height := logo.GetHeight()
				Expect(height).To(Equal(display.LogoArtHeight + 2))
			})
		})

		Context("without tagline and version", func() {
			It("returns just art height", func() {
				logo.ShowTagline(false)
				logo.ShowVersion(false)
				height := logo.GetHeight()
				Expect(height).To(Equal(display.LogoArtHeight))
			})
		})
	})

	Describe("GetWidth", func() {
		It("returns the fixed logo width", func() {
			Expect(logo.GetWidth()).To(Equal(88))
		})
	})
})
