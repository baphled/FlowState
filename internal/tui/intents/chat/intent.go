// Package chat provides the chat intent for FlowState TUI.
package chat

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/config"
	contextpkg "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	tuiintents "github.com/baphled/flowstate/internal/tui/intents"
	"github.com/baphled/flowstate/internal/tui/intents/models"
	"github.com/baphled/flowstate/internal/tui/uikit/layout"
	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
	"github.com/baphled/flowstate/internal/tui/views/chat"
	"github.com/baphled/flowstate/internal/ui/terminal"
)

// StreamChunkMsg carries a streaming response chunk to the chat intent.
type StreamChunkMsg struct {
	Content    string
	Error      error
	Done       bool
	ToolCall   *provider.ToolCall
	ToolStatus string
	EventType  string
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

// AppShell abstracts app methods needed by the chat intent.
type AppShell interface {
	// WriteConfig persists the given application configuration.
	WriteConfig(cfg *config.AppConfig) error
	// List returns the names of all registered providers.
	List() []string
	// Get returns the provider with the given name.
	Get(name string) (provider.Provider, error)
}

// IntentConfig holds the configuration for creating a new chat Intent.
type IntentConfig struct {
	App           AppShell
	Engine        *engine.Engine
	AgentID       string
	SessionID     string
	ProviderName  string
	ModelName     string
	TokenBudget   int
	AgentRegistry *agent.Registry
}

// Intent handles chat interactions in the TUI.
type Intent struct {
	app               AppShell
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
	statusIndicator   *widgets.StatusIndicator
	tokenCount        int
	tokenCounter      contextpkg.TokenCounter
	providerName      string
	modelName         string
	tokenBudget       int
	tickFrame         int
	streamChan        <-chan provider.StreamChunk
	cancelStream      context.CancelFunc
	lastEscTime       time.Time
	pendingPermission *ToolPermissionMsg
	result            *tuiintents.IntentResult
	msgViewport       viewport.Model
	vpReady           bool
	agentRegistry     *agent.Registry
	toolCallName      string
	toolCallStatus    string
	loadedSkills      []string
	view              *chat.View
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
		app:             cfg.App,
		engine:          cfg.Engine,
		agentID:         cfg.AgentID,
		sessionID:       cfg.SessionID,
		messages:        []chat.Message{},
		input:           "",
		streaming:       false,
		response:        "",
		width:           80,
		height:          24,
		statusBar:       sb,
		statusIndicator: widgets.NewStatusIndicator(nil),
		tokenCount:      0,
		tokenCounter:    contextpkg.NewTiktokenCounter(),
		providerName:    cfg.ProviderName,
		modelName:       cfg.ModelName,
		tokenBudget:     cfg.TokenBudget,
		tickFrame:       0,
		result:          nil,
		agentRegistry:   cfg.AgentRegistry,
		view:            chat.NewView(),
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
		if !msg.Done {
			return tea.Batch(
				func() tea.Msg { return i.readNextChunk() },
				tickSpinner(),
			)
		}
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
	case tea.KeyEsc:
		return i.handleEscapeKey()
	case tea.KeyCtrlC:
		return tea.Quit
	case tea.KeyTab:
		return i.toggleAgent()
	case tea.KeyCtrlP:
		return i.openModelSelector()
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

// handleEscapeKey detects double-press of Escape within 500ms to cancel a
// streaming response.
//
// Returns:
//   - A tea.Cmd from cancelStreamingResponse on double-press, or nil.
//
// Side effects:
//   - Records escape timestamp on first press while streaming.
//   - Cancels streaming and discards partial response on double-press.
func (i *Intent) handleEscapeKey() tea.Cmd {
	if !i.streaming {
		return nil
	}
	now := time.Now()
	if !i.lastEscTime.IsZero() && now.Sub(i.lastEscTime) < 500*time.Millisecond {
		return i.cancelStreamingResponse()
	}
	i.lastEscTime = now
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
//   - If Error is set, appends error message to content and logs critical errors.
//   - Preserves partial response accumulated during streaming even if an error occurs.
//   - Updates tool call state when present.
//   - When Done is true and streaming is already false (cancelled), discards the chunk without appending.
//   - Counts tokens and updates the StatusBar.
func (i *Intent) handleStreamChunk(msg StreamChunkMsg) {
	if msg.EventType == "skills_loaded" {
		i.loadedSkills = strings.Split(msg.Content, ",")
		formattedSkills := strings.ReplaceAll(msg.Content, ",", ", ")
		i.messages = append(i.messages, chat.Message{
			Role:    "system",
			Content: "📚 Skills: " + formattedSkills,
		})
		i.syncStatusBar()
		i.refreshViewport()
		return
	}

	if msg.Done && !i.streaming {
		i.response = ""
		return
	}

	if !msg.Done {
		i.response += msg.Content
		if msg.ToolCall != nil {
			i.toolCallName = msg.ToolCall.Name
			i.toolCallStatus = msg.ToolStatus
		}
	} else {
		i.finalizeResponse(msg)
		i.toolCallName = ""
		i.toolCallStatus = ""
	}

	tokens := i.tokenCounter.Count(msg.Content)
	i.tokenCount += tokens
	i.syncStatusBar()
}

// finalizeResponse completes a streaming response and handles any errors.
//
// Expected:
//   - msg.Done is true.
//   - i.response contains the accumulated partial response.
//
// Returns:
//   - Nothing (modifies i.messages, i.response, and i.streaming in place).
//
// Side effects:
//   - Appends the final message to i.messages.
//   - Clears i.response and sets i.streaming to false.
//   - Logs critical errors to stderr.
func (i *Intent) finalizeResponse(msg StreamChunkMsg) {
	content := i.response + msg.Content
	if msg.Error != nil {
		formatted := formatErrorMessage(msg.Error)
		if content != "" {
			content += "\n\n" + formatted
		} else {
			content = formatted
		}
		if isLogWorthy(msg.Error) {
			fmt.Fprintf(os.Stderr, "chat: streaming error: %v\n", msg.Error)
		}
	}
	if content != "" {
		i.messages = append(i.messages, chat.Message{
			Role:    "assistant",
			Content: content,
		})
	}
	i.streaming = false
	i.response = ""
}

// httpErrorPattern matches HTTP error strings like POST "https://api.example.com/v1/messages": 404 Not Found.
var httpErrorPattern = regexp.MustCompile(
	`^(\w+)\s+"(https?://[^"]+)":\s+(\d+\s+\S[\w\s]*)(?:\s+(.+))?$`,
)

// modelInBodyPattern extracts model names from JSON error bodies.
var modelInBodyPattern = regexp.MustCompile(`"model[:\s]*([a-zA-Z0-9._/-]+)"`)

// messageInBodyPattern extracts human-readable messages from JSON error bodies.
var messageInBodyPattern = regexp.MustCompile(`"message":\s*"([^"]+)"`)

const maxFallbackLength = 100

// formatErrorMessage parses a raw error and returns a structured, readable display string.
//
// Expected:
//   - err is a non-nil error from a provider streaming operation.
//
// Returns:
//   - A formatted multi-line string for HTTP errors with extracted fields.
//   - A truncated single-line fallback for unparseable errors.
//
// Side effects:
//   - None.
func formatErrorMessage(err error) string {
	errMsg := err.Error()
	if matches := httpErrorPattern.FindStringSubmatch(errMsg); matches != nil {
		return buildHTTPErrorDisplay(matches)
	}
	return buildFallbackDisplay(errMsg)
}

// buildHTTPErrorDisplay formats an HTTP error into a structured multi-line display.
//
// Expected:
//   - matches contains [full, method, url, status, body] from httpErrorPattern.
//
// Returns:
//   - A multi-line formatted error string with provider, model, and detail fields.
//
// Side effects:
//   - None.
func buildHTTPErrorDisplay(matches []string) string {
	url := matches[2]
	status := strings.TrimSpace(matches[3])
	body := matches[4]

	providerName := extractProviderFromURL(url)
	modelName := extractModelFromBody(body)
	detail := extractDetailFromBody(body)

	var sb strings.Builder
	fmt.Fprintf(&sb, "⚠ API Error (%s)", status)
	if providerName != "" {
		fmt.Fprintf(&sb, "\n  Provider: %s", providerName)
	}
	if modelName != "" {
		fmt.Fprintf(&sb, "\n  Model: %s", modelName)
	}
	if detail != "" {
		fmt.Fprintf(&sb, "\n  Detail: %s", detail)
	}
	return sb.String()
}

// buildFallbackDisplay returns a truncated single-line error for unparseable messages.
//
// Expected:
//   - errMsg is the raw error string.
//
// Returns:
//   - A single line prefixed with ⚠ Error:, truncated if longer than maxFallbackLength.
//
// Side effects:
//   - None.
func buildFallbackDisplay(errMsg string) string {
	if len(errMsg) > maxFallbackLength {
		return "⚠ Error: " + errMsg[:maxFallbackLength] + "..."
	}
	return "⚠ Error: " + errMsg
}

// extractProviderFromURL extracts a provider name from an API URL hostname.
//
// Expected:
//   - url is a valid HTTP(S) URL string.
//
// Returns:
//   - A provider name like "anthropic" or "openai", or empty string if not recognised.
//
// Side effects:
//   - None.
func extractProviderFromURL(url string) string {
	switch {
	case strings.Contains(url, "anthropic"):
		return "anthropic"
	case strings.Contains(url, "openai"):
		return "openai"
	case strings.Contains(url, "ollama"):
		return "ollama"
	default:
		return ""
	}
}

// extractModelFromBody extracts a model name from a JSON error body.
//
// Expected:
//   - body is a JSON string that may contain a model reference.
//
// Returns:
//   - The model name if found, or empty string.
//
// Side effects:
//   - None.
func extractModelFromBody(body string) string {
	if body == "" {
		return ""
	}
	if matches := modelInBodyPattern.FindStringSubmatch(body); matches != nil {
		return matches[1]
	}
	return ""
}

// extractDetailFromBody extracts a human-readable detail from a JSON error body.
//
// Expected:
//   - body is a JSON string that may contain a "message" field.
//
// Returns:
//   - The extracted detail message, or empty string.
//
// Side effects:
//   - None.
func extractDetailFromBody(body string) string {
	if body == "" {
		return ""
	}
	if matches := messageInBodyPattern.FindStringSubmatch(body); matches != nil {
		return matches[1]
	}
	return ""
}

// isLogWorthy determines which errors are critical enough to log to stderr.
//
// Expected:
//   - err may be nil (returns false immediately).
//
// Returns:
//   - true if the error should be logged to stderr, false otherwise.
//
// Critical errors include: missing configuration, authentication failures,
// and complete provider failures. Non-critical errors (partial responses, timeouts)
// are logged to the chat but not stderr.
//
// Side effects:
//   - None.
func isLogWorthy(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	return strings.Contains(errMsg, "no model preferences") ||
		strings.Contains(errMsg, "API key") ||
		strings.Contains(errMsg, "invalid") ||
		strings.Contains(errMsg, "required") ||
		strings.Contains(errMsg, "all providers failed") ||
		strings.Contains(errMsg, "authentication")
}

// syncStatusBar updates the StatusBar with the current intent state.
//
// Side effects:
//   - Updates the StatusBar with provider, model, and token information.
func (i *Intent) syncStatusBar() {
	i.statusBar.Update(layout.StatusBarMsg{
		Provider:     i.providerName,
		Model:        i.modelName,
		AgentID:      i.agentID,
		TokensUsed:   i.tokenCount,
		TokenBudget:  i.tokenBudget,
		LoadedSkills: i.loadedSkills,
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
	i.view.SetDimensions(i.width, i.msgViewport.Height)
	i.view.SetStreaming(i.streaming, i.response)
	if i.toolCallName != "" && i.toolCallStatus != "" {
		i.view.SetToolCall(i.toolCallName, i.toolCallStatus)
	}
	i.view.SetMessages(i.messages)
	content := i.view.RenderContent(i.width)
	i.msgViewport.SetContent(content)
	i.msgViewport.GotoBottom()
}

// sendMessage appends the current input to messages and streams a response from the engine.
//
// Returns:
//   - A tea.Cmd that starts the stream and reads the first chunk.
//
// Side effects:
//   - Appends the input to messages as a user message, clears input, and sets streaming to true.
//   - Clears tool call state and stores the stream channel on the intent for subsequent chunk reads.
func (i *Intent) sendMessage() tea.Cmd {
	userMessage := i.input
	i.input = ""

	if strings.HasPrefix(userMessage, "/") {
		return i.handleSlashCommand(userMessage)
	}

	i.messages = append(i.messages, chat.Message{Role: "user", Content: userMessage})
	i.streaming = true
	i.response = ""
	i.toolCallName = ""
	i.toolCallStatus = ""
	i.refreshViewport()

	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		i.cancelStream = cancel
		stream, err := i.engine.Stream(ctx, i.agentID, userMessage)
		if err != nil {
			return StreamChunkMsg{Content: "", Error: err, Done: true}
		}
		i.streamChan = stream
		return i.readNextChunk()
	}
}

// cancelStreamingResponse cancels the active stream, discards partial content,
// and resets the intent to accept new input.
//
// Returns:
//   - nil (no async command needed).
//
// Side effects:
//   - Calls the cancel function to stop the stream context.
//   - Clears streaming state, partial response, and escape timing.
func (i *Intent) cancelStreamingResponse() tea.Cmd {
	if i.cancelStream != nil {
		i.cancelStream()
		i.cancelStream = nil
	}
	i.streaming = false
	i.response = ""
	i.lastEscTime = time.Time{}
	return nil
}

// readNextChunk reads one chunk from the active stream channel.
//
// Returns:
//   - A StreamChunkMsg with the next chunk's content, error, and done state.
//   - If the channel is closed, returns StreamChunkMsg{Done: true}.
//
// Side effects:
//   - Blocks until a chunk is available on the stream channel.
//   - Captures tool call state if present in chunk.
func (i *Intent) readNextChunk() tea.Msg {
	chunk, ok := <-i.streamChan
	if !ok {
		return StreamChunkMsg{Done: true}
	}

	toolStatus := ""
	if chunk.ToolCall != nil {
		toolStatus = "running"
	}

	return StreamChunkMsg{
		Content:    chunk.Content,
		Error:      chunk.Error,
		Done:       chunk.Done,
		ToolCall:   chunk.ToolCall,
		ToolStatus: toolStatus,
		EventType:  chunk.EventType,
	}
}

// View renders the chat interface as a string.
//
// Returns:
//   - A rendered chat view with messages in a persistent viewport and input in the footer.
//
// Side effects:
//   - Syncs streaming state into the StatusBar.
//   - Updates status indicator based on streaming state.
func (i *Intent) View() string {
	i.statusBar.SetStreaming(i.streaming, i.tickFrame)
	i.updateStatusIndicator()

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

	status := i.renderStatusString()

	sl := layout.NewScreenLayout(&terminal.Info{Width: i.width, Height: i.height}).
		WithBreadcrumbs("Chat").
		WithContent(content).
		WithInput(inputLine).
		WithStatusBar(i.statusBar.RenderContent(i.width)).
		WithHelp(status + "  ·  Enter: send  ·  /models /model /help  ·  ↑/↓ PgUp/PgDn: scroll  ·  Ctrl+C: quit").
		WithFooterSeparator(true)

	return sl.Render()
}

// updateStatusIndicator updates the status indicator based on streaming state.
//
// Side effects:
//   - Updates the status indicator active state and advances frame if streaming.
func (i *Intent) updateStatusIndicator() {
	if i.streaming {
		i.statusIndicator.SetActive(true)
		i.statusIndicator.SetFrame(i.tickFrame)
	} else {
		i.statusIndicator.SetActive(false)
	}
}

// renderStatusString returns the current status as a display string.
//
// Returns:
//   - "Thinking..." with spinner when streaming, "Ready" when idle.
//
// Side effects:
//   - None.
func (i *Intent) renderStatusString() string {
	if i.streaming {
		return i.statusIndicator.Render()
	}
	return "Ready"
}

// Result returns the current outcome state of the chat intent.
//
// Returns:
//   - The current IntentResult, or nil if no result has been set.
//
// Side effects:
//   - None.
func (i *Intent) Result() *tuiintents.IntentResult {
	return i.result
}

// handleSlashCommand processes a slash command and returns a Cmd.
//
// Expected:
//   - cmd is a non-empty string starting with "/".
//
// Returns:
//   - A tea.Cmd that appends a system message and refreshes the viewport.
//
// Side effects:
//   - Parses the command and executes its logic.
//   - Appends system messages to the message list.
//   - May update model preference via SetModelPreference.
func (i *Intent) handleSlashCommand(cmd string) tea.Cmd {
	return func() tea.Msg {
		parts := strings.SplitN(strings.TrimPrefix(cmd, "/"), " ", 2)
		command := parts[0]
		args := ""
		if len(parts) > 1 {
			args = parts[1]
		}

		var response string
		switch command {
		case "models":
			availableModels, err := i.engine.ListAvailableModels()
			if err != nil {
				response = "Error listing models: " + err.Error()
			} else if len(availableModels) == 0 {
				response = "No models available"
			} else {
				var sb strings.Builder
				sb.WriteString("Available models:\n")
				for _, m := range availableModels {
					fmt.Fprintf(&sb, "  • %s (%s, %d tokens)\n", m.ID, m.Provider, m.ContextLength)
				}
				response = sb.String()
			}

		case "model":
			if args == "" {
				response = "Usage: /model <provider>/<model-name>\nExample: /model ollama/llama2"
			} else {
				parts := strings.Split(args, "/")
				if len(parts) != 2 {
					response = "Usage: /model <provider>/<model>"
				} else {
					providerName := strings.TrimSpace(parts[0])
					model := strings.TrimSpace(parts[1])
					i.engine.SetModelPreference(providerName, model)
					i.providerName = providerName
					i.modelName = model
					i.syncStatusBar()
					response = "Switched to model: " + providerName + "/" + model
				}
			}

		case "help":
			response = "Available slash commands:\n" +
				"  /models - List all available models\n" +
				"  /model <provider>/<model> - Switch to a model\n" +
				"  /help - Show this help message"

		default:
			response = "Unknown command: /" + command
		}

		i.messages = append(i.messages, chat.Message{Role: "system", Content: response})
		i.refreshViewport()
		return nil
	}
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

// openModelSelector creates and shows the model selector as a modal overlay.
//
// Returns:
//   - A tea.Cmd that emits a ShowModalMsg to display the model selector.
//
// Side effects:
//   - None.
func (i *Intent) openModelSelector() tea.Cmd {
	return func() tea.Msg {
		if i.app == nil {
			return nil
		}
		modelIntent := models.NewIntent(models.IntentConfig{
			AppShell:         i.app,
			ProviderRegistry: i.app,
			OnSelect: func(provider, model string) {
				i.engine.SetModelPreference(provider, model)
				i.providerName = provider
				i.modelName = model
				i.syncStatusBar()
			},
		})
		return tuiintents.ShowModalMsg{Modal: modelIntent}
	}
}

// toggleAgent alternates the active agent between "planner" and "executor".
//
// Expected:
//   - i.agentRegistry is non-nil.
//   - Both "planner" and "executor" manifests exist in the registry.
//
// Returns:
//   - nil (no async command needed — switch is synchronous).
//
// Side effects:
//   - Updates i.agentID, i.engine manifest, and status bar.
func (i *Intent) toggleAgent() tea.Cmd {
	if i.agentRegistry == nil {
		return nil
	}
	next := "planner"
	if i.agentID == "planner" {
		next = "executor"
	}
	manifest, found := i.agentRegistry.Get(next)
	if !found {
		return nil
	}
	i.engine.SetManifest(*manifest)
	i.agentID = next
	i.syncStatusBar()
	return nil
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

// SetApp sets the TUI app shell reference for navigation.
//
// Expected:
//   - appShell is a non-nil reference to the TUI app shell.
//
// Side effects:
//   - Sets the internal app reference used for intent switching.
func (i *Intent) SetApp(appShell AppShell) {
	i.app = appShell
}

// AgentIDForTest returns the current agent ID for testing purposes.
//
// Returns:
//   - The current agent ID.
//
// Side effects:
//   - None.
func (i *Intent) AgentIDForTest() string {
	return i.agentID
}

// SetAgentIDForTest sets the agent ID for testing purposes.
//
// Expected:
//   - id is a non-empty string matching a known agent ID.
//
// Side effects:
//   - Sets the internal agentID field.
func (i *Intent) SetAgentIDForTest(id string) {
	i.agentID = id
}

// MessagesForTest returns all messages including system and user roles.
//
// Returns:
//   - A slice of all messages in the chat, unfiltered by role.
//
// Side effects:
//   - None.
func (i *Intent) MessagesForTest() []chat.Message {
	return i.messages
}
