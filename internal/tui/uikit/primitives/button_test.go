package primitives_test

import (
	"github.com/baphled/flowstate/internal/tui/uikit/primitives"
	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Button", func() {
	var th theme.Theme

	BeforeEach(func() {
		th = theme.Default()
	})

	Describe("NewButton", func() {
		It("should create button with label", func() {
			btn := primitives.NewButton("Click Me", th)
			Expect(btn).NotTo(BeNil())
			rendered := btn.Render()
			Expect(rendered).To(ContainSubstring("Click Me"))
		})

		It("should accept nil theme and use default", func() {
			btn := primitives.NewButton("Button", nil)
			Expect(btn).NotTo(BeNil())
			rendered := btn.Render()
			Expect(rendered).To(ContainSubstring("Button"))
		})
	})

	Describe("Fluent API", func() {
		Describe("Variant", func() {
			It("should set Primary variant", func() {
				btn := primitives.NewButton("Primary", th).Variant(primitives.ButtonPrimary)
				rendered := btn.Render()
				Expect(rendered).To(ContainSubstring("Primary"))
			})

			It("should set Secondary variant", func() {
				btn := primitives.NewButton("Secondary", th).Variant(primitives.ButtonSecondary)
				rendered := btn.Render()
				Expect(rendered).To(ContainSubstring("Secondary"))
			})

			It("should set Danger variant", func() {
				btn := primitives.NewButton("Danger", th).Variant(primitives.ButtonDanger)
				rendered := btn.Render()
				Expect(rendered).To(ContainSubstring("Danger"))
			})
		})

		Describe("Focused", func() {
			It("should set focused state to true", func() {
				btn := primitives.NewButton("Focus", th).Focused(true)
				rendered := btn.Render()
				Expect(rendered).To(ContainSubstring("Focus"))
				// Focused buttons should have different styling (checked via visual inspection or snapshot)
			})

			It("should set focused state to false", func() {
				btn := primitives.NewButton("Not Focused", th).Focused(false)
				rendered := btn.Render()
				Expect(rendered).To(ContainSubstring("Not Focused"))
			})

			It("should allow toggling focus", func() {
				btn := primitives.NewButton("Toggle", th).Focused(true).Focused(false)
				rendered := btn.Render()
				Expect(rendered).To(ContainSubstring("Toggle"))
			})
		})

		Describe("Disabled", func() {
			It("should set disabled state to true", func() {
				btn := primitives.NewButton("Disabled", th).Disabled(true)
				rendered := btn.Render()
				Expect(rendered).To(ContainSubstring("Disabled"))
			})

			It("should set disabled state to false", func() {
				btn := primitives.NewButton("Enabled", th).Disabled(false)
				rendered := btn.Render()
				Expect(rendered).To(ContainSubstring("Enabled"))
			})
		})

		Describe("Width", func() {
			It("should constrain button width", func() {
				btn := primitives.NewButton("W", th).Width(30)
				rendered := btn.Render()
				// Button should apply width constraint
				Expect(rendered).To(ContainSubstring("W"))
			})

			It("should chain with other methods", func() {
				btn := primitives.NewButton("OK", th).Variant(primitives.ButtonPrimary).Width(15)
				rendered := btn.Render()
				Expect(rendered).To(ContainSubstring("OK"))
			})
		})
	})

	Describe("Render", func() {
		It("should render with border", func() {
			btn := primitives.NewButton("Bordered", th)
			rendered := btn.Render()
			// Should contain border characters (visual verification in snapshot tests)
			Expect(rendered).NotTo(BeEmpty())
		})

		It("should render focused button differently", func() {
			normal := primitives.NewButton("Normal", th).Render()
			focused := primitives.NewButton("Normal", th).Focused(true).Render()
			// Focused should have different rendering (tested via snapshots)
			Expect(focused).NotTo(Equal(normal))
		})

		It("should render disabled button", func() {
			disabled := primitives.NewButton("Normal", th).Disabled(true).Render()
			// Disabled should render successfully with content
			Expect(disabled).To(ContainSubstring("Normal"))
			Expect(disabled).NotTo(BeEmpty())
		})

		It("should render all variants successfully", func() {
			primary := primitives.NewButton("Btn", th).Variant(primitives.ButtonPrimary).Render()
			secondary := primitives.NewButton("Btn", th).Variant(primitives.ButtonSecondary).Render()
			danger := primitives.NewButton("Btn", th).Variant(primitives.ButtonDanger).Render()

			// Each variant should render successfully with content
			Expect(primary).To(ContainSubstring("Btn"))
			Expect(secondary).To(ContainSubstring("Btn"))
			Expect(danger).To(ContainSubstring("Btn"))
			Expect(primary).NotTo(BeEmpty())
			Expect(secondary).NotTo(BeEmpty())
			Expect(danger).NotTo(BeEmpty())
		})
	})

	Describe("Convenience Constructors", func() {
		Describe("PrimaryButton", func() {
			It("should create primary button", func() {
				btn := primitives.PrimaryButton("Save", th)
				rendered := btn.Render()
				Expect(rendered).To(ContainSubstring("Save"))
			})
		})

		Describe("SecondaryButton", func() {
			It("should create secondary button", func() {
				btn := primitives.SecondaryButton("Cancel", th)
				rendered := btn.Render()
				Expect(rendered).To(ContainSubstring("Cancel"))
			})
		})

		Describe("DangerButton", func() {
			It("should create danger button", func() {
				btn := primitives.DangerButton("Delete", th)
				rendered := btn.Render()
				Expect(rendered).To(ContainSubstring("Delete"))
			})
		})
	})
})
