package chat

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// AgentAttributionRenderer manages the styling and rendering of agent identity badges.
type AgentAttributionRenderer struct {
	styles map[string]lipgloss.Style
}

// NewAgentAttributionRenderer creates a renderer with role-specific styles.
//
// Expected:
//   - None.
//
// Returns:
//   - A renderer preloaded with role-specific badge styles.
//
// Side effects:
//   - None.
func NewAgentAttributionRenderer() *AgentAttributionRenderer {
	return &AgentAttributionRenderer{
		styles: map[string]lipgloss.Style{
			"planner":     lipgloss.NewStyle().Foreground(lipgloss.Color("#5fb3b3")).Bold(true),
			"executor":    lipgloss.NewStyle().Foreground(lipgloss.Color("#d9a66c")).Bold(true),
			"researcher":  lipgloss.NewStyle().Foreground(lipgloss.Color("#6cb56c")).Bold(true),
			"reviewer":    lipgloss.NewStyle().Foreground(lipgloss.Color("#a99bd1")).Bold(true),
			"coordinator": lipgloss.NewStyle().Foreground(lipgloss.Color("#6ab0d3")).Bold(true),
			"system":      lipgloss.NewStyle().Foreground(lipgloss.Color("#5e6673")).Italic(true),
		},
	}
}

// RenderBadge returns a styled badge string for the given agent role/ID.
//
// Expected:
//   - agentID identifies the agent whose badge should be rendered.
//
// Returns:
//   - A styled badge string for the supplied agent identity.
//
// Side effects:
//   - None.
func (r *AgentAttributionRenderer) RenderBadge(agentID string) string {
	role := strings.ToLower(agentID)
	style, ok := r.styles[role]
	if !ok {
		if strings.Contains(role, "plan") {
			style = r.styles["planner"]
		} else if strings.Contains(role, "exec") {
			style = r.styles["executor"]
		} else {
			style = lipgloss.NewStyle().Bold(true)
		}
	}

	display := cases.Title(language.English).String(agentID)
	return style.Render("[" + display + "]")
}

// DecorateMessage prefixes the content with the agent badge.
//
// Expected:
//   - agentID identifies the agent supplying the message.
//   - content contains the message body to decorate.
//
// Returns:
//   - The badge-prefixed message content.
//
// Side effects:
//   - None.
func (r *AgentAttributionRenderer) DecorateMessage(agentID, content string) string {
	badge := r.RenderBadge(agentID)
	return badge + " " + content
}
