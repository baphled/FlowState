package models

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/provider"
	tuiintents "github.com/baphled/flowstate/internal/tui/intents"
	"github.com/baphled/flowstate/internal/tui/intents/providersetup"
	"github.com/baphled/flowstate/internal/tui/uikit/layout"
	"github.com/baphled/flowstate/internal/ui/terminal"
)

const (
	helpText = "↑↓ navigate  ·  Enter select  ·  a add provider  ·  Esc cancel"
)

// ProviderRegistry provides access to registered AI providers.
type ProviderRegistry interface {
	// List returns the names of all registered providers.
	List() []string
	// Get returns the provider with the given name.
	Get(name string) (provider.Provider, error)
}

// ModelGroup represents a collapsible group of models from a single provider.
type ModelGroup struct {
	ProviderName string
	Models       []ModelItem
	Expanded     bool
}

// ModelItem represents an individual AI model.
type ModelItem struct {
	ID            string
	ContextLength int
}

// AppShell abstracts app methods needed by ModelSelectorIntent.
type AppShell interface {
	// WriteConfig persists the given application configuration.
	WriteConfig(cfg *config.AppConfig) error
	// List returns the names of all registered providers.
	List() []string
	// Get returns the provider with the given name.
	Get(name string) (provider.Provider, error)
}

// IntentConfig holds configuration for ModelSelectorIntent.
type IntentConfig struct {
	AppShell         AppShell
	ProviderRegistry ProviderRegistry
	OnSelect         func(provider, model string)
	ReturnTo         tuiintents.Intent
}

// Intent allows users to select an AI model from registered providers.
type Intent struct {
	groups        []ModelGroup
	selectedGroup int
	selectedModel int
	expanded      bool
	width         int
	height        int
	onSelect      func(provider, model string)
	returnTo      tuiintents.Intent
	appShell      AppShell
}

// NewIntent creates a new model selector intent from the given configuration.
//
// Expected:
//   - cfg is a fully populated IntentConfig with a valid ProviderRegistry.
//
// Returns:
//   - An initialised Intent with default dimensions (80x24) and no group expanded.
//
// Side effects:
//   - None.
func NewIntent(cfg IntentConfig) *Intent {
	groups := buildGroups(cfg.ProviderRegistry)
	return &Intent{
		groups:        groups,
		selectedGroup: 0,
		selectedModel: 0,
		expanded:      false,
		width:         80,
		height:        24,
		onSelect:      cfg.OnSelect,
		returnTo:      cfg.ReturnTo,
		appShell:      cfg.AppShell,
	}
}

// buildGroups constructs model groups from the registered providers.
//
// Expected:
//   - registry is a non-nil ProviderRegistry with zero or more providers.
//
// Returns:
//   - A slice of ModelGroup, one per provider that returns models without error.
//
// Side effects:
//   - None.
func buildGroups(registry ProviderRegistry) []ModelGroup {
	names := registry.List()
	groups := make([]ModelGroup, 0, len(names))
	for _, name := range names {
		p, err := registry.Get(name)
		if err != nil {
			continue
		}
		models, err := p.Models()
		if err != nil {
			continue
		}
		items := make([]ModelItem, 0, len(models))
		for _, m := range models {
			items = append(items, ModelItem{
				ID:            m.ID,
				ContextLength: m.ContextLength,
			})
		}
		groups = append(groups, ModelGroup{
			ProviderName: name,
			Models:       items,
			Expanded:     false,
		})
	}
	return groups
}

// Init initialises the model selector intent.
//
// Returns:
//   - nil.
//
// Side effects:
//   - None.
func (i *Intent) Init() tea.Cmd {
	return nil
}

// Update handles messages from the Bubble Tea event loop.
//
// Expected:
//   - msg is a valid tea.Msg, typically a tea.KeyMsg or tea.WindowSizeMsg.
//
// Returns:
//   - A tea.Cmd for intent switching, or nil when no command is needed.
//
// Side effects:
//   - Mutates intent state based on the message type.
func (i *Intent) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return i.handleKeyMsg(msg)
	case tea.WindowSizeMsg:
		i.width = msg.Width
		i.height = msg.Height
	}
	return nil
}

