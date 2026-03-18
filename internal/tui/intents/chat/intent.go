// Package chat provides the chat intent for FlowState TUI.
package chat

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/tui/uikit/layout"
)

// StreamChunkMsg carries a streaming response chunk to the chat intent.
type StreamChunkMsg struct {
	Content string
	Done    bool
}

// IntentConfig holds the configuration for creating a new chat Intent.
type IntentConfig struct {
	Engine       *engine.Engine
	AgentID      string
	SessionID    string
	ProviderName string
	ModelName    string
	TokenBudget  int
}

// Intent handles chat interactions in the TUI.
type Intent struct {
	engine       *engine.Engine
	agentID      string
	sessionID    string
	messages     []string
	input        string
	mode         string
	streaming    bool
	response     strings.Builder
	width        int
	height       int
	statusBar    *layout.StatusBar
	tokenCount   int
	providerName string
	modelName    string
	tokenBudget  int
}

// NewIntent creates a new chat intent from the given configuration.
//
// Expected:
//   - cfg.Engine is a non-nil Engine instance.
//   - cfg.AgentID and cfg.SessionID are non-empty strings.
//   - cfg.ProviderName and cfg.ModelName identify the active provider and model.
//   - cfg.TokenBudget is the maximum token allocation for the session.
//
// Returns:
//   - An initialised Intent with default dimensions (80x24), normal mode, and a configured StatusBar.
//
// Side effects:
//   - None.
func NewIntent(cfg IntentConfig) *Intent {
	sb := layout.NewStatusBar(80)
	sb.Update(layout.StatusBarMsg{
		Provider:    cfg.ProviderName,
		Model:       cfg.ModelName,
		Mode:        "NORMAL",
		TokensUsed:  0,
		TokenBudget: cfg.TokenBudget,
	})

	return &Intent{
		engine:       cfg.Engine,
		agentID:      cfg.AgentID,
		sessionID:    cfg.SessionID,
		messages:     []string{},
		input:        "",
		mode:         "normal",
		streaming:    false,
		width:        80,
		height:       24,
		statusBar:    sb,
		tokenCount:   0,
		providerName: cfg.ProviderName,
		modelName:    cfg.ModelName,
		tokenBudget:  cfg.TokenBudget,
	}
}

// Init returns the initial command for the intent.
//
// Returns:
//   - nil (no initial command).
//
// Side effects:
//   - None.
func (i *Intent) Init() tea.Cmd {
	return nil
}

// Update processes a Bubble Tea message and returns any command to execute.
//
// Expected:
//   - msg is a tea.Msg from the Bubble Tea event loop.
//
// Returns:
//   - A tea.Cmd to execute, or nil if no command is needed.
//
// Side effects:
//   - Updates terminal dimensions on WindowSizeMsg.
//   - Accumulates token count on StreamChunkMsg.
//   - Delegates to handleKeyMsg for key events.
func (i *Intent) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return i.handleKeyMsg(msg)
	case tea.WindowSizeMsg:
		i.width = msg.Width
		i.height = msg.Height
		return nil
	case StreamChunkMsg:
		i.handleStreamChunk(msg)
		return nil
	}
	return nil
}

// handleKeyMsg processes keyboard input and returns any command to execute.
//
// Expected:
//   - msg is a tea.KeyMsg from the Bubble Tea event loop.
//
// Returns:
//   - A tea.Cmd to execute, or nil if no command is needed.
//
// Side effects:
//   - Updates mode, input, or returns a quit command based on key input.
func (i *Intent) handleKeyMsg(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyCtrlC:
		return tea.Quit
	case tea.KeyEscape:
		if i.mode == "insert" {
			i.mode = "normal"
			i.syncStatusBarMode()
		}
		return nil
	case tea.KeyBackspace:
		if i.mode == "insert" && i.input != "" {
			i.input = i.input[:len(i.input)-1]
		}
		return nil
	case tea.KeyEnter:
		if i.mode == "insert" && i.input != "" {
			return i.sendMessage()
		}
		return nil
	case tea.KeyRunes:
		return i.handleRunes(msg.Runes)
	}
	return nil
}

// handleRunes processes rune input in the current mode.
//
// Expected:
//   - runes is a slice of runes from keyboard input.
//
// Returns:
//   - A tea.Cmd to execute, or nil if no command is needed.
//
// Side effects:
//   - Switches to insert mode on 'i' in normal mode, or appends runes to input in insert mode.
//   - Updates StatusBar mode indicator on mode switch.
func (i *Intent) handleRunes(runes []rune) tea.Cmd {
	if i.mode == "normal" {
		if len(runes) == 1 {
			switch runes[0] {
			case 'i':
				i.mode = "insert"
				i.syncStatusBarMode()
				return nil
			case 'q':
				return tea.Quit
			}
		}
		return nil
	}

	i.input += string(runes)
	return nil
}

