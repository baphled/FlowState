package navigation

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SessionTrail", func() {
	Describe("NewSessionTrail", func() {
		It("creates an empty trail", func() {
			trail := NewSessionTrail()
			Expect(trail).NotTo(BeNil())
			Expect(trail.Items()).To(BeEmpty())
		})
	})

	Describe("WithItems", func() {
		It("returns the trail for chaining", func() {
			trail := NewSessionTrail()
			result := trail.WithItems([]SessionTrailItem{{Label: "root"}})
			Expect(result).To(Equal(trail))
		})

		It("stores the supplied items", func() {
			items := []SessionTrailItem{
				{SessionID: "s1", AgentID: "a1", Label: "root"},
				{SessionID: "s2", AgentID: "a2", Label: "child"},
			}
			trail := NewSessionTrail().WithItems(items)
			Expect(trail.Items()).To(Equal(items))
		})
	})

	Describe("Items", func() {
		It("returns a defensive copy so mutations do not leak", func() {
			items := []SessionTrailItem{{Label: "a"}, {Label: "b"}}
			trail := NewSessionTrail().WithItems(items)

			got := trail.Items()
			got[0].Label = "MUTATED"

			Expect(trail.Items()[0].Label).To(Equal("a"))
		})
	})

	Describe("Render", func() {
		Context("boundary cases", func() {
			It("returns empty string when the trail has no items", func() {
				trail := NewSessionTrail()
				Expect(trail.Render(80)).To(Equal(""))
			})

			It("returns empty string when width is zero or negative", func() {
				trail := NewSessionTrail().WithItems([]SessionTrailItem{{Label: "root"}})
				Expect(trail.Render(0)).To(Equal(""))
				Expect(trail.Render(-10)).To(Equal(""))
			})

			It("renders a single item as the label without any separator", func() {
				trail := NewSessionTrail().WithItems([]SessionTrailItem{{Label: "only"}})
				out := trail.Render(80)
				Expect(out).To(Equal("only"))
				Expect(out).NotTo(ContainSubstring(" > "))
			})
		})

		Context("without truncation", func() {
			It("joins three items with ' > ' separator", func() {
				items := []SessionTrailItem{
					{Label: "a"},
					{Label: "b"},
					{Label: "c"},
				}
				trail := NewSessionTrail().WithItems(items)
				Expect(trail.Render(80)).To(Equal("a > b > c"))
			})

			It("joins five items without ellipsis truncation", func() {
				items := []SessionTrailItem{
					{Label: "one"},
					{Label: "two"},
					{Label: "three"},
					{Label: "four"},
					{Label: "five"},
				}
				trail := NewSessionTrail().WithItems(items)
				Expect(trail.Render(80)).To(Equal("one > two > three > four > five"))
			})
		})

		Context("with ellipsis truncation", func() {
			It("collapses six items into first-2 + ellipsis + last-3", func() {
				items := []SessionTrailItem{
					{Label: "a"}, {Label: "b"}, {Label: "c"},
					{Label: "d"}, {Label: "e"}, {Label: "f"},
				}
				trail := NewSessionTrail().WithItems(items)
				Expect(trail.Render(80)).To(Equal("a > b > … > d > e > f"))
			})

			It("collapses ten items using the same first-2 + ellipsis + last-3 rule", func() {
				items := []SessionTrailItem{
					{Label: "a"}, {Label: "b"}, {Label: "c"},
					{Label: "d"}, {Label: "e"}, {Label: "f"},
					{Label: "g"}, {Label: "h"}, {Label: "i"},
					{Label: "j"},
				}
				trail := NewSessionTrail().WithItems(items)
				Expect(trail.Render(80)).To(Equal("a > b > … > h > i > j"))
			})
		})

		Context("width clamping with long labels", func() {
			It("falls back to per-item truncation when a single item exceeds the width", func() {
				items := []SessionTrailItem{
					{Label: strings.Repeat("x", 50)},
				}
				trail := NewSessionTrail().WithItems(items)
				out := trail.Render(10)
				Expect(lipgloss.Width(out)).To(BeNumerically("<=", 10))
				Expect(out).To(HaveSuffix("…"))
			})

			It("shrinks the rendered output to fit within the requested width", func() {
				items := []SessionTrailItem{
					{Label: strings.Repeat("alpha", 4)},
					{Label: strings.Repeat("beta", 4)},
					{Label: strings.Repeat("gamma", 4)},
				}
				trail := NewSessionTrail().WithItems(items)
				out := trail.Render(30)
				Expect(lipgloss.Width(out)).To(BeNumerically("<=", 30))
			})

			It("still produces output for six items in a narrow width", func() {
				items := []SessionTrailItem{
					{Label: "aaaa"}, {Label: "bbbb"}, {Label: "cccc"},
					{Label: "dddd"}, {Label: "eeee"}, {Label: "ffff"},
				}
				trail := NewSessionTrail().WithItems(items)
				out := trail.Render(25)
				Expect(out).NotTo(BeEmpty())
				Expect(lipgloss.Width(out)).To(BeNumerically("<=", 25))
			})

			It("emits a bare ellipsis when the budget is one column for a single item", func() {
				items := []SessionTrailItem{{Label: strings.Repeat("z", 20)}}
				trail := NewSessionTrail().WithItems(items)
				Expect(trail.Render(1)).To(Equal("…"))
			})

			It("emits a bare ellipsis when the budget is one column for many items", func() {
				items := []SessionTrailItem{
					{Label: "aaaaa"}, {Label: "bbbbb"}, {Label: "ccccc"},
					{Label: "ddddd"}, {Label: "eeeee"}, {Label: "fffff"},
				}
				trail := NewSessionTrail().WithItems(items)
				Expect(trail.Render(1)).To(Equal("…"))
			})
		})
	})
})
