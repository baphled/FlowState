package theme

import (
	"regexp"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/charmbracelet/lipgloss"
)

var agentHexColorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// AgentColorPalette defines the fallback colours used when an agent manifest does not provide a valid colour.
var AgentColorPalette = []lipgloss.Color{
	lipgloss.Color("#6cb56c"),
	lipgloss.Color("#a99bd1"),
	lipgloss.Color("#6cb56c"),
	lipgloss.Color("#d9a66c"),
	lipgloss.Color("#5fb3b3"),
	lipgloss.Color("#d76e6e"),
	lipgloss.Color("#6ab0d3"),
}

// ResolveAgentColor returns the manifest colour when it is valid, or a palette colour otherwise.
//
// Expected:
//   - m may omit Color or contain an invalid hex colour.
//   - index is used to rotate through the fallback palette.
//   - theme is accepted for signature consistency.
//
// Returns:
//   - A lipgloss colour ready for rendering the agent.
//
// Side effects:
//   - None.
func ResolveAgentColor(m agent.Manifest, index int, _ Theme) lipgloss.Color {
	if validHexColor(m.Color) {
		return lipgloss.Color(m.Color)
	}
	return AgentColorPalette[index%len(AgentColorPalette)]
}

// validHexColor reports whether the provided value is a valid six-digit hex colour.
//
// Expected:
//   - value may be empty or contain a colour string.
//
// Returns:
//   - true when value matches the supported hex colour format.
//   - false when value is empty or invalid.
//
// Side effects:
//   - None.
func validHexColor(value string) bool {
	if value == "" {
		return false
	}
	return agentHexColorPattern.MatchString(value)
}
