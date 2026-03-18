package themes_test

import (
	"github.com/baphled/flowstate/internal/ui/themes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Loading Theme Integration", func() {
	var theme themes.Theme

	BeforeEach(func() {
		theme = themes.NewDefaultTheme()
	})

	Describe("SpinnerType", func() {
		It("should have all spinner type constants defined", func() {
			Expect(themes.SpinnerDot).To(Equal(themes.SpinnerType(0)))
			Expect(themes.SpinnerLine).To(Equal(themes.SpinnerType(1)))
			Expect(themes.SpinnerMiniDot).To(Equal(themes.SpinnerType(2)))
		})
	})

	Describe("NewThemedSpinnerWithType", func() {
		It("should create a dot spinner", func() {
			s := themes.NewThemedSpinnerWithType(theme, themes.SpinnerDot)
			Expect(s).NotTo(BeNil())
		})

		It("should create a line spinner", func() {
			s := themes.NewThemedSpinnerWithType(theme, themes.SpinnerLine)
			Expect(s).NotTo(BeNil())
		})

		It("should create a mini dot spinner", func() {
			s := themes.NewThemedSpinnerWithType(theme, themes.SpinnerMiniDot)
			Expect(s).NotTo(BeNil())
		})

		It("should create a jump spinner", func() {
			s := themes.NewThemedSpinnerWithType(theme, themes.SpinnerJump)
			Expect(s).NotTo(BeNil())
		})

		It("should create a pulse spinner", func() {
			s := themes.NewThemedSpinnerWithType(theme, themes.SpinnerPulse)
			Expect(s).NotTo(BeNil())
		})

		It("should create a points spinner", func() {
			s := themes.NewThemedSpinnerWithType(theme, themes.SpinnerPoints)
			Expect(s).NotTo(BeNil())
		})

		It("should create a globe spinner", func() {
			s := themes.NewThemedSpinnerWithType(theme, themes.SpinnerGlobe)
			Expect(s).NotTo(BeNil())
		})

		It("should create a moon spinner", func() {
			s := themes.NewThemedSpinnerWithType(theme, themes.SpinnerMoon)
			Expect(s).NotTo(BeNil())
		})

		It("should create a monkey spinner", func() {
			s := themes.NewThemedSpinnerWithType(theme, themes.SpinnerMonkey)
			Expect(s).NotTo(BeNil())
		})

		It("should create a meter spinner", func() {
			s := themes.NewThemedSpinnerWithType(theme, themes.SpinnerMeter)
			Expect(s).NotTo(BeNil())
		})

		It("should create a hamburger spinner", func() {
			s := themes.NewThemedSpinnerWithType(theme, themes.SpinnerHamburger)
			Expect(s).NotTo(BeNil())
		})

		It("should handle nil theme", func() {
			s := themes.NewThemedSpinnerWithType(nil, themes.SpinnerDot)
			Expect(s).NotTo(BeNil())
		})

		It("should handle invalid spinner type", func() {
			s := themes.NewThemedSpinnerWithType(theme, themes.SpinnerType(999))
			Expect(s).NotTo(BeNil())
		})

		It("should render spinner with theme", func() {
			s := themes.NewThemedSpinnerWithType(theme, themes.SpinnerDot)
			view := s.View()
			Expect(view).NotTo(BeEmpty())
		})

		It("should render spinner without theme", func() {
			s := themes.NewThemedSpinnerWithType(nil, themes.SpinnerLine)
			view := s.View()
			Expect(view).NotTo(BeEmpty())
		})
	})

	Describe("LoadingView", func() {
		var loadingView *themes.LoadingView

		BeforeEach(func() {
			loadingView = themes.NewLoadingView(theme, "Loading...")
		})

		It("should create a loading view", func() {
			Expect(loadingView).NotTo(BeNil())
		})

		It("should render the view", func() {
			view := loadingView.View()
			Expect(view).To(ContainSubstring("Loading"))
		})

		It("should allow setting message", func() {
			loadingView.SetMessage("Processing...")
			view := loadingView.View()
			Expect(view).To(ContainSubstring("Processing"))
		})

		It("should return the spinner model", func() {
			spinner := loadingView.GetSpinner()
			Expect(spinner).NotTo(BeNil())
		})

		It("should set spinner model", func() {
			newSpinner := themes.NewThemedSpinner(theme)
			loadingView.SetSpinner(newSpinner)
			spinner := loadingView.GetSpinner()
			Expect(spinner).NotTo(BeNil())
		})

		It("should render after setting spinner", func() {
			newSpinner := themes.NewThemedSpinner(theme)
			loadingView.SetSpinner(newSpinner)
			view := loadingView.View()
			Expect(view).To(ContainSubstring("Loading"))
		})

		It("should handle nil theme", func() {
			lv := themes.NewLoadingView(nil, "Test message")
			view := lv.View()
			Expect(view).To(ContainSubstring("Test message"))
		})

		It("should handle nil theme with SetSpinner", func() {
			lv := themes.NewLoadingView(nil, "Test message")
			newSpinner := themes.NewThemedSpinner(nil)
			lv.SetSpinner(newSpinner)
			view := lv.View()
			Expect(view).To(ContainSubstring("Test message"))
		})
	})

	Describe("RenderLoadingBox", func() {
		It("should render a loading box with theme", func() {
			result := themes.RenderLoadingBox(theme, "Loading CV", "Generating content...", "⠋")
			Expect(result).To(ContainSubstring("Loading CV"))
			Expect(result).To(ContainSubstring("Generating content"))
		})

		It("should handle nil theme", func() {
			result := themes.RenderLoadingBox(nil, "Title", "Message", "⠋")
			Expect(result).To(ContainSubstring("Title"))
			Expect(result).To(ContainSubstring("Message"))
		})
	})

	Describe("RenderProgressBox", func() {
		It("should render a progress box with theme", func() {
			result := themes.RenderProgressBox(theme, "Exporting", 0.5, "████████░░░░░░░░")
			Expect(result).To(ContainSubstring("Exporting"))
		})

		It("should handle nil theme", func() {
			result := themes.RenderProgressBox(nil, "Progress", 0.75, "███████████░░░░░")
			Expect(result).To(ContainSubstring("Progress"))
		})

		It("should handle zero progress", func() {
			result := themes.RenderProgressBox(theme, "Starting", 0.0, "░░░░░░░░░░░░░░░░")
			Expect(result).To(ContainSubstring("Starting"))
		})

		It("should handle complete progress", func() {
			result := themes.RenderProgressBox(theme, "Complete", 1.0, "████████████████")
			Expect(result).To(ContainSubstring("Complete"))
		})
	})
})
