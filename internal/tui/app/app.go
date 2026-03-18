package app

import tea "github.com/charmbracelet/bubbletea"

// App is the root Bubble Tea model for FlowState TUI.
type App struct {
	intent Intent
	width  int
	height int
}

// New creates an App with the given initial intent.
//
// Expected:
//   - intent is a non-nil Intent implementation.
//
// Returns:
//   - An initialised App with the given intent.
//
// Side effects:
//   - None.
func New(intent Intent) *App {
	return &App{intent: intent}
}

// Init delegates to the active intent.
//
// Returns:
//   - A tea.Cmd from the active intent's Init method.
//
// Side effects:
//   - Delegates to the active intent's Init method.
func (a *App) Init() tea.Cmd {
	return a.intent.Init()
}

// Update handles messages by delegating to the active intent.
//
// Expected:
//   - msg is a tea.Msg from the Bubble Tea event loop.
//
// Returns:
//   - The App model and a tea.Cmd from the active intent's Update method.
//
// Side effects:
//   - Updates terminal dimensions on WindowSizeMsg.
//   - Delegates to the active intent's Update method.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if wsm, ok := msg.(tea.WindowSizeMsg); ok {
		a.width = wsm.Width
		a.height = wsm.Height
	}
	return a, a.intent.Update(msg)
}

// View delegates to the active intent.
//
// Returns:
//   - The rendered view string from the active intent.
//
// Side effects:
//   - None.
func (a *App) View() string {
	return a.intent.View()
}

// SetIntent switches to a new intent.
//
// Expected:
//   - intent is a non-nil Intent implementation.
//
// Side effects:
//   - Replaces the active intent.
func (a *App) SetIntent(intent Intent) {
	a.intent = intent
}

// Width returns the current terminal width.
//
// Returns:
//   - The current terminal width in columns.
//
// Side effects:
//   - None.
func (a *App) Width() int {
	return a.width
}

// Height returns the current terminal height.
//
// Returns:
//   - The current terminal height in rows.
//
// Side effects:
//   - None.
func (a *App) Height() int {
	return a.height
}
