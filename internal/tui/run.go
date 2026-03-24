package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	flowapp "github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/tui/app"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

// Run starts the FlowState TUI with the given application and session context.
//
// Expected:
//   - application is a fully initialised App with a configured Engine.
//   - agentID and sessionID are non-empty strings identifying the conversation.
//
// Returns:
//   - An error if the Bubble Tea program fails to run, or nil on success.
//
// Side effects:
//   - Launches a full-screen terminal UI that blocks until the user exits.
func Run(application *flowapp.App, agentID string, sessionID string) error {
	if agentID != "" && application.Registry != nil {
		if manifest, ok := application.Registry.Get(agentID); ok {
			application.Engine.SetManifest(*manifest)
		}
	}
	chatIntent := chat.NewIntent(chat.IntentConfig{
		App:           nil,
		Engine:        application.Engine,
		Streamer:      application.Streamer,
		AgentID:       agentID,
		SessionID:     sessionID,
		ModelName:     application.Engine.LastModel(),
		ProviderName:  application.Engine.LastProvider(),
		TokenBudget:   application.Engine.ModelContextLimit(),
		AgentRegistry: application.Registry,
	})
	appShell := app.New(chatIntent, application)
	chatIntent.SetApp(appShell)
	p := tea.NewProgram(appShell, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