// openProviderSetup creates and shows the provider setup as a modal overlay.
//
// Returns:
//   - A tea.Cmd that emits a ShowModalMsg to display the provider setup.
//
// Side effects:
//   - None.
func (i *Intent) openProviderSetup() tea.Cmd {
	return func() tea.Msg {
		provSetup := providersetup.NewIntent(providersetup.IntentConfig{
			Shell:      i.appShell,
			Config:     nil,
			MCPServers: nil,
		})
		return tuiintents.ShowModalMsg{Modal: provSetup}
	}
}

// navigateBack returns a command to dismiss the modal overlay.
//
// Returns:
//   - A tea.Cmd that emits a DismissModalMsg to close the modal.
//
// Side effects:
//   - None.
func (i *Intent) navigateBack() tea.Cmd {
	return func() tea.Msg { return tuiintents.DismissModalMsg{} }
}

// handleKeyMsg processes keyboard input for navigation and selection.
//
// Expected:
//   - msg is a valid tea.KeyMsg from the Bubble Tea event loop.
//
// Returns:
//   - A tea.Cmd for intent switching or provider setup, or nil when no command is needed.
//
// Side effects:
//   - Mutates selection indices, expanded state, and requestsSetup flag based on the key pressed.
func (i *Intent) handleKeyMsg(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyUp:
		if i.expanded {
			if i.selectedModel > 0 {
				i.selectedModel--
			}
		} else if i.selectedGroup > 0 {
			i.selectedGroup--
		}
		return nil
	case tea.KeyDown:
		if i.expanded {
			maxModel := len(i.groups[i.selectedGroup].Models)
			if i.selectedModel < maxModel {
				i.selectedModel++
			}
		} else if i.selectedGroup < len(i.groups)-1 {
			i.selectedGroup++
		}
		return nil
	case tea.KeyEnter:
		return i.handleEnter()
	case tea.KeyEsc:
		return i.navigateBack()
	case tea.KeyRunes:
		if len(msg.Runes) == 1 && msg.Runes[0] == 'a' {
			return i.openProviderSetup()
		}
	}
	return nil
}

// handleEnter processes the enter key for group expansion and model selection.
//
// Returns:
//   - A tea.Cmd to navigate back after model selection, or nil when expanding or collapsing a group.
//
// Side effects:
//   - Toggles group expansion state and invokes the onSelect callback when a model is chosen.
func (i *Intent) handleEnter() tea.Cmd {
	if !i.expanded {
		i.expanded = true
		if i.selectedGroup < len(i.groups) {
			i.groups[i.selectedGroup].Expanded = true
		}
		return nil
	}
	hasSelectedModel := i.selectedModel < len(i.groups[i.selectedGroup].Models)
	if hasSelectedModel {
		model := i.groups[i.selectedGroup].Models[i.selectedModel]
		if i.onSelect != nil {
			i.onSelect(i.groups[i.selectedGroup].ProviderName, model.ID)
		}
		return i.navigateBack()
	} else {
		i.expanded = false
		if i.selectedGroup < len(i.groups) {
			i.groups[i.selectedGroup].Expanded = false
		}
	}
	return nil
}

// View renders the model selection interface as modal content.
//
// Returns:
//   - A string containing the rendered modal content with group list and help text.
//
// Side effects:
//   - None.
func (i *Intent) View() string {
	content := i.renderContent()
	helpTextStyled := lipgloss.NewStyle().
		Foreground(lipgloss.Color("245")).
		Render(helpText)

	termInfo := &terminal.Info{Width: i.width, Height: i.height}
	sl := layout.NewScreenLayout(termInfo).
		WithBreadcrumbs("Chat", "Select Model").
		WithContent(content).
		WithHelp(helpTextStyled).
		WithFooterSeparator(true)

	return sl.Render()
}

// renderContent extracts the content rendering logic for the view.
//
// Returns:
//   - A string containing the rendered group list.
//
// Side effects:
//   - None.
func (i *Intent) renderContent() string {
	return renderGroupList(i.groups, i.selectedGroup, i.selectedModel, i.width)
}

