package app

import tea "github.com/charmbracelet/bubbletea"

// App is the root Bubble Tea model for FlowState TUI.
type App struct {
	intent Intent
	width  int
	height int
}

// New creates an App with the given initial intent.
func New(intent Intent) *App {
	return &App{intent: intent}
}

// Init delegates to the active intent.
func (a *App) Init() tea.Cmd {
	return a.intent.Init()
}

// Update handles messages by delegating to the active intent.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if wsm, ok := msg.(tea.WindowSizeMsg); ok {
		a.width = wsm.Width
		a.height = wsm.Height
	}
	return a, a.intent.Update(msg)
}

// View delegates to the active intent.
func (a *App) View() string {
	return a.intent.View()
}

// SetIntent switches to a new intent.
func (a *App) SetIntent(intent Intent) {
	a.intent = intent
}

// Width returns the current terminal width.
func (a *App) Width() int {
	return a.width
}

// Height returns the current terminal height.
func (a *App) Height() int {
	return a.height
}
