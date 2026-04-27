package widgets_test

import (
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
)

var _ = Describe("Picker", func() {
	Describe("Filtered", func() {
		It("returns all items when filter is empty", func() {
			p := newPickerWith(threeItems())
			Expect(p.Filtered()).To(HaveLen(3))
		})

		It("returns only matching items when filter is set", func() {
			p := newPickerWith(threeItems())
			p.SetFilter("cle")
			filtered := p.Filtered()
			Expect(filtered).To(HaveLen(1))
			Expect(filtered[0].Label).To(Equal("/clear"))
		})

		It("matches case-insensitively", func() {
			p := newPickerWith(threeItems())
			p.SetFilter("HE")
			filtered := p.Filtered()
			Expect(filtered).To(HaveLen(1))
			Expect(filtered[0].Label).To(Equal("/help"))
		})

		It("returns nothing when no items match", func() {
			p := newPickerWith(threeItems())
			p.SetFilter("zzzz")
			Expect(p.Filtered()).To(BeEmpty())
		})
	})

	Describe("cursor bounds", func() {
		It("clamps cursor at the last filtered item on KeyDown", func() {
			p := newPickerWith(threeItems())
			pressKey(p, tea.KeyDown, 10)
			Expect(p.Cursor()).To(Equal(2))
		})

		It("clamps cursor at zero on KeyUp", func() {
			p := newPickerWith(threeItems())
			pressKey(p, tea.KeyUp, 5)
			Expect(p.Cursor()).To(Equal(0))
		})

		It("resets cursor when filter shrinks the matched list", func() {
			p := newPickerWith(threeItems())
			pressKey(p, tea.KeyDown, 2)
			Expect(p.Cursor()).To(Equal(2))

			p.SetFilter("clear")
			Expect(p.Cursor()).To(Equal(0))
		})
	})

	Describe("viewport offset", func() {
		It("advances offset when cursor crosses the visible bottom", func() {
			p := newPickerWith(manyItems(8))
			p.SetMaxVisible(3)
			pressKey(p, tea.KeyDown, 7)
			Expect(p.Offset()).To(BeNumerically(">", 0))
			Expect(p.Cursor() - p.Offset()).To(BeNumerically("<", 3))
		})

		It("decreases offset when cursor crosses the visible top", func() {
			p := newPickerWith(manyItems(8))
			p.SetMaxVisible(3)
			pressKey(p, tea.KeyDown, 7)
			beforeOffset := p.Offset()

			pressKey(p, tea.KeyUp, beforeOffset+1)
			Expect(p.Offset()).To(BeNumerically("<", beforeOffset))
		})

		It("keeps offset at zero when items fit in the viewport", func() {
			p := newPickerWith(manyItems(2))
			p.SetMaxVisible(5)
			pressKey(p, tea.KeyDown, 1)
			Expect(p.Offset()).To(Equal(0))
		})
	})

	Describe("Selected", func() {
		It("returns the cursor's item", func() {
			p := newPickerWith(threeItems())
			pressKey(p, tea.KeyDown, 1)
			sel := p.Selected()
			Expect(sel).NotTo(BeNil())
			Expect(sel.Label).To(Equal("/clear"))
		})

		It("returns nil when no items match", func() {
			p := newPickerWith(threeItems())
			p.SetFilter("zzz")
			Expect(p.Selected()).To(BeNil())
		})
	})

	Describe("Update events", func() {
		It("emits EventSelect on Enter", func() {
			p := newPickerWith(threeItems())
			_, ev := p.Update(tea.KeyMsg{Type: tea.KeyEnter})
			Expect(ev.Type).To(Equal(widgets.EventSelect))
			Expect(ev.Item.Label).To(Equal("/help"))
		})

		It("emits EventSelect on Tab", func() {
			p := newPickerWith(threeItems())
			_, ev := p.Update(tea.KeyMsg{Type: tea.KeyTab})
			Expect(ev.Type).To(Equal(widgets.EventSelect))
		})

		It("emits EventCancel on Esc", func() {
			p := newPickerWith(threeItems())
			_, ev := p.Update(tea.KeyMsg{Type: tea.KeyEsc})
			Expect(ev.Type).To(Equal(widgets.EventCancel))
		})

		It("emits EventNone on Up/Down", func() {
			p := newPickerWith(threeItems())
			_, ev := p.Update(tea.KeyMsg{Type: tea.KeyDown})
			Expect(ev.Type).To(Equal(widgets.EventNone))
		})

		It("emits EventNone when Enter pressed with no matches", func() {
			p := newPickerWith(threeItems())
			p.SetFilter("zzz")
			_, ev := p.Update(tea.KeyMsg{Type: tea.KeyEnter})
			Expect(ev.Type).To(Equal(widgets.EventNone))
		})
	})

	Describe("View", func() {
		It("renders all rows when items fit", func() {
			p := newPickerWith(threeItems())
			view := p.View()
			Expect(view).To(ContainSubstring("/help"))
			Expect(view).To(ContainSubstring("/clear"))
			Expect(view).To(ContainSubstring("/exit"))
		})

		It("renders an empty placeholder when no items match", func() {
			p := newPickerWith(threeItems())
			p.SetFilter("zzzz")
			Expect(p.View()).To(ContainSubstring("no matches"))
		})
	})
})

func newPickerWith(items []widgets.Item) *widgets.Picker {
	return widgets.NewPicker(items)
}

func threeItems() []widgets.Item {
	return []widgets.Item{
		{Label: "/help", Description: "Show help", Value: "help"},
		{Label: "/clear", Description: "Wipe chat buffer", Value: "clear"},
		{Label: "/exit", Description: "Quit the TUI", Value: "exit"},
	}
}

func manyItems(n int) []widgets.Item {
	items := make([]widgets.Item, n)
	for idx := range items {
		items[idx] = widgets.Item{
			Label: itemLabel(idx),
			Value: idx,
		}
	}
	return items
}

func itemLabel(idx int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	if idx < len(letters) {
		return "/cmd-" + string(letters[idx])
	}
	return "/cmd-" + string(letters[idx%len(letters)])
}

func pressKey(p *widgets.Picker, kind tea.KeyType, n int) {
	for k := 0; k < n; k++ {
		p.Update(tea.KeyMsg{Type: kind})
	}
}