// renderGroupList renders all provider groups as a vertical list.
//
// Expected:
//   - groups is a slice of ModelGroup to render.
//   - selectedGroup and selectedModel are valid indices for the current selection.
//
// Returns:
//   - A string containing the rendered group list, or a placeholder if no providers are available.
//
// Side effects:
//   - None.
func renderGroupList(groups []ModelGroup, selectedGroup, selectedModel, _ int) string {
	if len(groups) == 0 {
		return "No providers available"
	}

	var lines []string
	selectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("205")).
		Bold(true)
	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("245"))

	for gIdx, group := range groups {
		isSelected := gIdx == selectedGroup
		lines = append(lines, renderGroupHeader(group, isSelected, selectedStyle))
		if group.Expanded {
			modelLines := renderModelItems(group.Models, isSelected, selectedModel, selectedStyle, dimStyle)
			lines = append(lines, modelLines...)
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// renderGroupHeader renders a single group header with an expansion indicator.
//
// Expected:
//   - group is a valid ModelGroup to render.
//   - isSelected indicates whether this group is currently highlighted.
//   - selectedStyle is the lipgloss style for the selected group.
//
// Returns:
//   - A styled string containing the expansion indicator and provider name.
//
// Side effects:
//   - None.
func renderGroupHeader(group ModelGroup, isSelected bool, selectedStyle lipgloss.Style) string {
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	indicator := "\u25b6"
	if group.Expanded {
		indicator = "\u25bc"
	}
	if isSelected {
		return selectedStyle.Render(fmt.Sprintf("%s [%s]", indicator, group.ProviderName))
	}
	return style.Render(fmt.Sprintf("%s [%s]", indicator, group.ProviderName))
}

// renderModelItems renders the list of models within an expanded group.
//
// Expected:
//   - models is a slice of ModelItem to render.
//   - isGroupSelected indicates whether the parent group is selected.
//   - selectedModel is the index of the currently highlighted model.
//   - selectedStyle and dimStyle are lipgloss styles for highlighted and non-highlighted items.
//
// Returns:
//   - A slice of styled strings, one per model item.
//
// Side effects:
//   - None.
func renderModelItems(models []ModelItem, isGroupSelected bool, selectedModel int,
	selectedStyle lipgloss.Style, dimStyle lipgloss.Style) []string {
	var lines []string
	for mIdx, model := range models {
		indicator := "  "
		if isGroupSelected && mIdx == selectedModel {
			lines = append(lines, selectedStyle.Render(fmt.Sprintf("%s \u203a %s (%d tokens)", indicator, model.ID, model.ContextLength)))
		} else {
			lines = append(lines, dimStyle.Render(fmt.Sprintf("%s   %s (%d tokens)", indicator, model.ID, model.ContextLength)))
		}
	}
	return lines
}

// Result returns the intent result.
//
// Returns:
//   - nil; model selection communicates results via the onSelect callback.
//
// Side effects:
//   - None.
func (i *Intent) Result() *tuiintents.IntentResult {
	return nil
}

// Groups returns the list of model groups.
//
// Returns:
//   - A slice of ModelGroup representing all provider groups.
//
// Side effects:
//   - None.
func (i *Intent) Groups() []ModelGroup {
	return i.groups
}

// SelectedGroup returns the index of the currently selected group.
//
// Returns:
//   - An int representing the selected group index.
//
// Side effects:
//   - None.
func (i *Intent) SelectedGroup() int {
	return i.selectedGroup
}

// SelectedModel returns the index of the currently selected model.
//
// Returns:
//   - An int representing the selected model index within the expanded group.
//
// Side effects:
//   - None.
func (i *Intent) SelectedModel() int {
	return i.selectedModel
}

// IsExpanded returns whether the selected group is expanded.
//
// Returns:
//   - true if a group is currently expanded to show its models.
//
// Side effects:
//   - None.
func (i *Intent) IsExpanded() bool {
	return i.expanded
}

// Expand expands the currently selected group to show its models.
//
// Side effects:
//   - Sets the intent expanded state to true and marks the selected group as expanded.
func (i *Intent) Expand() {
	i.expanded = true
	if i.selectedGroup < len(i.groups) {
		i.groups[i.selectedGroup].Expanded = true
	}
}

// Width returns the current terminal width.
//
// Returns:
//   - An int representing the terminal width in columns.
//
// Side effects:
//   - None.
func (i *Intent) Width() int {
	return i.width
}

// Height returns the current terminal height.
//
// Returns:
//   - An int representing the terminal height in rows.
//
// Side effects:
//   - None.
func (i *Intent) Height() int {
	return i.height
}
