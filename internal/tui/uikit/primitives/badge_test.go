package primitives_test

import (
	"github.com/baphled/flowstate/internal/tui/uikit/primitives"
	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Badge", func() {
	var th theme.Theme

	BeforeEach(func() {
		th = theme.Default()
	})

	Describe("NewBadge", func() {
		It("should create badge with label", func() {
			badge := primitives.NewBadge("New", th)
			Expect(badge).NotTo(BeNil())
			rendered := badge.Render()
			Expect(rendered).To(ContainSubstring("New"))
		})

		It("should accept nil theme and use default", func() {
			badge := primitives.NewBadge("Test", nil)
			Expect(badge).NotTo(BeNil())
		})
	})

	Describe("Fluent API", func() {
		Describe("Value", func() {
			It("should set value for key badge", func() {
				badge := primitives.NewBadge("Enter", th).Value("confirm")
				rendered := badge.Render()
				Expect(rendered).To(ContainSubstring("Enter"))
			})

			It("should return badge for chaining", func() {
				badge := primitives.NewBadge("Esc", th)
				result := badge.Value("cancel")
				Expect(result).To(Equal(badge))
			})
		})

		Describe("Variant", func() {
			It("should set Default variant", func() {
				badge := primitives.NewBadge("Tag", th).Variant(primitives.BadgeDefault)
				rendered := badge.Render()
				Expect(rendered).To(ContainSubstring("Tag"))
			})

			It("should set Key variant", func() {
				badge := primitives.NewBadge("Tab", th).Variant(primitives.BadgeKey)
				rendered := badge.Render()
				Expect(rendered).To(ContainSubstring("Tab"))
			})

			It("should set Status variant", func() {
				badge := primitives.NewBadge("Active", th).Variant(primitives.BadgeStatus)
				rendered := badge.Render()
				Expect(rendered).To(ContainSubstring("Active"))
			})

			It("should set Tag variant", func() {
				badge := primitives.NewBadge("Feature", th).Variant(primitives.BadgeTag)
				rendered := badge.Render()
				Expect(rendered).To(ContainSubstring("Feature"))
			})

			It("should return badge for chaining", func() {
				badge := primitives.NewBadge("Test", th)
				result := badge.Variant(primitives.BadgeKey)
				Expect(result).To(Equal(badge))
			})
		})
	})

	Describe("Render", func() {
		It("should render default badge", func() {
			badge := primitives.NewBadge("Info", th)
			rendered := badge.Render()
			Expect(rendered).To(ContainSubstring("Info"))
			Expect(rendered).NotTo(BeEmpty())
		})

		It("should render key badge with bracket format", func() {
			badge := primitives.NewBadge("Enter", th).Variant(primitives.BadgeKey)
			rendered := badge.Render()
			Expect(rendered).To(ContainSubstring("Enter"))
		})

		It("should render status badge", func() {
			badge := primitives.NewBadge("Success", th).Variant(primitives.BadgeStatus)
			rendered := badge.Render()
			Expect(rendered).To(ContainSubstring("Success"))
		})

		It("should render tag badge with pill style", func() {
			badge := primitives.NewBadge("Tag", th).Variant(primitives.BadgeTag)
			rendered := badge.Render()
			Expect(rendered).To(ContainSubstring("Tag"))
		})
	})

	Describe("Convenience Constructors", func() {
		Describe("KeyBadge", func() {
			It("should create key badge", func() {
				badge := primitives.KeyBadge("Esc", "cancel", th)
				rendered := badge.Render()
				Expect(rendered).To(ContainSubstring("Esc"))
			})

			It("should format as key-action pair", func() {
				badge := primitives.KeyBadge("Tab", "next", th)
				rendered := badge.Render()
				// Should show key in some format
				Expect(rendered).To(ContainSubstring("Tab"))
			})
		})

		Describe("StatusBadge", func() {
			It("should create status badge", func() {
				badge := primitives.StatusBadge("Complete", th)
				rendered := badge.Render()
				Expect(rendered).To(ContainSubstring("Complete"))
			})
		})

		Describe("TagBadge", func() {
			It("should create tag badge", func() {
				badge := primitives.TagBadge("Feature", th)
				rendered := badge.Render()
				Expect(rendered).To(ContainSubstring("Feature"))
			})
		})

		Describe("HelpKeyBadge", func() {
			It("should create help key badge with two-part styling", func() {
				badge := primitives.HelpKeyBadge("Esc", "Back", th)
				rendered := badge.Render()
				Expect(rendered).To(ContainSubstring("Esc"))
				Expect(rendered).To(ContainSubstring("Back"))
			})

			It("should work with nil theme", func() {
				badge := primitives.HelpKeyBadge("Enter", "Select", nil)
				rendered := badge.Render()
				Expect(rendered).To(ContainSubstring("Enter"))
				Expect(rendered).To(ContainSubstring("Select"))
			})
		})
	})

	Describe("Common Help Key Badge Constructors", func() {
		It("NavigateBadge should return navigation badge", func() {
			badge := primitives.NavigateBadge(th)
			rendered := badge.Render()
			Expect(rendered).To(ContainSubstring("Navigate"))
		})

		It("SelectBadge should return select badge", func() {
			badge := primitives.SelectBadge(th)
			rendered := badge.Render()
			Expect(rendered).To(ContainSubstring("Enter"))
			Expect(rendered).To(ContainSubstring("Select"))
		})

		It("CancelBadge should return cancel badge", func() {
			badge := primitives.CancelBadge(th)
			rendered := badge.Render()
			Expect(rendered).To(ContainSubstring("Esc"))
			Expect(rendered).To(ContainSubstring("Cancel"))
		})

		It("QuitBadge should return quit badge", func() {
			badge := primitives.QuitBadge(th)
			rendered := badge.Render()
			Expect(rendered).To(ContainSubstring("q"))
			Expect(rendered).To(ContainSubstring("Quit"))
		})

		It("BackBadge should return back badge", func() {
			badge := primitives.BackBadge(th)
			rendered := badge.Render()
			Expect(rendered).To(ContainSubstring("Esc"))
			Expect(rendered).To(ContainSubstring("Back"))
		})

		It("EditBadge should return edit badge", func() {
			badge := primitives.EditBadge(th)
			rendered := badge.Render()
			Expect(rendered).To(ContainSubstring("e"))
			Expect(rendered).To(ContainSubstring("Edit"))
		})

		It("DeleteBadge should return delete badge", func() {
			badge := primitives.DeleteBadge(th)
			rendered := badge.Render()
			Expect(rendered).To(ContainSubstring("d"))
			Expect(rendered).To(ContainSubstring("Delete"))
		})

		It("SaveBadge should return save badge", func() {
			badge := primitives.SaveBadge(th)
			rendered := badge.Render()
			Expect(rendered).To(ContainSubstring("Ctrl+S"))
			Expect(rendered).To(ContainSubstring("Save"))
		})

		It("SearchBadge should return search badge", func() {
			badge := primitives.SearchBadge(th)
			rendered := badge.Render()
			Expect(rendered).To(ContainSubstring("/"))
			Expect(rendered).To(ContainSubstring("Search"))
		})

		It("FilterBadge should return filter badge", func() {
			badge := primitives.FilterBadge(th)
			rendered := badge.Render()
			Expect(rendered).To(ContainSubstring("f"))
			Expect(rendered).To(ContainSubstring("Filter"))
		})

		It("YesBadge should return yes badge", func() {
			badge := primitives.YesBadge(th)
			rendered := badge.Render()
			Expect(rendered).To(ContainSubstring("y"))
			Expect(rendered).To(ContainSubstring("Yes"))
		})

		It("NoBadge should return no badge", func() {
			badge := primitives.NoBadge(th)
			rendered := badge.Render()
			Expect(rendered).To(ContainSubstring("n"))
			Expect(rendered).To(ContainSubstring("No"))
		})

		It("ToggleBadge should return toggle badge", func() {
			badge := primitives.ToggleBadge(th)
			rendered := badge.Render()
			Expect(rendered).To(ContainSubstring("Toggle"))
		})

		It("RetryBadge should return retry badge", func() {
			badge := primitives.RetryBadge(th)
			rendered := badge.Render()
			Expect(rendered).To(ContainSubstring("r"))
			Expect(rendered).To(ContainSubstring("Retry"))
		})

		It("RetryEnterBadge should return retry badge with Enter/r", func() {
			badge := primitives.RetryEnterBadge(th)
			rendered := badge.Render()
			Expect(rendered).To(ContainSubstring("Enter/r"))
			Expect(rendered).To(ContainSubstring("Retry"))
		})

		It("ContinueBadge should return continue badge", func() {
			badge := primitives.ContinueBadge(th)
			rendered := badge.Render()
			Expect(rendered).To(ContainSubstring("Enter"))
			Expect(rendered).To(ContainSubstring("Continue"))
		})
	})

	Describe("RenderHelpFooter", func() {
		It("should render multiple badges", func() {
			footer := primitives.RenderHelpFooter(th,
				primitives.NavigateBadge(th),
				primitives.SelectBadge(th),
				primitives.BackBadge(th),
			)
			Expect(footer).To(ContainSubstring("Navigate"))
			Expect(footer).To(ContainSubstring("Select"))
			Expect(footer).To(ContainSubstring("Back"))
		})

		It("should return empty string for empty badges", func() {
			footer := primitives.RenderHelpFooter(th)
			Expect(footer).To(BeEmpty())
		})

		It("should work with nil theme", func() {
			footer := primitives.RenderHelpFooter(nil,
				primitives.NavigateBadge(nil),
				primitives.BackBadge(nil),
			)
			Expect(footer).To(ContainSubstring("Navigate"))
			Expect(footer).To(ContainSubstring("Back"))
		})
	})

	Describe("Standard Footer Presets", func() {
		It("RenderMenuFooter should render menu footer", func() {
			footer := primitives.RenderMenuFooter(th)
			Expect(footer).To(ContainSubstring("Navigate"))
			Expect(footer).To(ContainSubstring("Select"))
			Expect(footer).To(ContainSubstring("Help"))
			Expect(footer).To(ContainSubstring("Quit"))
		})

		It("RenderListFooter should render list footer", func() {
			footer := primitives.RenderListFooter(th)
			Expect(footer).To(ContainSubstring("Navigate"))
			Expect(footer).To(ContainSubstring("Select"))
			Expect(footer).To(ContainSubstring("Back"))
		})

		It("RenderFormFooter should render form footer", func() {
			footer := primitives.RenderFormFooter(th)
			Expect(footer).To(ContainSubstring("Confirm"))
			Expect(footer).To(ContainSubstring("Cancel"))
		})

		It("RenderEditFooter should render edit footer", func() {
			footer := primitives.RenderEditFooter(th)
			Expect(footer).To(ContainSubstring("Save"))
			Expect(footer).To(ContainSubstring("Cancel"))
		})

		It("RenderBrowseFooter should render browse footer", func() {
			footer := primitives.RenderBrowseFooter(th)
			Expect(footer).To(ContainSubstring("Navigate"))
			Expect(footer).To(ContainSubstring("Edit"))
			Expect(footer).To(ContainSubstring("Delete"))
		})

		It("RenderExportFooter should render export footer", func() {
			footer := primitives.RenderExportFooter(th)
			Expect(footer).To(ContainSubstring("Navigate"))
			Expect(footer).To(ContainSubstring("Confirm"))
		})
	})
})
