package themes_test

import (
	"os"

	"github.com/baphled/flowstate/internal/ui/themes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Terminal Detector", func() {
	Describe("ColorDepth", func() {
		It("should have correct constant values", func() {
			Expect(themes.ColorDepth16).To(Equal(themes.ColorDepth(16)))
			Expect(themes.ColorDepth256).To(Equal(themes.ColorDepth(256)))
			Expect(themes.ColorDepthTrue).To(Equal(themes.ColorDepth(16777216)))
		})
	})

	Describe("DetectColorDepth", func() {
		var originalColorterm, originalTerm string

		BeforeEach(func() {
			originalColorterm = os.Getenv("COLORTERM")
			originalTerm = os.Getenv("TERM")
		})

		AfterEach(func() {
			os.Setenv("COLORTERM", originalColorterm)
			os.Setenv("TERM", originalTerm)
		})

		Context("with COLORTERM=truecolor", func() {
			It("should detect true color", func() {
				os.Setenv("COLORTERM", "truecolor")
				depth := themes.DetectColorDepth()
				Expect(depth).To(Equal(themes.ColorDepthTrue))
			})
		})

		Context("with COLORTERM=24bit", func() {
			It("should detect true color", func() {
				os.Setenv("COLORTERM", "24bit")
				depth := themes.DetectColorDepth()
				Expect(depth).To(Equal(themes.ColorDepthTrue))
			})
		})

		Context("with TERM containing 256color", func() {
			It("should detect 256 colors", func() {
				os.Setenv("COLORTERM", "")
				os.Setenv("TERM", "xterm-256color")
				depth := themes.DetectColorDepth()
				Expect(depth).To(Equal(themes.ColorDepth256))
			})
		})

		Context("with basic TERM", func() {
			It("should detect 16 colors", func() {
				os.Setenv("COLORTERM", "")
				os.Setenv("TERM", "xterm")
				depth := themes.DetectColorDepth()
				Expect(depth).To(Equal(themes.ColorDepth16))
			})
		})

		Context("with no TERM set", func() {
			It("should default to 16 colors", func() {
				os.Unsetenv("COLORTERM")
				os.Unsetenv("TERM")
				depth := themes.DetectColorDepth()
				Expect(depth).To(Equal(themes.ColorDepth16))
			})
		})
	})

	Describe("DetectDarkMode", func() {
		var originalColorfgbg string

		BeforeEach(func() {
			originalColorfgbg = os.Getenv("COLORFGBG")
		})

		AfterEach(func() {
			if originalColorfgbg != "" {
				os.Setenv("COLORFGBG", originalColorfgbg)
			} else {
				os.Unsetenv("COLORFGBG")
			}
		})

		Context("with dark background (COLORFGBG)", func() {
			It("should detect dark mode", func() {
				os.Setenv("COLORFGBG", "15;0")
				isDark := themes.DetectDarkMode()
				Expect(isDark).To(BeTrue())
			})
		})

		Context("with light background (COLORFGBG)", func() {
			It("should detect light mode", func() {
				os.Setenv("COLORFGBG", "0;15")
				isDark := themes.DetectDarkMode()
				Expect(isDark).To(BeFalse())
			})
		})

		Context("with no COLORFGBG set", func() {
			It("should default to dark mode", func() {
				os.Unsetenv("COLORFGBG")
				isDark := themes.DetectDarkMode()
				Expect(isDark).To(BeTrue())
			})
		})

		Context("with invalid COLORFGBG", func() {
			It("should default to dark mode", func() {
				os.Setenv("COLORFGBG", "invalid")
				isDark := themes.DetectDarkMode()
				Expect(isDark).To(BeTrue())
			})
		})
	})

	Describe("TerminalInfo", func() {
		It("should create terminal info with detected values", func() {
			info := themes.NewTerminalInfo()
			Expect(info).NotTo(BeNil())
			Expect(info.ColorDepth).To(BeElementOf(
				themes.ColorDepth16,
				themes.ColorDepth256,
				themes.ColorDepthTrue,
			))
		})

		It("should report if truecolor is supported", func() {
			info := &themes.TerminalInfo{
				ColorDepth: themes.ColorDepthTrue,
				IsDark:     true,
			}
			Expect(info.SupportsTrueColor()).To(BeTrue())

			info.ColorDepth = themes.ColorDepth256
			Expect(info.SupportsTrueColor()).To(BeFalse())
		})

		It("should report if 256 colors are supported", func() {
			info := &themes.TerminalInfo{
				ColorDepth: themes.ColorDepth256,
				IsDark:     true,
			}
			Expect(info.Supports256Colors()).To(BeTrue())

			info.ColorDepth = themes.ColorDepthTrue
			Expect(info.Supports256Colors()).To(BeTrue()) // TrueColor also supports 256

			info.ColorDepth = themes.ColorDepth16
			Expect(info.Supports256Colors()).To(BeFalse())
		})
	})

	Describe("ThemeManager.AutoSelect", func() {
		var manager *themes.ThemeManager
		var originalColorfgbg string

		BeforeEach(func() {
			manager = themes.NewThemeManager()
			originalColorfgbg = os.Getenv("COLORFGBG")
		})

		AfterEach(func() {
			if originalColorfgbg != "" {
				os.Setenv("COLORFGBG", originalColorfgbg)
			} else {
				os.Unsetenv("COLORFGBG")
			}
		})

		It("should select default theme when no other themes available", func() {
			manager.AutoSelect()
			Expect(manager.Active().Name()).To(Equal("default"))
		})

		It("should prefer dark theme in dark mode", func() {
			// Register a light theme
			lightPalette := &themes.ColorPalette{
				Background: "#ffffff",
				Foreground: "#000000",
			}
			lightTheme := themes.NewBaseTheme("light", "Light Theme", "Test", false, lightPalette)
			_ = manager.Register(lightTheme)

			// In dark mode (default), should stay with dark theme
			os.Unsetenv("COLORFGBG") // Forces dark mode detection
			manager.AutoSelect()
			Expect(manager.Active().IsDark()).To(BeTrue())
		})

		It("should prefer light theme in light mode", func() {
			// Register a light theme
			lightPalette := &themes.ColorPalette{
				Background: "#ffffff",
				Foreground: "#000000",
			}
			lightTheme := themes.NewBaseTheme("light", "Light Theme", "Test", false, lightPalette)
			_ = manager.Register(lightTheme)

			// Set light mode
			os.Setenv("COLORFGBG", "0;15")
			manager.AutoSelect()
			Expect(manager.Active().IsDark()).To(BeFalse())
		})

		It("should handle empty theme list gracefully", func() {
			emptyManager := themes.NewThemeManager()
			// Clear all themes by creating new manager
			emptyManager.AutoSelect()
			// Should not panic
			Expect(emptyManager.Active()).NotTo(BeNil())
		})

		It("should fall back to default theme when no match found", func() {
			manager.AutoSelect()
			Expect(manager.Active().Name()).To(Equal("default"))
		})

		It("should handle theme retrieval errors gracefully", func() {
			manager.AutoSelect()
			// Should complete without error
			Expect(manager.Active()).NotTo(BeNil())
		})
	})
})
