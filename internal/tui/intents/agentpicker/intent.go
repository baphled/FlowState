// Package agentpicker provides a TUI intent for selecting an agent from a list.
package agentpicker

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/tui/intents"
)

// AgentEntry represents an available agent.
type AgentEntry struct {
	ID   string
	Name string
}

// IntentConfig holds configuration for AgentPickerIntent.
type IntentConfig struct {
	Agents   []AgentEntry
	OnSelect func(agentID string)
}

// Intent allows users to select an agent from a list.
type Intent struct {
	agents        []AgentEntry
	selectedAgent int
	result        *intents.IntentResult
	onSelect      func(agentID string)
}

// NewIntent creates a new agent picker intent from the given configuration.
//
// Expected:
//   - cfg is a fully populated IntentConfig with a valid agent list.
//
// Returns:
//   - An initialised Intent with selection at first agent.
//
// Side effects:
//   - None.
func NewIntent(cfg IntentConfig) *Intent {
	return &Intent{
		agents:        cfg.Agents,
		selectedAgent: 0,
		result:        nil,
		onSelect:      cfg.OnSelect,
	}
}

// Init initialises the agent picker intent.
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
//   - A tea.Cmd, or nil when no command is needed.
//
// Side effects:
//   - Mutates intent state based on the message type.
func (i *Intent) Update(msg tea.Msg) tea.Cmd {
	keyMsg, ok := msg.(tea.KeyMsg)
	if ok {
		return i.handleKeyMsg(keyMsg)
	}
	return nil
}

// handleKeyMsg processes keyboard input for navigation and selection.
//
// Expected:
//   - msg is a valid tea.KeyMsg from the Bubble Tea event loop.
//
// Returns:
//   - A tea.Cmd, or nil when no command is needed.
//
// Side effects:
//   - Mutates selection index and result state based on the key pressed.
func (i *Intent) handleKeyMsg(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyUp:
		if i.selectedAgent > 0 {
			i.selectedAgent--
		}
		return nil
	case tea.KeyDown:
		if i.selectedAgent < len(i.agents)-1 {
			i.selectedAgent++
		}
		return nil
	case tea.KeyEnter:
		if i.selectedAgent < len(i.agents) {
			selectedID := i.agents[i.selectedAgent].ID
			i.result = &intents.IntentResult{
				Data: selectedID,
			}
			if i.onSelect != nil {
				i.onSelect(selectedID)
			}
		}
		return dismissModal
	case tea.KeyEsc, tea.KeyCtrlC:
		i.result = nil
		return dismissModal
	}
	return nil
}

// dismissModal yields the message the parent app loop reads to unmount
// the modal overlay. Identical pattern to models.Intent's navigateBack
// helper; defined at package scope because the picker has no use for
// a closure over receiver state on dismiss.
func dismissModal() tea.Msg {
	return intents.DismissModalMsg{}
}

// View renders the agent selection interface.
//
// Returns:
//   - A string containing the rendered agent list.
//
// Side effects:
//   - None.
func (i *Intent) View() string {
	return i.renderContent()
}

// renderContent renders the list of agents.
//
// Returns:
//   - A string containing the rendered agent list.
//
// Side effects:
//   - None.
func (i *Intent) renderContent() string {
	var lines []string
	for idx, agent := range i.agents {
		prefix := "  "
		if idx == i.selectedAgent {
			prefix = "> "
		}
		lines = append(lines, prefix+agent.Name)
	}

	var buf strings.Builder
	for _, line := range lines {
		buf.WriteString(line)
		buf.WriteString("\n")
	}
	return buf.String()
}

// Result returns the intent result.
//
// Returns:
//   - The IntentResult if an agent was selected or escape was pressed.
//   - nil if no action has been taken yet.
//
// Side effects:
//   - None.
func (i *Intent) Result() *intents.IntentResult {
	return i.result
}

// SelectedAgent returns the index of the currently selected agent.
//
// Returns:
//   - An int representing the selected agent index.
//
// Side effects:
//   - None.
func (i *Intent) SelectedAgent() int {
	return i.selectedAgent
}

// SetOnSelect replaces the callback invoked when the user confirms an agent selection.
//
// Expected:
//   - fn is a non-nil function accepting the selected agent ID.
//
// Returns:
//   - None.
//
// Side effects:
//   - Replaces i.onSelect.
func (i *Intent) SetOnSelect(fn func(agentID string)) {
	i.onSelect = fn
}
