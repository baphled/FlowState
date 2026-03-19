// Package chat provides the chat intent for FlowState TUI.
package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	contextpkg "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/tui/app"
	"github.com/baphled/flowstate/internal/tui/uikit/layout"
	"github.com/baphled/flowstate/internal/tui/views/chat"
	"github.com/baphled/flowstate/internal/ui/terminal"
)

// StreamChunkMsg carries a streaming response chunk to the chat intent.
type StreamChunkMsg struct {
	Content string
	Done    bool
}

// SpinnerTickMsg is sent periodically to advance the chat spinner animation.
type SpinnerTickMsg struct{}

// tickSpinner returns a Cmd that fires a SpinnerTickMsg after a short delay.
//
// Returns:
//   - A tea.Cmd that sends SpinnerTickMsg after 100ms.
//
// Side effects:
//   - None.
func tickSpinner() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg {
		return SpinnerTickMsg{}
	})
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
	tickFrame         int
	pendingPermission *ToolPermissionMsg
	result            *app.IntentResult
	msgViewport       viewport.Model
	vpReady           bool
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
//   - An initialised Intent with default dimensions (80x24) and a configured StatusBar.
//
// Side effects:
//   - None.
func NewIntent(cfg IntentConfig) *Intent {
	sb := layout.NewStatusBar(80)
	sb.Update(layout.StatusBarMsg{
		Provider:    cfg.ProviderName,
		Model:       cfg.ModelName,
		AgentID:     cfg.AgentID,
		TokensUsed:  0,
		TokenBudget: cfg.TokenBudget,
	})

	return &Intent{
		engine:       cfg.Engine,
		agentID:      cfg.AgentID,
		sessionID:    cfg.SessionID,
		messages:     []chat.Message{},
		input:        "",
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
		tickFrame:    0,
		result:       nil,
	}
}

// Init returns the initial command for the intent.
//
// Returns:
//   - A tea.Cmd that starts the spinner tick loop.
//
// Side effects:
//   - Schedules the first SpinnerTickMsg.
func (i *Intent) Init() tea.Cmd {
	return tickSpinner()
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
		footerHeight := 8
		vpHeight := msg.Height - footerHeight
		if vpHeight < 1 {
			vpHeight = 1
		}
		if !i.vpReady {
			i.msgViewport = viewport.New(msg.Width, vpHeight)
			i.msgViewport.SetContent("")
			i.vpReady = true
		} else {
			i.msgViewport.Width = msg.Width
			i.msgViewport.Height = vpHeight
		}
		return nil
	case StreamChunkMsg:
		i.handleStreamChunk(msg)
		i.refreshViewport()
		return tickSpinner()
	case ToolPermissionMsg:
		i.handleToolPermission(msg)
		return nil
	case SpinnerTickMsg:
		if i.streaming {
			i.tickFrame++
		}
		return tickSpinner()
	}
	return nil
}

// handleKeyMsg processes keyboard input directly without mode switching.
//
// Expected:
//   - msg is a tea.KeyMsg from the Bubble Tea event loop.
//
// Returns:
//   - A tea.Cmd to execute, or nil if no command is needed.
//
// Side effects:
//   - Updates input or returns a quit command based on key input.
func (i *Intent) handleKeyMsg(msg tea.KeyMsg) tea.Cmd {
	if i.pendingPermission != nil {
		return i.handlePermissionKey(msg)
	}

	if i.vpReady {
		switch msg.Type {
		case tea.KeyPgUp, tea.KeyPgDown, tea.KeyUp, tea.KeyDown, tea.KeyHome, tea.KeyEnd:
			var cmd tea.Cmd
			i.msgViewport, cmd = i.msgViewport.Update(msg)
			return cmd
		}
	}

	switch msg.Type {
	case tea.KeyCtrlC:
		return tea.Quit
	case tea.KeyBackspace:
		if i.input != "" {
			i.input = i.input[:len(i.input)-1]
		}
		return nil
	case tea.KeyEnter:
		if i.input != "" {
			return i.sendMessage()
		}
		return nil
	case tea.KeySpace:
		i.input += " "
		return nil
	case tea.KeyRunes:
		i.input += string(msg.Runes)
		return nil
	}
	return nil
}

