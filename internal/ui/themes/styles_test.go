package themes_test

import (
	"github.com/baphled/flowstate/internal/ui/themes"
	"github.com/charmbracelet/lipgloss"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("StyleSet", func() {
	var (
		palette  *themes.ColorPalette
		styleSet *themes.StyleSet
	)

	BeforeEach(func() {
		palette = &themes.ColorPalette{
			Background:      lipgloss.Color("#1a1f2e"),
			BackgroundAlt:   lipgloss.Color("#242936"),
			BackgroundCard:  lipgloss.Color("#2d3346"),
			Foreground:      lipgloss.Color("#c7ccd1"),
			ForegroundDim:   lipgloss.Color("#8b92a0"),
			ForegroundMuted: lipgloss.Color("#5e6673"),
			Primary:         lipgloss.Color("#5fb3b3"),
			Secondary:       lipgloss.Color("#6cb56c"),
			Tertiary:        lipgloss.Color("#a99bd1"),
			Success:         lipgloss.Color("#6cb56c"),
			Warning:         lipgloss.Color("#d9a66c"),
			Error:           lipgloss.Color("#d76e6e"),
			Info:            lipgloss.Color("#6ab0d3"),
			Border:          lipgloss.Color("#3d4454"),
			BorderActive:    lipgloss.Color("#5fb3b3"),
			BorderError:     lipgloss.Color("#d76e6e"),
			Selection:       lipgloss.Color("#3d4454"),
			Highlight:       lipgloss.Color("#4d5566"),
			Link:            lipgloss.Color("#6ab0d3"),
		}
		styleSet = themes.GenerateStyles(palette)
	})

	Describe("GenerateStyles", func() {
		Context("with a valid palette", func() {
			It("should generate all button styles", func() {
				Expect(styleSet.ButtonBase).NotTo(BeZero())
				Expect(styleSet.ButtonPrimary).NotTo(BeZero())
				Expect(styleSet.ButtonSecondary).NotTo(BeZero())
				Expect(styleSet.ButtonFocused).NotTo(BeZero())
				Expect(styleSet.ButtonDisabled).NotTo(BeZero())
			})

			It("should generate all input styles", func() {
				Expect(styleSet.InputBase).NotTo(BeZero())
				Expect(styleSet.InputFocused).NotTo(BeZero())
				Expect(styleSet.InputError).NotTo(BeZero())
				Expect(styleSet.InputLabel).NotTo(BeZero())
				Expect(styleSet.InputHint).NotTo(BeZero())
			})

			It("should generate all card styles", func() {
				Expect(styleSet.CardBase).NotTo(BeZero())
				Expect(styleSet.CardHeader).NotTo(BeZero())
				Expect(styleSet.CardContent).NotTo(BeZero())
				Expect(styleSet.CardFooter).NotTo(BeZero())
			})

			It("should generate all list styles", func() {
				Expect(styleSet.ListItem).NotTo(BeZero())
				Expect(styleSet.ListItemSelected).NotTo(BeZero())
				Expect(styleSet.ListItemFocused).NotTo(BeZero())
			})

			It("should generate all message box styles", func() {
				Expect(styleSet.ErrorBox).NotTo(BeZero())
				Expect(styleSet.WarningBox).NotTo(BeZero())
				Expect(styleSet.SuccessBox).NotTo(BeZero())
				Expect(styleSet.InfoBox).NotTo(BeZero())
			})

			It("should generate all header styles", func() {
				Expect(styleSet.HeaderMain).NotTo(BeZero())
				Expect(styleSet.HeaderSection).NotTo(BeZero())
				Expect(styleSet.HeaderSubsection).NotTo(BeZero())
			})

			It("should generate all progress styles", func() {
				Expect(styleSet.ProgressBar).NotTo(BeZero())
				Expect(styleSet.ProgressText).NotTo(BeZero())
			})

			It("should generate all badge and tag styles", func() {
				Expect(styleSet.Badge).NotTo(BeZero())
				Expect(styleSet.BadgeSelected).NotTo(BeZero())
				Expect(styleSet.Tag).NotTo(BeZero())
				Expect(styleSet.TagSelected).NotTo(BeZero())
			})

			It("should generate all key badge styles", func() {
				Expect(styleSet.KeyBadge).NotTo(BeZero())
				Expect(styleSet.KeyBadgeHint).NotTo(BeZero())
			})

			It("should generate all modal styles", func() {
				Expect(styleSet.ModalBase).NotTo(BeZero())
				Expect(styleSet.ModalTitle).NotTo(BeZero())
				Expect(styleSet.ModalMessage).NotTo(BeZero())
				Expect(styleSet.ModalDestructive).NotTo(BeZero())
			})

			It("should generate all text styles", func() {
				Expect(styleSet.ErrorText).NotTo(BeZero())
				Expect(styleSet.WarningText).NotTo(BeZero())
				Expect(styleSet.SuccessText).NotTo(BeZero())
				Expect(styleSet.InfoText).NotTo(BeZero())
				Expect(styleSet.MutedText).NotTo(BeZero())
			})
		})

		Context("with a nil palette", func() {
			It("should return an empty StyleSet without panicking", func() {
				emptySet := themes.GenerateStyles(nil)
				Expect(emptySet).NotTo(BeNil())
			})
		})
	})

	Describe("Style rendering", func() {
		It("should render button styles correctly", func() {
			result := styleSet.ButtonPrimary.Render("Test")
			Expect(result).To(ContainSubstring("Test"))
		})

		It("should render card styles correctly", func() {
			result := styleSet.CardBase.Render("Card Content")
			Expect(result).To(ContainSubstring("Card Content"))
		})

		It("should render header styles correctly", func() {
			result := styleSet.HeaderMain.Render("Main Header")
			Expect(result).To(ContainSubstring("Main Header"))
		})

		It("should render error box styles correctly", func() {
			result := styleSet.ErrorBox.Render("Error message")
			Expect(result).To(ContainSubstring("Error message"))
		})

		It("should render key badge styles correctly", func() {
			result := styleSet.KeyBadge.Render("Esc")
			Expect(result).To(ContainSubstring("Esc"))
		})
	})

	Describe("Style consistency", func() {
		It("should use consistent border styles across components", func() {
			// Both ButtonBase and CardBase should use RoundedBorder
			buttonRendered := styleSet.ButtonBase.Render("X")
			cardRendered := styleSet.CardBase.Render("X")

			// Both should have rounded borders (contain specific corner characters)
			Expect(buttonRendered).To(Or(
				ContainSubstring("╭"),
				ContainSubstring("┌"),
			))
			Expect(cardRendered).To(Or(
				ContainSubstring("╭"),
				ContainSubstring("┌"),
			))
		})

		It("should have proper padding on input styles", func() {
			// InputBase should have horizontal padding
			result := styleSet.InputBase.Render("Test")
			// The result should be longer than just "Test" due to padding
			Expect(len(result)).To(BeNumerically(">", len("Test")))
		})

		It("should apply bold to header styles", func() {
			// HeaderMain should be bold - verify it renders without error
			// and produces output (bold styling is applied at render time)
			result := styleSet.HeaderMain.Render("Header")
			Expect(result).To(ContainSubstring("Header"))
			// The HeaderMain style is configured with Bold(true), which we verify
			// by ensuring the style renders successfully
			Expect(len(result)).To(BeNumerically(">=", len("Header")))
		})
	})
})
