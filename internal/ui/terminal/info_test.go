package terminal_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/ui/terminal"
)

var _ = Describe("NewInfo", func() {
	It("returns a non-nil Info", func() {
		info := terminal.NewInfo()
		Expect(info).NotTo(BeNil())
	})

	It("initializes with zero dimensions", func() {
		info := terminal.NewInfo()
		Expect(info.Width).To(Equal(0))
		Expect(info.Height).To(Equal(0))
	})

	It("initializes with IsValid false", func() {
		info := terminal.NewInfo()
		Expect(info.IsValid).To(BeFalse())
	})
})

var _ = Describe("Info.Update", func() {
	var info *terminal.Info

	BeforeEach(func() {
		info = terminal.NewInfo()
	})

	It("sets width from WindowSizeMsg", func() {
		info.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		Expect(info.Width).To(Equal(120))
	})

	It("sets height from WindowSizeMsg", func() {
		info.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		Expect(info.Height).To(Equal(40))
	})

	It("sets IsValid to true", func() {
		info.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		Expect(info.IsValid).To(BeTrue())
	})
})

var _ = Describe("Info.GetCategory", func() {
	var info *terminal.Info

	BeforeEach(func() {
		info = terminal.NewInfo()
	})

	Context("when Info is invalid", func() {
		It("returns SizeNormal", func() {
			category := info.GetCategory()
			Expect(category).To(Equal(terminal.SizeNormal))
		})
	})

	Context("when terminal width is less than 60", func() {
		BeforeEach(func() {
			info.Update(tea.WindowSizeMsg{Width: 50, Height: 24})
		})

		It("returns SizeTiny", func() {
			Expect(info.GetCategory()).To(Equal(terminal.SizeTiny))
		})
	})

	Context("when terminal width is 60-79", func() {
		BeforeEach(func() {
			info.Update(tea.WindowSizeMsg{Width: 70, Height: 24})
		})

		It("returns SizeCompact", func() {
			Expect(info.GetCategory()).To(Equal(terminal.SizeCompact))
		})
	})

	Context("when terminal width is 80-119", func() {
		BeforeEach(func() {
			info.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
		})

		It("returns SizeNormal", func() {
			Expect(info.GetCategory()).To(Equal(terminal.SizeNormal))
		})
	})

	Context("when terminal width is 120-159", func() {
		BeforeEach(func() {
			info.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		})

		It("returns SizeLarge", func() {
			Expect(info.GetCategory()).To(Equal(terminal.SizeLarge))
		})
	})

	Context("when terminal width is 160 or more", func() {
		BeforeEach(func() {
			info.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
		})

		It("returns SizeXLarge", func() {
			Expect(info.GetCategory()).To(Equal(terminal.SizeXLarge))
		})
	})
})

var _ = Describe("Info.GetSafeDimensions", func() {
	var info *terminal.Info

	BeforeEach(func() {
		info = terminal.NewInfo()
	})

	Context("when Info is invalid", func() {
		It("returns DefaultConfig dimensions", func() {
			w, h := info.GetSafeDimensions(terminal.DefaultConfig)
			Expect(w).To(Equal(terminal.DefaultConfig.DefaultWidth))
			Expect(h).To(Equal(terminal.DefaultConfig.DefaultHeight))
		})
	})

	Context("when Info is valid with sufficient dimensions", func() {
		BeforeEach(func() {
			info.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		})

		It("returns actual dimensions", func() {
			w, h := info.GetSafeDimensions(terminal.DefaultConfig)
			Expect(w).To(Equal(120))
			Expect(h).To(Equal(40))
		})
	})

	Context("when terminal dimensions are below minimum", func() {
		BeforeEach(func() {
			info.Update(tea.WindowSizeMsg{Width: 20, Height: 10})
		})

		It("enforces minimum width", func() {
			w, _ := info.GetSafeDimensions(terminal.DefaultConfig)
			Expect(w).To(Equal(terminal.DefaultConfig.MinWidth))
		})

		It("enforces minimum height", func() {
			_, h := info.GetSafeDimensions(terminal.DefaultConfig)
			Expect(h).To(Equal(terminal.DefaultConfig.MinHeight))
		})
	})
})

var _ = Describe("Info.CanRender", func() {
	var info *terminal.Info

	BeforeEach(func() {
		info = terminal.NewInfo()
	})

	Context("when Info is invalid", func() {
		It("returns true", func() {
			Expect(info.CanRender(terminal.DefaultConfig)).To(BeTrue())
		})
	})

	Context("when terminal dimensions meet minimum", func() {
		BeforeEach(func() {
			info.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		})

		It("returns true", func() {
			Expect(info.CanRender(terminal.DefaultConfig)).To(BeTrue())
		})
	})

	Context("when terminal width is below minimum", func() {
		BeforeEach(func() {
			info.Update(tea.WindowSizeMsg{Width: 30, Height: 24})
		})

		It("returns false", func() {
			Expect(info.CanRender(terminal.DefaultConfig)).To(BeFalse())
		})
	})

	Context("when terminal height is below minimum", func() {
		BeforeEach(func() {
			info.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
		})

		It("returns false", func() {
			Expect(info.CanRender(terminal.DefaultConfig)).To(BeFalse())
		})
	})
})

var _ = Describe("Info.ContentArea", func() {
	var info *terminal.Info

	BeforeEach(func() {
		info = terminal.NewInfo()
		info.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	})

	It("subtracts margins from dimensions", func() {
		margins := terminal.Margins{Top: 2, Bottom: 2, Left: 1, Right: 1}
		w, h := info.ContentArea(margins)
		expectedW := 120 - 1 - 1
		expectedH := 40 - 2 - 2
		Expect(w).To(Equal(expectedW))
		Expect(h).To(Equal(expectedH))
	})

	It("enforces minimum content width", func() {
		margins := terminal.Margins{Top: 0, Bottom: 0, Left: 50, Right: 50}
		w, _ := info.ContentArea(margins)
		Expect(w).To(Equal(20))
	})

	It("enforces minimum content height", func() {
		margins := terminal.Margins{Top: 30, Bottom: 30, Left: 0, Right: 0}
		_, h := info.ContentArea(margins)
		Expect(h).To(Equal(5))
	})

	Context("when info is invalid", func() {
		BeforeEach(func() {
			info = terminal.NewInfo()
		})

		It("uses default config dimensions", func() {
			margins := terminal.Margins{Top: 2, Bottom: 2, Left: 1, Right: 1}
			w, h := info.ContentArea(margins)
			expectedW := terminal.DefaultConfig.DefaultWidth - 1 - 1
			expectedH := terminal.DefaultConfig.DefaultHeight - 2 - 2
			Expect(w).To(Equal(expectedW))
			Expect(h).To(Equal(expectedH))
		})
	})
})
