package themes_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/charmbracelet/huh"

	"github.com/baphled/flowstate/internal/ui/themes"
)

// GenerateHuhTheme + NewThemedForm specs cover the theme adapter for the
// Charm Huh form library:
//   - GenerateHuhTheme(nil) falls back to the Catppuccin defaults rather
//     than panicking; the result is a non-nil *huh.Theme.
//   - GenerateHuhTheme(activeTheme) produces a theme whose Focused styles
//     are bold (title, select selector) and whose Blurred styles are not
//     — a quick visual-hierarchy contract.
//   - The Help short-key style is bold so keymap rows stay legible.
//   - NewThemedForm threads the theme into a constructed huh.Form and
//     accepts a nil theme (falls back the same way as GenerateHuhTheme).
var _ = Describe("themes.GenerateHuhTheme + NewThemedForm", func() {
	Describe("GenerateHuhTheme", func() {
		It("returns a non-nil theme when given nil (falls back to Catppuccin)", func() {
			Expect(themes.GenerateHuhTheme(nil)).NotTo(BeNil())
		})

		It("returns a non-nil theme when given the active theme manager output", func() {
			tm := themes.NewThemeManager()
			Expect(themes.GenerateHuhTheme(tm.Active())).NotTo(BeNil())
		})

		It("makes the Focused.Title and SelectSelector bold", func() {
			tm := themes.NewThemeManager()
			result := themes.GenerateHuhTheme(tm.Active())
			Expect(result.Focused.Title.GetBold()).To(BeTrue())
			Expect(result.Focused.SelectSelector.GetBold()).To(BeTrue())
		})

		It("does NOT make the Blurred.Title bold", func() {
			tm := themes.NewThemeManager()
			result := themes.GenerateHuhTheme(tm.Active())
			Expect(result.Blurred.Title.GetBold()).To(BeFalse())
		})

		It("makes the Help.ShortKey bold", func() {
			tm := themes.NewThemeManager()
			result := themes.GenerateHuhTheme(tm.Active())
			Expect(result.Help.ShortKey.GetBold()).To(BeTrue())
		})
	})

	Describe("NewThemedForm", func() {
		It("returns a non-nil form when given the active theme", func() {
			tm := themes.NewThemeManager()
			group := huh.NewGroup(
				huh.NewInput().
					Key("test").
					Title("Test Input"),
			)
			Expect(themes.NewThemedForm(tm.Active(), group)).NotTo(BeNil())
		})

		It("returns a non-nil form when given nil theme (falls back to Catppuccin)", func() {
			group := huh.NewGroup(
				huh.NewInput().
					Key("test").
					Title("Test Input"),
			)
			Expect(themes.NewThemedForm(nil, group)).NotTo(BeNil())
		})
	})
})
