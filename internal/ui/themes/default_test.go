package themes_test

import (
	"github.com/baphled/flowstate/internal/ui/themes"
	"github.com/charmbracelet/lipgloss"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("DefaultTheme", func() {
	var theme themes.Theme

	BeforeEach(func() {
		theme = themes.NewDefaultTheme()
	})

	Describe("Metadata", func() {
		It("should have name 'default'", func() {
			Expect(theme.Name()).To(Equal("default"))
		})

		It("should have a description", func() {
			Expect(theme.Description()).NotTo(BeEmpty())
		})

		It("should have an author", func() {
			Expect(theme.Author()).NotTo(BeEmpty())
		})

		It("should be a dark theme", func() {
			Expect(theme.IsDark()).To(BeTrue())
		})
	})

	Describe("Palette", func() {
		It("should have all background colors matching current KaRiya colors", func() {
			palette := theme.Palette()
			Expect(palette.Background).To(Equal(lipgloss.Color("#1a1f2e")))
			Expect(palette.BackgroundAlt).To(Equal(lipgloss.Color("#242936")))
			Expect(palette.BackgroundCard).To(Equal(lipgloss.Color("#2d3346")))
		})

		It("should have all foreground colors matching current KaRiya colors", func() {
			palette := theme.Palette()
			Expect(palette.Foreground).To(Equal(lipgloss.Color("#c7ccd1")))
			Expect(palette.ForegroundDim).To(Equal(lipgloss.Color("#8b92a0")))
			Expect(palette.ForegroundMuted).To(Equal(lipgloss.Color("#5e6673")))
		})

		It("should have all accent colors matching current KaRiya colors", func() {
			palette := theme.Palette()
			Expect(palette.Primary).To(Equal(lipgloss.Color("#5fb3b3")))   // Teal
			Expect(palette.Secondary).To(Equal(lipgloss.Color("#6cb56c"))) // Green
			Expect(palette.Tertiary).To(Equal(lipgloss.Color("#a99bd1")))  // Purple
		})

		It("should have all status colors matching current KaRiya colors", func() {
			palette := theme.Palette()
			Expect(palette.Success).To(Equal(lipgloss.Color("#6cb56c")))
			Expect(palette.Warning).To(Equal(lipgloss.Color("#d9a66c")))
			Expect(palette.Error).To(Equal(lipgloss.Color("#d76e6e")))
			Expect(palette.Info).To(Equal(lipgloss.Color("#6ab0d3")))
		})

		It("should have all border colors matching current KaRiya colors", func() {
			palette := theme.Palette()
			Expect(palette.Border).To(Equal(lipgloss.Color("#3d4454")))
			Expect(palette.BorderActive).To(Equal(lipgloss.Color("#5fb3b3")))
			Expect(palette.BorderError).To(Equal(lipgloss.Color("#d76e6e")))
		})

		It("should have all special colors defined", func() {
			palette := theme.Palette()
			Expect(palette.Selection).To(Equal(lipgloss.Color("#3d4454")))
			Expect(palette.Highlight).To(Equal(lipgloss.Color("#4d5566")))
			Expect(palette.Link).To(Equal(lipgloss.Color("#6ab0d3")))
		})
	})

	Describe("Semantic Color Helpers", func() {
		It("should return correct primary color", func() {
			Expect(theme.PrimaryColor()).To(Equal(lipgloss.Color("#5fb3b3")))
		})

		It("should return correct secondary color", func() {
			Expect(theme.SecondaryColor()).To(Equal(lipgloss.Color("#6cb56c")))
		})

		It("should return correct accent color", func() {
			Expect(theme.AccentColor()).To(Equal(lipgloss.Color("#a99bd1")))
		})

		It("should return correct background color", func() {
			Expect(theme.BackgroundColor()).To(Equal(lipgloss.Color("#1a1f2e")))
		})

		It("should return correct foreground color", func() {
			Expect(theme.ForegroundColor()).To(Equal(lipgloss.Color("#c7ccd1")))
		})

		It("should return correct muted color", func() {
			Expect(theme.MutedColor()).To(Equal(lipgloss.Color("#5e6673")))
		})

		It("should return correct success color", func() {
			Expect(theme.SuccessColor()).To(Equal(lipgloss.Color("#6cb56c")))
		})

		It("should return correct warning color", func() {
			Expect(theme.WarningColor()).To(Equal(lipgloss.Color("#d9a66c")))
		})

		It("should return correct error color", func() {
			Expect(theme.ErrorColor()).To(Equal(lipgloss.Color("#d76e6e")))
		})

		It("should return correct info color", func() {
			Expect(theme.InfoColor()).To(Equal(lipgloss.Color("#6ab0d3")))
		})

		It("should return correct border color", func() {
			Expect(theme.BorderColor()).To(Equal(lipgloss.Color("#3d4454")))
		})

		It("should return correct border active color", func() {
			Expect(theme.BorderActiveColor()).To(Equal(lipgloss.Color("#5fb3b3")))
		})
	})

	Describe("Styles", func() {
		It("should generate a valid StyleSet", func() {
			styles := theme.Styles()
			Expect(styles).NotTo(BeNil())
		})

		It("should have all button styles", func() {
			styles := theme.Styles()
			Expect(styles.ButtonBase).NotTo(BeZero())
			Expect(styles.ButtonPrimary).NotTo(BeZero())
			Expect(styles.ButtonSecondary).NotTo(BeZero())
			Expect(styles.ButtonFocused).NotTo(BeZero())
			Expect(styles.ButtonDisabled).NotTo(BeZero())
		})

		It("should have all input styles", func() {
			styles := theme.Styles()
			Expect(styles.InputBase).NotTo(BeZero())
			Expect(styles.InputFocused).NotTo(BeZero())
			Expect(styles.InputError).NotTo(BeZero())
		})

		It("should have all card styles", func() {
			styles := theme.Styles()
			Expect(styles.CardBase).NotTo(BeZero())
			Expect(styles.CardHeader).NotTo(BeZero())
			Expect(styles.CardContent).NotTo(BeZero())
			Expect(styles.CardFooter).NotTo(BeZero())
		})
	})

	Describe("Backwards Compatibility", func() {
		It("should use the same colors as the current styles.go", func() {
			// These are the exact colors from internal/cli/styles/styles.go
			palette := theme.Palette()

			// ColorBackground = lipgloss.Color("#1a1f2e")
			Expect(palette.Background).To(Equal(lipgloss.Color("#1a1f2e")))

			// ColorAccentTeal = lipgloss.Color("#5fb3b3")
			Expect(palette.Primary).To(Equal(lipgloss.Color("#5fb3b3")))

			// ColorTextPrimary = lipgloss.Color("#c7ccd1")
			Expect(palette.Foreground).To(Equal(lipgloss.Color("#c7ccd1")))

			// ColorError = lipgloss.Color("#d76e6e")
			Expect(palette.Error).To(Equal(lipgloss.Color("#d76e6e")))
		})
	})
})

var _ = Describe("DefaultPalette", func() {
	It("should be exported and accessible", func() {
		palette := themes.DefaultPalette
		Expect(palette.Background).NotTo(BeEmpty())
		Expect(palette.Foreground).NotTo(BeEmpty())
		Expect(palette.Primary).NotTo(BeEmpty())
	})
})
