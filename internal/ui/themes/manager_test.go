package themes_test

import (
	"github.com/baphled/flowstate/internal/ui/themes"
	"github.com/charmbracelet/lipgloss"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ThemeManager", func() {
	var manager *themes.ThemeManager

	BeforeEach(func() {
		manager = themes.NewThemeManager()
	})

	Describe("NewThemeManager", func() {
		It("should create a new theme manager", func() {
			Expect(manager).NotTo(BeNil())
		})

		It("should have the default theme registered", func() {
			Expect(manager.Active()).NotTo(BeNil())
			Expect(manager.Active().Name()).To(Equal("default"))
		})
	})

	Describe("Register", func() {
		It("should register a new theme", func() {
			customTheme := createTestTheme("custom", "Custom Theme")
			err := manager.Register(customTheme)
			Expect(err).NotTo(HaveOccurred())
			Expect(manager.List()).To(ContainElement("custom"))
		})

		It("should return an error if theme is nil", func() {
			err := manager.Register(nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("nil"))
		})

		It("should return an error if theme name is empty", func() {
			emptyNameTheme := createTestTheme("", "Empty Name Theme")
			err := manager.Register(emptyNameTheme)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("name"))
		})

		It("should return an error if theme with same name already exists", func() {
			customTheme := createTestTheme("custom", "Custom Theme")
			err := manager.Register(customTheme)
			Expect(err).NotTo(HaveOccurred())

			duplicateTheme := createTestTheme("custom", "Duplicate Theme")
			err = manager.Register(duplicateTheme)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("already registered"))
		})
	})

	Describe("SetActive", func() {
		BeforeEach(func() {
			customTheme := createTestTheme("custom", "Custom Theme")
			err := manager.Register(customTheme)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should set the active theme", func() {
			err := manager.SetActive("custom")
			Expect(err).NotTo(HaveOccurred())
			Expect(manager.Active().Name()).To(Equal("custom"))
		})

		It("should return an error if theme does not exist", func() {
			err := manager.SetActive("nonexistent")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))
		})

		It("should keep the previous theme if setting fails", func() {
			originalTheme := manager.Active().Name()
			err := manager.SetActive("nonexistent")
			Expect(err).To(HaveOccurred())
			Expect(manager.Active().Name()).To(Equal(originalTheme))
		})
	})

	Describe("Active", func() {
		It("should return the active theme", func() {
			theme := manager.Active()
			Expect(theme).NotTo(BeNil())
			Expect(theme.Name()).To(Equal("default"))
		})

		It("should return nil if no theme is set", func() {
			// Create a manager without default theme registration
			emptyManager := themes.NewEmptyThemeManager()
			Expect(emptyManager.Active()).To(BeNil())
		})
	})

	Describe("List", func() {
		It("should list all registered themes", func() {
			names := manager.List()
			Expect(names).To(ContainElement("default"))
		})

		It("should include newly registered themes", func() {
			customTheme := createTestTheme("custom", "Custom Theme")
			err := manager.Register(customTheme)
			Expect(err).NotTo(HaveOccurred())

			names := manager.List()
			Expect(names).To(ContainElement("default"))
			Expect(names).To(ContainElement("custom"))
		})

		It("should return themes in alphabetical order", func() {
			_ = manager.Register(createTestTheme("zebra", "Zebra Theme"))
			_ = manager.Register(createTestTheme("alpha", "Alpha Theme"))

			names := manager.List()
			// Check ordering
			Expect(names[0]).To(Equal("alpha"))
		})
	})

	Describe("Get", func() {
		It("should get a theme by name", func() {
			theme, err := manager.Get("default")
			Expect(err).NotTo(HaveOccurred())
			Expect(theme).NotTo(BeNil())
			Expect(theme.Name()).To(Equal("default"))
		})

		It("should return an error if theme does not exist", func() {
			_, err := manager.Get("nonexistent")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))
		})
	})

	Describe("OnChange", func() {
		It("should call callback when theme changes", func() {
			called := false
			var newTheme themes.Theme

			manager.OnChange(func(theme themes.Theme) {
				called = true
				newTheme = theme
			})

			customTheme := createTestTheme("custom", "Custom Theme")
			_ = manager.Register(customTheme)
			err := manager.SetActive("custom")
			Expect(err).NotTo(HaveOccurred())

			Expect(called).To(BeTrue())
			Expect(newTheme.Name()).To(Equal("custom"))
		})

		It("should call multiple callbacks", func() {
			callCount := 0

			manager.OnChange(func(_ themes.Theme) {
				callCount++
			})
			manager.OnChange(func(_ themes.Theme) {
				callCount++
			})

			customTheme := createTestTheme("custom", "Custom Theme")
			_ = manager.Register(customTheme)
			_ = manager.SetActive("custom")

			Expect(callCount).To(Equal(2))
		})

		It("should not call callback if SetActive fails", func() {
			called := false

			manager.OnChange(func(_ themes.Theme) {
				called = true
			})

			_ = manager.SetActive("nonexistent")
			Expect(called).To(BeFalse())
		})
	})

	Describe("RemoveChangeCallback", func() {
		It("should remove a callback", func() {
			callCount := 0
			id := manager.OnChange(func(_ themes.Theme) {
				callCount++
			})

			customTheme := createTestTheme("custom", "Custom Theme")
			_ = manager.Register(customTheme)
			_ = manager.SetActive("custom")
			Expect(callCount).To(Equal(1))

			manager.RemoveChangeCallback(id)

			anotherTheme := createTestTheme("another", "Another Theme")
			_ = manager.Register(anotherTheme)
			_ = manager.SetActive("another")
			Expect(callCount).To(Equal(1)) // Should not have incremented
		})
	})

	Describe("Styles Helper", func() {
		It("should provide quick access to active theme styles", func() {
			styles := manager.Styles()
			Expect(styles).NotTo(BeNil())
			Expect(styles.ButtonBase).NotTo(BeZero())
		})

		It("should return nil if no active theme", func() {
			emptyManager := themes.NewEmptyThemeManager()
			Expect(emptyManager.Styles()).To(BeNil())
		})
	})

	Describe("Palette Helper", func() {
		It("should provide quick access to active theme palette", func() {
			palette := manager.Palette()
			Expect(palette).NotTo(BeNil())
			Expect(palette.Primary).NotTo(BeEmpty())
		})

		It("should return nil if no active theme", func() {
			emptyManager := themes.NewEmptyThemeManager()
			Expect(emptyManager.Palette()).To(BeNil())
		})
	})
})

