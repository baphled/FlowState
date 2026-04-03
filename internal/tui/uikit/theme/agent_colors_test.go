package theme_test

import (
	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/charmbracelet/lipgloss"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("agent colour resolution", func() {
	It("uses the manifest colour when it is a valid hex value", func() {
		manifest := agent.Manifest{Color: "#112233"}

		resolved := theme.ResolveAgentColor(manifest, 3, theme.Default())

		Expect(resolved).To(Equal(lipgloss.Color("#112233")))
	})

	It("falls back to the palette when the manifest colour is invalid", func() {
		manifest := agent.Manifest{Color: "teal"}

		resolved := theme.ResolveAgentColor(manifest, 2, theme.Default())

		Expect(resolved).To(Equal(theme.AgentColorPalette[2]))
	})

	It("falls back to the palette when the manifest colour is empty", func() {
		manifest := agent.Manifest{}

		resolved := theme.ResolveAgentColor(manifest, 1, theme.Default())

		Expect(resolved).To(Equal(theme.AgentColorPalette[1]))
	})

	It("cycles through the palette by index", func() {
		manifest := agent.Manifest{}

		resolved := theme.ResolveAgentColor(manifest, len(theme.AgentColorPalette), theme.Default())

		Expect(resolved).To(Equal(theme.AgentColorPalette[0]))
	})

	It("exposes the default palette colours in order", func() {
		Expect(theme.AgentColorPalette).To(Equal([]lipgloss.Color{
			lipgloss.Color("#6cb56c"),
			lipgloss.Color("#a99bd1"),
			lipgloss.Color("#6cb56c"),
			lipgloss.Color("#d9a66c"),
			lipgloss.Color("#5fb3b3"),
			lipgloss.Color("#d76e6e"),
			lipgloss.Color("#6ab0d3"),
		}))
	})
})
