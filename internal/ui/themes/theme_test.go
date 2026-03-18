package themes_test

import (
	"github.com/baphled/flowstate/internal/ui/themes"
	"github.com/charmbracelet/lipgloss"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Theme", func() {
	Describe("ColorPalette", func() {
		var palette *themes.ColorPalette

		BeforeEach(func() {
			palette = &themes.ColorPalette{
				// Background colors
				Background:     lipgloss.Color("#1a1f2e"),
				BackgroundAlt:  lipgloss.Color("#242936"),
				BackgroundCard: lipgloss.Color("#2d3346"),

				// Foreground colors
				Foreground:      lipgloss.Color("#c7ccd1"),
				ForegroundDim:   lipgloss.Color("#8b92a0"),
				ForegroundMuted: lipgloss.Color("#5e6673"),

				// Accent colors
				Primary:   lipgloss.Color("#5fb3b3"),
				Secondary: lipgloss.Color("#6cb56c"),
				Tertiary:  lipgloss.Color("#a99bd1"),

				// Status colors
				Success: lipgloss.Color("#6cb56c"),
				Warning: lipgloss.Color("#d9a66c"),
				Error:   lipgloss.Color("#d76e6e"),
				Info:    lipgloss.Color("#6ab0d3"),

				// Border colors
				Border:       lipgloss.Color("#3d4454"),
				BorderActive: lipgloss.Color("#5fb3b3"),
				BorderError:  lipgloss.Color("#d76e6e"),

				// Special colors
				Selection: lipgloss.Color("#3d4454"),
				Highlight: lipgloss.Color("#4d5566"),
				Link:      lipgloss.Color("#6ab0d3"),
			}
		})

		It("should have all background colors defined", func() {
			Expect(palette.Background).NotTo(BeEmpty())
			Expect(palette.BackgroundAlt).NotTo(BeEmpty())
			Expect(palette.BackgroundCard).NotTo(BeEmpty())
		})

		It("should have all foreground colors defined", func() {
			Expect(palette.Foreground).NotTo(BeEmpty())
			Expect(palette.ForegroundDim).NotTo(BeEmpty())
			Expect(palette.ForegroundMuted).NotTo(BeEmpty())
		})

		It("should have all accent colors defined", func() {
			Expect(palette.Primary).NotTo(BeEmpty())
			Expect(palette.Secondary).NotTo(BeEmpty())
			Expect(palette.Tertiary).NotTo(BeEmpty())
		})

		It("should have all status colors defined", func() {
			Expect(palette.Success).NotTo(BeEmpty())
			Expect(palette.Warning).NotTo(BeEmpty())
			Expect(palette.Error).NotTo(BeEmpty())
			Expect(palette.Info).NotTo(BeEmpty())
		})

		It("should have all border colors defined", func() {
			Expect(palette.Border).NotTo(BeEmpty())
			Expect(palette.BorderActive).NotTo(BeEmpty())
			Expect(palette.BorderError).NotTo(BeEmpty())
		})

		It("should have all special colors defined", func() {
			Expect(palette.Selection).NotTo(BeEmpty())
			Expect(palette.Highlight).NotTo(BeEmpty())
			Expect(palette.Link).NotTo(BeEmpty())
		})
	})

	Describe("Theme Interface", func() {
		var theme themes.Theme

		BeforeEach(func() {
			palette := &themes.ColorPalette{
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
			theme = themes.NewBaseTheme("test-theme", "Test Theme", "Test Author", true, palette)
		})

		Describe("Metadata", func() {
			It("should return the theme name", func() {
				Expect(theme.Name()).To(Equal("test-theme"))
			})

			It("should return the theme description", func() {
				Expect(theme.Description()).To(Equal("Test Theme"))
			})

			It("should return the theme author", func() {
				Expect(theme.Author()).To(Equal("Test Author"))
			})

			It("should return whether the theme is dark", func() {
				Expect(theme.IsDark()).To(BeTrue())
			})
		})

		Describe("Color Access", func() {
			It("should return the palette", func() {
				Expect(theme.Palette()).NotTo(BeNil())
				Expect(theme.Palette().Background).To(Equal(lipgloss.Color("#1a1f2e")))
			})

			It("should return the styles", func() {
				Expect(theme.Styles()).NotTo(BeNil())
			})
		})

		Describe("Semantic Color Helpers", func() {
			It("should return primary color", func() {
				Expect(theme.PrimaryColor()).To(Equal(lipgloss.Color("#5fb3b3")))
			})

			It("should return secondary color", func() {
				Expect(theme.SecondaryColor()).To(Equal(lipgloss.Color("#6cb56c")))
			})

			It("should return accent color (tertiary)", func() {
				Expect(theme.AccentColor()).To(Equal(lipgloss.Color("#a99bd1")))
			})

			It("should return background color", func() {
				Expect(theme.BackgroundColor()).To(Equal(lipgloss.Color("#1a1f2e")))
			})

			It("should return foreground color", func() {
				Expect(theme.ForegroundColor()).To(Equal(lipgloss.Color("#c7ccd1")))
			})

			It("should return muted color", func() {
				Expect(theme.MutedColor()).To(Equal(lipgloss.Color("#5e6673")))
			})

			It("should return success color", func() {
				Expect(theme.SuccessColor()).To(Equal(lipgloss.Color("#6cb56c")))
			})

			It("should return warning color", func() {
				Expect(theme.WarningColor()).To(Equal(lipgloss.Color("#d9a66c")))
			})

			It("should return error color", func() {
				Expect(theme.ErrorColor()).To(Equal(lipgloss.Color("#d76e6e")))
			})

			It("should return info color", func() {
				Expect(theme.InfoColor()).To(Equal(lipgloss.Color("#6ab0d3")))
			})

			It("should return border color", func() {
				Expect(theme.BorderColor()).To(Equal(lipgloss.Color("#3d4454")))
			})

			It("should return border active color", func() {
				Expect(theme.BorderActiveColor()).To(Equal(lipgloss.Color("#5fb3b3")))
			})
		})
	})

	Describe("BaseTheme", func() {
		It("should create a theme with the given parameters", func() {
			palette := &themes.ColorPalette{
				Background: lipgloss.Color("#000000"),
				Foreground: lipgloss.Color("#ffffff"),
				Primary:    lipgloss.Color("#ff0000"),
			}
			theme := themes.NewBaseTheme("custom", "Custom Theme", "Author", false, palette)

			Expect(theme.Name()).To(Equal("custom"))
			Expect(theme.Description()).To(Equal("Custom Theme"))
			Expect(theme.Author()).To(Equal("Author"))
			Expect(theme.IsDark()).To(BeFalse())
			Expect(theme.Palette()).To(Equal(palette))
		})

		It("should generate styles from the palette", func() {
			palette := &themes.ColorPalette{
				Background:     lipgloss.Color("#1a1f2e"),
				BackgroundCard: lipgloss.Color("#2d3346"),
				Foreground:     lipgloss.Color("#c7ccd1"),
				Primary:        lipgloss.Color("#5fb3b3"),
				Border:         lipgloss.Color("#3d4454"),
			}
			theme := themes.NewBaseTheme("test", "Test", "Author", true, palette)

			styles := theme.Styles()
			Expect(styles).NotTo(BeNil())
			Expect(styles.ButtonBase).NotTo(BeZero())
			Expect(styles.CardBase).NotTo(BeZero())
		})
	})
})