// Helper to create test themes.
func createTestTheme(name, description string) themes.Theme {
	palette := &themes.ColorPalette{
		Background:      lipgloss.Color("#000000"),
		BackgroundAlt:   lipgloss.Color("#111111"),
		BackgroundCard:  lipgloss.Color("#222222"),
		Foreground:      lipgloss.Color("#ffffff"),
		ForegroundDim:   lipgloss.Color("#cccccc"),
		ForegroundMuted: lipgloss.Color("#888888"),
		Primary:         lipgloss.Color("#ff0000"),
		Secondary:       lipgloss.Color("#00ff00"),
		Tertiary:        lipgloss.Color("#0000ff"),
		Success:         lipgloss.Color("#00ff00"),
		Warning:         lipgloss.Color("#ffff00"),
		Error:           lipgloss.Color("#ff0000"),
		Info:            lipgloss.Color("#0000ff"),
		Border:          lipgloss.Color("#444444"),
		BorderActive:    lipgloss.Color("#666666"),
		BorderError:     lipgloss.Color("#ff0000"),
		Selection:       lipgloss.Color("#333333"),
		Highlight:       lipgloss.Color("#555555"),
		Link:            lipgloss.Color("#0000ff"),
	}
	return themes.NewBaseTheme(name, description, "Test", true, palette)
}
