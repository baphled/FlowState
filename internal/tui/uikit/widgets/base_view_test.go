package widgets_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
)

var _ = Describe("BaseView", func() {
	var bv *widgets.BaseView

	BeforeEach(func() {
		bv = &widgets.BaseView{}
	})

	Describe("SetTerminalInfo / GetTerminalWidth / GetTerminalHeight", func() {
		It("stores and retrieves terminal dimensions", func() {
			bv.SetTerminalInfo(120, 40)
			Expect(bv.GetTerminalWidth()).To(Equal(120))
			Expect(bv.GetTerminalHeight()).To(Equal(40))
		})
	})

	Describe("SetTheme / GetTheme", func() {
		It("stores and retrieves the theme", func() {
			bv.SetTheme("dark")
			Expect(bv.GetTheme()).To(Equal("dark"))
		})
	})

	Describe("SetLogo / GetLogo / GetLogoSpacing", func() {
		It("stores and retrieves the logo and spacing", func() {
			bv.SetLogo("KaRiya", 2)
			Expect(bv.GetLogo()).To(Equal("KaRiya"))
			Expect(bv.GetLogoSpacing()).To(Equal(2))
		})
	})

	Describe("zero values", func() {
		It("returns zero values before any setter is called", func() {
			Expect(bv.GetTerminalWidth()).To(Equal(0))
			Expect(bv.GetTerminalHeight()).To(Equal(0))
			Expect(bv.GetTheme()).To(BeNil())
			Expect(bv.GetLogo()).To(BeNil())
			Expect(bv.GetLogoSpacing()).To(Equal(0))
		})
	})
})
