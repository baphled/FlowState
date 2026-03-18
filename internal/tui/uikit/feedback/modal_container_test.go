package feedback_test

import (
	"github.com/baphled/flowstate/internal/tui/uikit/feedback"
	"github.com/baphled/flowstate/internal/ui/themes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ModalContainer", func() {
	var (
		container *feedback.ModalContainer
		theme     themes.Theme
	)

	BeforeEach(func() {
		theme = themes.NewDefaultTheme()
		container = feedback.NewModalContainer()
	})

	Describe("NewModalContainer", func() {
		It("should create a modal container", func() {
			Expect(container).NotTo(BeNil())
		})

		It("should initialize with default values", func() {
			Expect(container).NotTo(BeNil())
		})

		It("should initialize with default theme", func() {
			view := container.Render()
			Expect(view).NotTo(BeEmpty())
		})
	})

	Describe("getTheme", func() {
		It("should return default theme when nil", func() {
			container.WithTheme(nil)
			view := container.Render()
			Expect(view).NotTo(BeEmpty())
		})

		It("should return custom theme when set", func() {
			container.WithTheme(theme)
			view := container.Render()
			Expect(view).NotTo(BeEmpty())
		})

		It("should use theme in rendering", func() {
			container.SetTitle("Test").WithTheme(theme)
			view := container.Render()
			Expect(view).To(ContainSubstring("Test"))
		})
	})

	Describe("SetTitle", func() {
		It("should set the title", func() {
			container.SetTitle("Test Title")
			Expect(container).NotTo(BeNil())
		})

		It("should allow empty title", func() {
			container.SetTitle("")
			Expect(container).NotTo(BeNil())
		})

		It("should allow multiple title changes", func() {
			container.SetTitle("First Title")
			container.SetTitle("Second Title")
			Expect(container).NotTo(BeNil())
		})
	})

	Describe("SetMessage", func() {
		It("should set the message", func() {
			container.SetMessage("Test message")
			Expect(container).NotTo(BeNil())
		})

		It("should allow empty message", func() {
			container.SetMessage("")
			Expect(container).NotTo(BeNil())
		})

		It("should allow multiple message changes", func() {
			container.SetMessage("First message")
			container.SetMessage("Second message")
			Expect(container).NotTo(BeNil())
		})
	})

	Describe("SetButtons", func() {
		It("should set buttons", func() {
			buttons := []string{"OK", "Cancel"}
			container.SetButtons(buttons)
			Expect(container).NotTo(BeNil())
		})

		It("should allow empty buttons", func() {
			container.SetButtons([]string{})
			Expect(container).NotTo(BeNil())
		})

		It("should allow multiple button sets", func() {
			container.SetButtons([]string{"OK", "Cancel"})
			container.SetButtons([]string{"Yes", "No"})
			Expect(container).NotTo(BeNil())
		})
	})

	Describe("SetInstructions", func() {
		It("should set instructions", func() {
			container.SetInstructions("Use arrow keys to navigate")
			Expect(container).NotTo(BeNil())
		})

		It("should allow empty instructions", func() {
			container.SetInstructions("")
			Expect(container).NotTo(BeNil())
		})

		It("should allow multiple instruction changes", func() {
			container.SetInstructions("First instruction")
			container.SetInstructions("Second instruction")
			Expect(container).NotTo(BeNil())
		})
	})

	Describe("WithDestructiveStyle", func() {
		It("should set destructive style", func() {
			result := container.WithDestructiveStyle()
			Expect(result).To(Equal(container))
		})

		It("should return container for chaining", func() {
			result := container.WithDestructiveStyle()
			Expect(result).NotTo(BeNil())
		})

		It("should allow multiple calls", func() {
			container.WithDestructiveStyle()
			container.WithDestructiveStyle()
			Expect(container).NotTo(BeNil())
		})
	})

	Describe("WithTheme", func() {
		It("should set custom theme", func() {
			result := container.WithTheme(theme)
			Expect(result).To(Equal(container))
		})

		It("should return container for chaining", func() {
			result := container.WithTheme(theme)
			Expect(result).NotTo(BeNil())
		})

		It("should handle nil theme", func() {
			result := container.WithTheme(nil)
			Expect(result).NotTo(BeNil())
		})

		It("should allow multiple theme changes", func() {
			container.WithTheme(theme)
			container.WithTheme(nil)
			Expect(container).NotTo(BeNil())
		})
	})

	Describe("WithWidth", func() {
		It("should set width", func() {
			result := container.WithWidth(100)
			Expect(result).To(Equal(container))
		})

		It("should return container for chaining", func() {
			result := container.WithWidth(80)
			Expect(result).NotTo(BeNil())
		})

		It("should allow multiple width changes", func() {
			container.WithWidth(80)
			container.WithWidth(120)
			Expect(container).NotTo(BeNil())
		})

		It("should handle small widths", func() {
			result := container.WithWidth(40)
			Expect(result).NotTo(BeNil())
		})

		It("should handle large widths", func() {
			result := container.WithWidth(200)
			Expect(result).NotTo(BeNil())
		})
	})

	Describe("WithScrollHint", func() {
		It("should enable scroll hint", func() {
			result := container.WithScrollHint(true)
			Expect(result).To(Equal(container))
		})

		It("should return container for chaining", func() {
			result := container.WithScrollHint(true)
			Expect(result).NotTo(BeNil())
		})

		It("should allow disabling scroll hint", func() {
			result := container.WithScrollHint(false)
			Expect(result).NotTo(BeNil())
		})

		It("should allow multiple scroll hint changes", func() {
			container.WithScrollHint(true)
			container.WithScrollHint(false)
			Expect(container).NotTo(BeNil())
		})
	})

	Describe("Render", func() {
		It("should render the modal", func() {
			container.SetTitle("Test Title")
			container.SetMessage("Test message")
			view := container.Render()
			Expect(view).NotTo(BeEmpty())
		})

		It("should render with all options set", func() {
			container.
				SetTitle("Title").
				SetMessage("Message").
				SetButtons([]string{"OK", "Cancel"}).
				SetInstructions("Instructions").
				WithTheme(theme).
				WithWidth(100)
			view := container.Render()
			Expect(view).NotTo(BeEmpty())
		})

		It("should render with destructive style", func() {
			container.
				SetTitle("Delete").
				SetMessage("Are you sure?").
				WithDestructiveStyle()
			view := container.Render()
			Expect(view).NotTo(BeEmpty())
		})

		It("should render with scroll hint", func() {
			container.
				SetTitle("Title").
				SetMessage("Long message content").
				WithScrollHint(true)
			view := container.Render()
			Expect(view).NotTo(BeEmpty())
		})

		It("should render empty container", func() {
			view := container.Render()
			Expect(view).NotTo(BeEmpty())
		})
	})

	Describe("Fluent API", func() {
		It("should support full method chaining", func() {
			view := container.
				SetTitle("Confirm Action").
				SetMessage("Are you sure you want to proceed?").
				SetButtons([]string{"Yes", "No"}).
				SetInstructions("Press Y or N").
				WithTheme(theme).
				WithWidth(80).
				WithScrollHint(true).
				Render()
			Expect(view).NotTo(BeEmpty())
		})

		It("should support partial chaining", func() {
			container.SetTitle("Title")
			container.SetMessage("Message")
			view := container.Render()
			Expect(view).NotTo(BeEmpty())
		})
	})

	Describe("Integration", func() {
		It("should handle complete workflow", func() {
			container.
				SetTitle("Confirm Delete").
				SetMessage("This action cannot be undone.").
				SetButtons([]string{"Delete", "Cancel"}).
				WithDestructiveStyle().
				WithTheme(theme)

			view := container.Render()
			Expect(view).NotTo(BeEmpty())
			Expect(view).To(ContainSubstring("Confirm Delete"))
		})

		It("should handle theme changes", func() {
			container.SetTitle("Test")
			container.WithTheme(theme)
			view := container.Render()
			Expect(view).NotTo(BeEmpty())
		})

		It("should handle width changes", func() {
			container.SetTitle("Test")
			container.WithWidth(100)
			view := container.Render()
			Expect(view).NotTo(BeEmpty())
		})

		It("should handle multiple resets", func() {
			container.SetTitle("First")
			view1 := container.Render()

			container.SetTitle("Second")
			view2 := container.Render()

			Expect(view1).NotTo(Equal(view2))
		})
	})
})