// handleStreamChunk processes a streaming response chunk.
//
// Expected:
//   - msg is a StreamChunkMsg with content from the provider stream.
//
// Side effects:
//   - When Done is true, appends the accumulated response to messages, clears streaming state.
//   - When Done is false, accumulates content in i.response.
//   - Counts tokens and updates the StatusBar.
func (i *Intent) handleStreamChunk(msg StreamChunkMsg) {
	if msg.Done {
		i.messages = append(i.messages, chat.Message{
			Role:    "assistant",
			Content: msg.Content,
		})
		i.streaming = false
		i.response = ""
	} else {
		i.response += msg.Content
	}

	tokens := i.tokenCounter.Count(msg.Content)
	i.tokenCount += tokens
	i.syncStatusBar()
}

// syncStatusBar updates the StatusBar with the current intent state.
//
// Side effects:
//   - Updates the StatusBar with provider, model, and token information.
func (i *Intent) syncStatusBar() {
	i.statusBar.Update(layout.StatusBarMsg{
		Provider:    i.providerName,
		Model:       i.modelName,
		AgentID:     i.agentID,
		TokensUsed:  i.tokenCount,
		TokenBudget: i.tokenBudget,
	})
}

// refreshViewport rebuilds the message viewport content and scrolls to the bottom.
//
// Side effects:
//   - Updates msgViewport content and scrolls to latest message.
func (i *Intent) refreshViewport() {
	if !i.vpReady {
		return
	}
	cv := chat.NewView()
	cv.SetDimensions(i.width, i.msgViewport.Height)
	cv.SetStreaming(i.streaming, i.response)
	for _, msg := range i.messages {
		cv.AddMessage(msg)
	}
	content := cv.RenderContent(i.width)
	i.msgViewport.SetContent(content)
	i.msgViewport.GotoBottom()
}

// sendMessage appends the current input to messages and streams a response from the engine.
//
// Returns:
//   - A tea.Cmd that performs the streaming operation.
//
// Side effects:
//   - Appends the input to messages as a user message, clears input, and sets streaming to true.
//   - The returned Cmd streams the response and returns a StreamChunkMsg{Done: true}.
func (i *Intent) sendMessage() tea.Cmd {
	userMessage := i.input
	i.messages = append(i.messages, chat.Message{Role: "user", Content: userMessage})
	i.input = ""
	i.streaming = true
	i.response = ""
	i.refreshViewport()

	return func() tea.Msg {
		ctx := context.Background()
		stream, err := i.engine.Stream(ctx, i.agentID, userMessage)
		if err != nil {
			return StreamChunkMsg{Content: "", Done: true}
		}

		var accumulated strings.Builder
		for chunk := range stream {
			accumulated.WriteString(chunk.Content)
		}
		return StreamChunkMsg{Content: accumulated.String(), Done: true}
	}
}

// View renders the chat interface as a string.
//
// Returns:
//   - A rendered chat view with messages in a persistent viewport and input in the footer.
//
// Side effects:
//   - Syncs streaming state into the StatusBar.
func (i *Intent) View() string {
	i.statusBar.SetStreaming(i.streaming, i.tickFrame)

	var content string
	if i.vpReady {
		content = i.msgViewport.View()
	}

	var inputLine string
	switch {
	case i.pendingPermission != nil:
		inputLine = fmt.Sprintf("[PERMISSION] Allow tool %q? (y/n)", i.pendingPermission.ToolName)
	default:
		inputLine = "> " + i.input
	}

	sl := layout.NewScreenLayout(&terminal.Info{Width: i.width, Height: i.height}).
		WithBreadcrumbs("Chat").
		WithContent(content).
		WithInput(inputLine).
		WithStatusBar(i.statusBar.RenderContent(i.width)).
		WithHelp("Enter: send  ·  ↑/↓ PgUp/PgDn: scroll  ·  Ctrl+C: quit").
		WithFooterSeparator(true)

	return sl.Render()
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
	var result []chat.Message
	for _, msg := range i.messages {
		if msg.Role == "assistant" {
			result = append(result, msg)
		}
	}
	return result
}

// Response returns the current streaming response content.
//
// Returns:
//   - The partial response string accumulated during streaming.
//
// Side effects:
//   - None.
func (i *Intent) Response() string {
	return i.response
}

// SpinnerFrame returns the current spinner animation frame as a string.
//
// Returns:
//   - The braille spinner character for the current tick frame.
//
// Side effects:
//   - None.
func (i *Intent) SpinnerFrame() string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	return frames[i.tickFrame%len(frames)]
}

// TickFrame returns the current tick frame counter for testing.
//
// Returns:
//   - The current integer tick frame index.
//
// Side effects:
//   - None.
func (i *Intent) TickFrame() int {
	return i.tickFrame
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
