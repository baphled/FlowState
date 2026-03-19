// Package chat provides the chat intent for FlowState TUI.
package chat

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	contextpkg "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/tui/app"
	"github.com/baphled/flowstate/internal/tui/uikit/layout"
	"github.com/baphled/flowstate/internal/tui/views/chat"
)

// StreamChunkMsg carries a streaming response chunk to the chat intent.
type StreamChunkMsg struct {
	Content string
	Done    bool
}

// ToolPermissionMsg requests user approval for a tool invocation.
type ToolPermissionMsg struct {
	ToolName  string
	Arguments map[string]interface{}
	Response  chan<- bool
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
	engine            *engine.Engine
	agentID           string
	sessionID         string
	messages          []chat.Message
	input             string
	mode              string
	streaming         bool
	response          string
	width             int
	height            int
	statusBar         *layout.StatusBar
	tokenCount        int
	tokenCounter      contextpkg.TokenCounter
	providerName      string
	modelName         string
	tokenBudget       int
	pendingPermission *ToolPermissionMsg
	result            *app.IntentResult
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
		messages:     []chat.Message{},
		input:        "",
		mode:         "normal",
		streaming:    false,
		response:     "",
		width:        80,
		height:       24,
		statusBar:    sb,
		tokenCount:   0,
		tokenCounter: contextpkg.NewTiktokenCounter(),
		providerName: cfg.ProviderName,
		modelName:    cfg.ModelName,
		tokenBudget:  cfg.TokenBudget,
		result:       nil,
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
	case ToolPermissionMsg:
		i.handleToolPermission(msg)
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
	if i.mode == "permission" {
		return i.handlePermissionKey(msg)
	}

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
	case tea.KeySpace:
		if i.mode == "insert" {
			i.input += " "
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
//   - Increments the token count using the configured TokenCounter (TiktokenCounter with fallback to approximate).
//   - Updates the StatusBar with the new token count.
func (i *Intent) handleStreamChunk(msg StreamChunkMsg) {
	tokens := i.tokenCounter.Count(msg.Content)
	i.tokenCount += tokens
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

// sendMessage appends the current input to messages and streams a response from the engine.
//
// Returns:
//   - A tea.Cmd that performs the streaming operation.
//
// Side effects:
//   - Appends the input to messages as a user message, clears input, and sets streaming to true.
//   - Initiates engine.Stream() to fetch the AI response.
func (i *Intent) sendMessage() tea.Cmd {
	userMessage := i.input
	i.messages = append(i.messages, chat.Message{
		Role:    "user",
		Content: userMessage,
	})
	i.input = ""
	i.streaming = true
	i.response = ""

	return func() tea.Msg {
		ctx := context.Background()
		stream, err := i.engine.Stream(ctx, i.agentID, userMessage)
		if err != nil {
			return nil
		}

		var accumulated strings.Builder
		for chunk := range stream {
			accumulated.WriteString(chunk.Content)
		}

		i.messages = append(i.messages, chat.Message{
			Role:    "assistant",
			Content: accumulated.String(),
		})
		i.streaming = false
		i.response = ""

		return nil
	}
}

// View renders the chat interface as a string.
//
// Returns:
//   - A rendered chat view with messages, input, mode indicator, and StatusBar.
//
// Side effects:
//   - Recreates the ChatView state from the current intent state before rendering.
func (i *Intent) View() string {
	cv := chat.NewView()
	cv.SetDimensions(i.width, i.height)
	cv.SetInput(i.input)
	cv.SetMode(i.mode)
	cv.SetStreaming(i.streaming, i.response)

	for _, msg := range i.messages {
		cv.AddMessage(msg)
	}

	var builder strings.Builder
	builder.WriteString(cv.RenderContent(i.width))
	builder.WriteString("\n\n")

	switch i.mode {
	case "permission":
		builder.WriteString(i.renderPermissionPrompt())
	case "insert":
		builder.WriteString("[INSERT] Esc: normal mode | Enter: send")
	default:
		builder.WriteString("[NORMAL] q: quit | i: insert mode")
	}

	builder.WriteString("\n")
	builder.WriteString(i.statusBar.RenderContent(i.width))

	return builder.String()
}

// Result returns the current outcome state of the chat intent.
//
// Returns:
//   - The current IntentResult, or nil if no result has been set.
//
// Side effects:
//   - None.
func (i *Intent) Result() *app.IntentResult {
	return i.result
}

// handleToolPermission processes a tool permission request by entering permission mode.
//
// Expected:
//   - msg contains tool details and a response channel.
//
// Side effects:
//   - Switches the intent to "permission" mode and stores the pending request.
func (i *Intent) handleToolPermission(msg ToolPermissionMsg) {
	i.mode = "permission"
	i.pendingPermission = &msg
}

// handlePermissionKey processes key input during permission mode.
//
// Expected:
//   - msg is a tea.KeyMsg while in permission mode.
//
// Returns:
//   - A tea.Cmd to execute, or nil.
//
// Side effects:
//   - Sends approval/denial on the response channel and returns to normal mode.
func (i *Intent) handlePermissionKey(msg tea.KeyMsg) tea.Cmd {
	if msg.Type != tea.KeyRunes || len(msg.Runes) == 0 {
		return nil
	}

	switch msg.Runes[0] {
	case 'y':
		i.resolvePermission(true)
	case 'n':
		i.resolvePermission(false)
	}
	return nil
}

// resolvePermission sends the user's decision and exits permission mode.
//
// Expected:
//   - approved indicates whether the user accepted the tool call.
//
// Side effects:
//   - Sends the decision on the pending permission's response channel.
//   - Clears the pending permission and returns to normal mode.
func (i *Intent) resolvePermission(approved bool) {
	if i.pendingPermission != nil && i.pendingPermission.Response != nil {
		i.pendingPermission.Response <- approved
	}
	i.pendingPermission = nil
	i.mode = "normal"
}

// renderPermissionPrompt builds the permission confirmation prompt.
//
// Returns:
//   - A string showing tool name and y/n options.
//
// Side effects:
//   - None.
func (i *Intent) renderPermissionPrompt() string {
	if i.pendingPermission == nil {
		return "[PERMISSION] No pending request"
	}
	return fmt.Sprintf("[PERMISSION] Allow tool %q? (y/n)", i.pendingPermission.ToolName)
}

// Mode returns the current input mode.
//
// Returns:
//   - The current mode: "normal", "insert", or "permission".
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
func (i *Intent) Messages() []chat.Message {
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
