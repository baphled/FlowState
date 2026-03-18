package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/engine"
)

func Run(eng *engine.Engine, agentID string, sessionID string) error {
	m := NewModel(eng, agentID, sessionID)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
