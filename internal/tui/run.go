package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/agent"
	flowapp "github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tui/app"
	"github.com/baphled/flowstate/internal/tui/intents"
	"github.com/baphled/flowstate/internal/tui/intents/agentpicker"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

// publishResumedEvent publishes a session.resumed event on bus when sessionID is non-empty.
//
// Expected:
//   - bus may be nil; when nil the call is a no-op.
//   - sessionID is the identifier of the session being resumed.
//
// Returns:
//   - None.
//
// Side effects:
//   - Publishes events.EventSessionResumed on bus when both bus and sessionID are non-nil/non-empty.
func publishResumedEvent(bus *eventbus.EventBus, sessionID string) {
	if bus == nil || sessionID == "" {
		return
	}
	bus.Publish(events.EventSessionResumed, events.NewSessionResumedEvent(events.SessionResumedEventData{
		SessionID: sessionID,
	}))
}

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
//   - Publishes session.resumed on the engine EventBus when sessionID is non-empty.
func Run(application *flowapp.App, agentID string, sessionID string) error {
	if agentID != "" && application.Registry != nil {
		if manifest, ok := application.Registry.Get(agentID); ok {
			application.Engine.SetManifest(*manifest)
		}
	}

	publishResumedEvent(application.Engine.EventBus(), sessionID)

	if mgr := application.SessionMgr(); mgr != nil {
		mgr.RegisterSession(sessionID, agentID)
	}

	persistRootSessionMetadata(application.SessionsDir(), sessionID, agentID)

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
		// Refresh the sidecar so the persisted agent_id matches the
		// picked agent. Run's pre-launch write carried the empty agent
		// that sent us down the picker branch in the first place.
		persistRootSessionMetadata(application.SessionsDir(), sessionID, selectedID)
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
	if orch := application.CompletionOrchestrator(); orch != nil {
		chatIntent.SetCompletionOrchestrator(orch)
		defer orch.UnsubscribeRePrompt(sessionID)
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
		SessionManager:     application.SessionMgr(),
		AgentID:            agentID,
		SessionID:          sessionID,
		ModelName:          application.Engine.LastModel(),
		ProviderName:       application.Engine.LastProvider(),
		TokenBudget:        application.Engine.ModelContextLimit(),
		AgentRegistry:      application.Registry,
		SwarmRegistry:      application.SwarmRegistry,
		SessionStore:       application.Sessions,
		ModelResolver:      application.Engine.FailoverManager(),
		ChildSessionLister: application.SessionMgr(),
		PlanStore:          application.Store,
	})
}

// persistRootSessionMetadata writes a <sessionID>.meta.json sidecar so
// the App.restorePersistedSessions path can rebuild the session
// hierarchy graph after a restart. Without the sidecar, root TUI
// sessions survive as message files but vanish from the in-memory
// Manager, and Manager.ChildSessions returns nothing for them — which
// is why the Session Browser shows orphaned children after a restart.
//
// Matches the engine-side convention from
// DelegateTool.persistSessionMetadata: empty sessionsDir disables
// persistence silently, and write failures are swallowed so
// persistence never blocks TUI launch.
//
// Expected:
//   - sessionsDir may be empty (disables persistence silently).
//   - sessionID is the root session identifier; empty is a no-op.
//   - agentID is the agent that owns the session (persisted verbatim).
//     The agent-picker path calls this helper a second time once the
//     user selects an agent so the sidecar reflects the final choice.
//
// Returns:
//   - None.
//
// Side effects:
//   - Writes <sessionsDir>/<sessionID>.meta.json when both inputs are
//     non-empty and the write succeeds.
func persistRootSessionMetadata(sessionsDir, sessionID, agentID string) {
	if sessionsDir == "" || sessionID == "" {
		return
	}
	sess := &session.Session{
		ID:        sessionID,
		AgentID:   agentID,
		Status:    string(session.StatusActive),
		CreatedAt: time.Now(),
	}
	// Swallow the error: persistence must never block TUI launch.
	// Matches DelegateTool.persistSessionMetadata.
	if err := session.PersistSession(sessionsDir, sess); err != nil {
		return
	}
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
