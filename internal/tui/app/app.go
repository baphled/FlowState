package app

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/provider"
	tuiintents "github.com/baphled/flowstate/internal/tui/intents"
	"github.com/baphled/flowstate/internal/tui/uikit/feedback"
	"github.com/baphled/flowstate/internal/ui/themes"
)

// App is the root Bubble Tea model for FlowState TUI.
type App struct {
	intent           tuiintents.Intent
	modal            tuiintents.Intent // active modal overlay, nil when no modal
	providerRegistry *provider.Registry
	width            int
	height           int
}

// New creates an App with the given initial intent and core app reference.
//
// Expected:
//   - intent is a non-nil Intent implementation.
//   - coreApp is a non-nil reference to the core app for accessing providers.
//
// Returns:
//   - An initialised App with the given intent.
//
// Side effects:
//   - None.
func New(intent tuiintents.Intent, coreApp *app.App) *App {
	return &App{
		intent:           intent,
		providerRegistry: coreApp.ProviderRegistry(),
		width:            80,
		height:           24,
	}
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

// Update handles messages, routing modal overlays and intent switching.
//
// Expected:
//   - msg is a tea.Msg from the Bubble Tea event loop.
//
// Returns:
//   - The App model and a tea.Cmd from the active modal or intent's Update method.
//
// Side effects:
//   - Updates terminal dimensions on WindowSizeMsg.
//   - Handles modal overlay display/dismiss.
//   - Delegates to modal or intent Update method.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if wsm, ok := msg.(tea.WindowSizeMsg); ok {
		a.width = wsm.Width
		a.height = wsm.Height
	}
	if sim, ok := msg.(tuiintents.ShowModalMsg); ok && sim.Modal != nil {
		a.modal = sim.Modal
		cmd := sim.Modal.Init()
		return a, cmd
	}
	if _, ok := msg.(tuiintents.DismissModalMsg); ok {
		a.modal = nil
		return a, nil
	}
	if sim, ok := msg.(tuiintents.SwitchToIntentMsg); ok && sim.Intent != nil {
		a.intent = sim.Intent
		return a, nil
	}
	if a.modal != nil {
		cmd := a.modal.Update(msg)
		return a, cmd
	}
	return a, a.intent.Update(msg)
}

// View renders the app, composing modal overlays if present.
//
// Returns:
//   - The rendered view string, with modal overlay if active.
//
// Side effects:
//   - None.
func (a *App) View() string {
	if a.modal != nil {
		bg := a.intent.View()
		modalContent := a.modal.View()
		return feedback.RenderOverlay(bg, modalContent, a.width, a.height, themes.NewDefaultTheme())
	}
	return a.intent.View()
}

// SetIntent switches to a new intent.
//
// Expected:
//   - intent is a non-nil Intent implementation.
//
// Side effects:
//   - Replaces the active intent.
func (a *App) SetIntent(intent tuiintents.Intent) {
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

// List returns the names of all registered providers.
//
// Returns:
//   - A slice of provider names.
//
// Side effects:
//   - None.
func (a *App) List() []string {
	return a.providerRegistry.List()
}

// Get retrieves a provider by name.
//
// Expected:
//   - name is a non-empty string matching a registered provider.
//
// Returns:
//   - The provider if found.
//   - An error if the provider is not registered.
//
// Side effects:
//   - None.
func (a *App) Get(name string) (provider.Provider, error) {
	return a.providerRegistry.Get(name)
}

// WriteConfig persists the given configuration to the config file.
//
// Expected:
//   - cfg is a non-nil AppConfig.
//
// Returns:
//   - An error if marshalling or writing fails, nil otherwise.
//
// Side effects:
//   - Writes configuration to ~/.config/flowstate/config.yaml.
func (a *App) WriteConfig(cfg *config.AppConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}
	path := filepath.Join(config.Dir(), "config.yaml")
	return os.WriteFile(path, data, 0o600)
}

// NewForTest creates an App for testing without a full core app dependency.
//
// Expected:
//   - intent is a non-nil Intent implementation.
//
// Returns:
//   - An initialised App for testing.
//
// Side effects:
//   - Sets providerRegistry to nil; do not use List/Get on the returned App.
func NewForTest(intent tuiintents.Intent) *App {
	return &App{
		intent:           intent,
		providerRegistry: nil,
		width:            80,
		height:           24,
	}
}
