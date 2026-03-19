package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/tui/app"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

// Run starts the chat TUI with the given engine and agent.
//
// Expected:
//   - eng is a non-nil Engine for handling chat requests.
//   - agentID identifies the agent to converse with.
//   - sessionID is the session identifier for context persistence.
//
// Returns:
//   - An error if the Bubble Tea programme fails to start or run.
//
// Side effects:
//   - Takes over the terminal with an alternate screen for the TUI.
func Run(eng *engine.Engine, agentID string, sessionID string) error {
	chatIntent := chat.NewIntent(chat.IntentConfig{
		Engine:       eng,
		AgentID:      agentID,
		SessionID:    sessionID,
		ModelName:    "default",
		ProviderName: "default",
		TokenBudget:  4096,
	})
	appShell := app.New(chatIntent)
	p := tea.NewProgram(appShell, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
