package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/agent"
	flowapp "github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tui/app"
	"github.com/baphled/flowstate/internal/tui/intents"
	"github.com/baphled/flowstate/internal/tui/intents/agentpicker"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

// Run starts the FlowState TUI with the given application and session context.
//
// Expected:
//   - application is a fully initialised App with a configured Engine.
//   - agentID identifies the agent to use; if empty the agent picker is shown first.
//   - sessionID identifies the chat session context.
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

	if mgr := application.SessionMgr(); mgr != nil {
		mgr.RegisterSession(sessionID, agentID)
	}

	startIntent := BuildStartIntent(agentID, application.Registry)
	if startIntent != nil {
		return runWithAgentPicker(application, startIntent, sessionID)
	}

	return runWithChatIntent(application, agentID, sessionID)
}

// runWithAgentPicker launches the TUI showing the agent picker first, then
// transitions to chat once the user selects an agent.
//
// Expected:
//   - application is a fully initialised App.
//   - pickerIntent is a non-nil agent picker intent.
//   - sessionID identifies the session context.
//
// Returns:
//   - An error if the Bubble Tea program fails to run, or nil on success.
//
// Side effects:
//   - Launches a full-screen terminal UI that blocks until the user exits.
func runWithAgentPicker(application *flowapp.App, pickerIntent intents.Intent, sessionID string) error {
	var appShell *app.App

	picker, ok := pickerIntent.(*agentpicker.Intent)
	if !ok {
		return runWithChatIntent(application, "", sessionID)
	}

	picker.SetOnSelect(func(selectedID string) {
		if application.Registry != nil {
			if manifest, ok := application.Registry.Get(selectedID); ok {
				application.Engine.SetManifest(*manifest)
			}
		}
		if mgr := application.SessionMgr(); mgr != nil {
			mgr.RegisterSession(sessionID, selectedID)
		}
		chatIntent := newChatIntent(application, selectedID, sessionID)
		if appShell != nil {
			appShell.SetIntent(chatIntent)
			chatIntent.SetApp(appShell)
		}
	})

	appShell = app.New(pickerIntent, application)
	p := tea.NewProgram(appShell, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

// runWithChatIntent launches the TUI directly with the chat intent for the given agent.
//
// Expected:
//   - application is a fully initialised App.
//   - agentID identifies the agent to use.
//   - sessionID identifies the session context.
//
// Returns:
//   - An error if the Bubble Tea program fails to run, or nil on success.
//
// Side effects:
//   - Launches a full-screen terminal UI that blocks until the user exits.
func runWithChatIntent(application *flowapp.App, agentID string, sessionID string) error {
	chatIntent := newChatIntent(application, agentID, sessionID)
	if bgMgr := application.BackgroundManager(); bgMgr != nil {
		ch := make(chan streaming.CompletionNotificationEvent, 64)
		bgMgr.SetCompletionSubscriber(ch)
		chatIntent.SetCompletionChannel(ch)
		chatIntent.SetBackgroundManager(bgMgr)
	}
	appShell := app.New(chatIntent, application)
	chatIntent.SetApp(appShell)
	p := tea.NewProgram(appShell, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

// newChatIntent constructs a chat intent for the given agent and session.
//
// Expected:
//   - application is a fully initialised App.
//   - agentID identifies the agent to use.
//   - sessionID identifies the session context.
//
// Returns:
//   - A fully configured chat.Intent.
//
// Side effects:
//   - Creates a session-scoped streamer.
func newChatIntent(application *flowapp.App, agentID string, sessionID string) *chat.Intent {
	sessionStreamer := streaming.NewSessionContextStreamer(
		application.Streamer,
		func() string { return sessionID },
		session.IDKey{},
	)
	return chat.NewIntent(chat.IntentConfig{
		App:                nil,
		Engine:             application.Engine,
		Streamer:           sessionStreamer,
		AgentID:            agentID,
		SessionID:          sessionID,
		ModelName:          application.Engine.LastModel(),
		ProviderName:       application.Engine.LastProvider(),
		TokenBudget:        application.Engine.ModelContextLimit(),
		AgentRegistry:      application.Registry,
		SessionStore:       application.Sessions,
		ModelResolver:      application.Engine.FailoverManager(),
		ChildSessionLister: application.SessionMgr(),
	})
}

// BuildStartIntent returns an agent picker intent when agentID is empty, so the
// user can select an agent before chat begins. When agentID is provided, it
// returns nil so the caller can start chat directly.
//
// Expected:
//   - agentID may be empty; when empty the picker is shown.
//   - registry may be nil; when nil the picker is shown with an empty agent list.
//
// Returns:
//   - An *agentpicker.Intent when agentID is empty.
//   - nil when agentID is non-empty.
//
// Side effects:
//   - None.
func BuildStartIntent(agentID string, registry *agent.Registry) intents.Intent {
	if agentID != "" {
		return nil
	}

	var entries []agentpicker.AgentEntry
	if registry != nil {
		for _, m := range registry.List() {
			entries = append(entries, agentpicker.AgentEntry{
				ID:   m.ID,
				Name: m.Name,
			})
		}
	}

	return agentpicker.NewIntent(agentpicker.IntentConfig{
		Agents: entries,
	})
}