// handleStreamChunk processes a streaming response chunk by accumulating token count.
//
// Expected:
//   - msg is a StreamChunkMsg with content from the provider stream.
//
// Side effects:
//   - Increments the token count using approximate character-based estimation.
//   - Updates the StatusBar with the new token count.
func (i *Intent) handleStreamChunk(msg StreamChunkMsg) {
	approxTokens := len(msg.Content) / 4
	if approxTokens == 0 && msg.Content != "" {
		approxTokens = 1
	}
	i.tokenCount += approxTokens
	i.syncStatusBar()
}

// syncStatusBar updates the StatusBar with the current intent state.
//
// Side effects:
//   - Updates the StatusBar with provider, model, mode, and token information.
func (i *Intent) syncStatusBar() {
	i.statusBar.Update(layout.StatusBarMsg{
		Provider:    i.providerName,
		Model:       i.modelName,
		Mode:        i.statusBarMode(),
		TokensUsed:  i.tokenCount,
		TokenBudget: i.tokenBudget,
	})
}

// syncStatusBarMode updates only the mode in the StatusBar.
//
// Side effects:
//   - Updates the StatusBar mode indicator.
func (i *Intent) syncStatusBarMode() {
	i.statusBar.Update(layout.StatusBarMsg{
		Mode:        i.statusBarMode(),
		TokensUsed:  i.tokenCount,
		TokenBudget: i.tokenBudget,
	})
}

// statusBarMode returns the mode string for the StatusBar display.
//
// Returns:
//   - "NORMAL" or "INSERT" based on the current input mode.
//
// Side effects:
//   - None.
func (i *Intent) statusBarMode() string {
	if i.mode == "insert" {
		return "INSERT"
	}
	return "NORMAL"
}

// sendMessage appends the current input to messages and starts streaming.
//
// Returns:
//   - nil (no command to execute).
//
// Side effects:
//   - Appends the input to messages, clears input, and sets streaming to true.
func (i *Intent) sendMessage() tea.Cmd {
	i.messages = append(i.messages, "> "+i.input)
	i.input = ""
	i.streaming = true
	return nil
}

// View renders the chat interface as a string.
//
// Returns:
//   - A rendered chat view with messages, input, mode indicator, and StatusBar.
//
// Side effects:
//   - None.
func (i *Intent) View() string {
	var builder strings.Builder

	for _, msg := range i.messages {
		builder.WriteString(msg)
		builder.WriteString("\n")
	}

	if i.response.Len() > 0 {
		builder.WriteString(i.response.String())
		builder.WriteString("\n")
	}

	builder.WriteString("\n")
	builder.WriteString("> ")
	builder.WriteString(i.input)
	builder.WriteString("\n\n")

	if i.mode == "normal" {
		builder.WriteString("[NORMAL] q: quit | i: insert mode")
	} else {
		builder.WriteString("[INSERT] Esc: normal mode | Enter: send")
	}

	builder.WriteString("\n")
	builder.WriteString(i.statusBar.RenderContent(i.width))

	return builder.String()
}

// Mode returns the current input mode.
//
// Returns:
//   - The current mode: "normal" or "insert".
//
// Side effects:
//   - None.
func (i *Intent) Mode() string {
	return i.mode
}

// Input returns the current input text.
//
// Returns:
//   - The current input text.
//
// Side effects:
//   - None.
func (i *Intent) Input() string {
	return i.input
}

// Messages returns all messages in the chat history.
//
// Returns:
//   - A slice of all messages in the chat.
//
// Side effects:
//   - None.
func (i *Intent) Messages() []string {
	return i.messages
}

// IsStreaming returns whether the intent is currently streaming a response.
//
// Returns:
//   - true if streaming, false otherwise.
//
// Side effects:
//   - None.
func (i *Intent) IsStreaming() bool {
	return i.streaming
}

// Width returns the current terminal width.
//
// Returns:
//   - The current terminal width in columns.
//
// Side effects:
//   - None.
func (i *Intent) Width() int {
	return i.width
}

// Height returns the current terminal height.
//
// Returns:
//   - The current terminal height in rows.
//
// Side effects:
//   - None.
func (i *Intent) Height() int {
	return i.height
}

// TokenCount returns the approximate token count accumulated during streaming.
//
// Returns:
//   - The current token count.
//
// Side effects:
//   - None.
func (i *Intent) TokenCount() int {
	return i.tokenCount
}
