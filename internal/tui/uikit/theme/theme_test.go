package theme_test

import (
	"testing"

	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/charmbracelet/lipgloss"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTheme(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "UIKit Theme Suite")
}

var _ = Describe("Theme Package", func() {
	Describe("Default()", func() {
		It("should return a valid theme", func() {
			th := theme.Default()
			Expect(th).NotTo(BeNil())
			Expect(th.Name()).NotTo(BeEmpty())
		})

		It("should return a theme with valid colors", func() {
			th := theme.Default()
			Expect(th.PrimaryColor()).NotTo(Equal(lipgloss.Color("")))
			Expect(th.SecondaryColor()).NotTo(Equal(lipgloss.Color("")))
			Expect(th.AccentColor()).NotTo(Equal(lipgloss.Color("")))
			Expect(th.ErrorColor()).NotTo(Equal(lipgloss.Color("")))
			Expect(th.SuccessColor()).NotTo(Equal(lipgloss.Color("")))
			Expect(th.WarningColor()).NotTo(Equal(lipgloss.Color("")))
			Expect(th.BorderColor()).NotTo(Equal(lipgloss.Color("")))
			Expect(th.BackgroundColor()).NotTo(Equal(lipgloss.Color("")))
			Expect(th.MutedColor()).NotTo(Equal(lipgloss.Color("")))
		})
	})

	Describe("Aware", func() {
		var aware *theme.Aware

		BeforeEach(func() {
			aware = &theme.Aware{}
		})

		Describe("SetTheme()", func() {
			It("should store the theme", func() {
				th := theme.Default()
				aware.SetTheme(th)
				Expect(aware.Theme()).To(Equal(th))
			})
		})

		Describe("Theme()", func() {
			It("should return the stored theme", func() {
				th := theme.Default()
				aware.SetTheme(th)
				retrieved := aware.Theme()
				Expect(retrieved).To(Equal(th))
			})

			It("should return default theme when nil", func() {
				// Don't set any theme
				th := aware.Theme()
				Expect(th).NotTo(BeNil())
				Expect(th.Name()).To(Equal(theme.Default().Name()))
			})
		})

		Describe("Color Getters", func() {
			BeforeEach(func() {
				aware.SetTheme(theme.Default())
			})

			It("should return valid PrimaryColor", func() {
				color := aware.PrimaryColor()
				Expect(color).NotTo(Equal(lipgloss.Color("")))
			})

			It("should return valid SecondaryColor", func() {
				color := aware.SecondaryColor()
				Expect(color).NotTo(Equal(lipgloss.Color("")))
			})

			It("should return valid AccentColor", func() {
				color := aware.AccentColor()
				Expect(color).NotTo(Equal(lipgloss.Color("")))
			})

			It("should return valid ErrorColor", func() {
				color := aware.ErrorColor()
				Expect(color).NotTo(Equal(lipgloss.Color("")))
			})

			It("should return valid SuccessColor", func() {
				color := aware.SuccessColor()
				Expect(color).NotTo(Equal(lipgloss.Color("")))
			})

			It("should return valid WarningColor", func() {
				color := aware.WarningColor()
				Expect(color).NotTo(Equal(lipgloss.Color("")))
			})

			It("should return valid BorderColor", func() {
				color := aware.BorderColor()
				Expect(color).NotTo(Equal(lipgloss.Color("")))
			})

			It("should return valid BackgroundColor", func() {
				color := aware.BackgroundColor()
				Expect(color).NotTo(Equal(lipgloss.Color("")))
			})

			It("should return valid MutedColor", func() {
				color := aware.MutedColor()
				Expect(color).NotTo(Equal(lipgloss.Color("")))
			})
		})

		Describe("Color Getters with Nil Theme", func() {
			It("should return valid colors from default theme", func() {
				// Don't set any theme - should use default
				Expect(aware.PrimaryColor()).NotTo(Equal(lipgloss.Color("")))
				Expect(aware.SecondaryColor()).NotTo(Equal(lipgloss.Color("")))
				Expect(aware.AccentColor()).NotTo(Equal(lipgloss.Color("")))
				Expect(aware.ErrorColor()).NotTo(Equal(lipgloss.Color("")))
				Expect(aware.SuccessColor()).NotTo(Equal(lipgloss.Color("")))
				Expect(aware.WarningColor()).NotTo(Equal(lipgloss.Color("")))
				Expect(aware.BorderColor()).NotTo(Equal(lipgloss.Color("")))
				Expect(aware.BackgroundColor()).NotTo(Equal(lipgloss.Color("")))
				Expect(aware.MutedColor()).NotTo(Equal(lipgloss.Color("")))
			})
		})
	})
})
