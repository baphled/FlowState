// Package chat provides the chat intent for FlowState TUI.
package chat

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/config"
	contextpkg "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	tuiintents "github.com/baphled/flowstate/internal/tui/intents"
	"github.com/baphled/flowstate/internal/tui/intents/models"
	"github.com/baphled/flowstate/internal/tui/intents/sessionbrowser"
	"github.com/baphled/flowstate/internal/tui/uikit/feedback"
	"github.com/baphled/flowstate/internal/tui/uikit/layout"
	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
	"github.com/baphled/flowstate/internal/tui/views/chat"
	"github.com/baphled/flowstate/internal/ui/terminal"
	"github.com/baphled/flowstate/internal/ui/themes"
)

// StreamChunkMsg carries a streaming response chunk to the chat intent.
// EventType propagates the provider.StreamChunk.EventType, enabling the intent
// to handle special events such as "harness_retry" without modifying the
// standard chunk processing pipeline.
type StreamChunkMsg struct {
	Content        string
	Error          error
	Done           bool
	EventType      string
	ToolCallName   string
	ToolStatus     string
	ToolResult     string
	ToolIsError    bool
	DelegationInfo *provider.DelegationInfo
	Next           tea.Cmd
}

// SpinnerTickMsg is sent periodically to advance the chat spinner animation.
type SpinnerTickMsg struct{}

// SessionSavedMsg signals completion of an async session save operation.
type SessionSavedMsg struct {
	// Err holds any error that occurred during saving, or nil on success.
	Err error
}

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

// SessionLister lists available sessions and manages session metadata.
type SessionLister interface {
	// List returns metadata for all saved sessions, sorted by most recently active first.
	List() []contextpkg.SessionInfo
	// SetTitle updates the title of an existing session.
	SetTitle(sessionID string, title string) error
	// Load retrieves a context store from a saved session.
	Load(sessionID string) (*recall.FileContextStore, error)
	// Save persists the session store to disk with the provided metadata.
	Save(sessionID string, store *recall.FileContextStore, meta contextpkg.SessionMetadata) error
}

// IntentConfig holds the configuration for creating a new chat Intent.
type IntentConfig struct {
	App           AppShell
	Engine        *engine.Engine
	Streamer      Streamer
	AgentID       string
	SessionID     string
	ProviderName  string
	ModelName     string
	TokenBudget   int
	AgentRegistry *agent.Registry
	SessionStore  SessionLister
}

// Intent handles chat interactions in the TUI.
type Intent struct {
	app               AppShell
	engine            *engine.Engine
	streamer          Streamer
	agentID           string
	sessionID         string
	input             string
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
	pendingPermission *ToolPermissionMsg
	result            *tuiintents.IntentResult
	msgViewport       viewport.Model
	vpReady           bool
	agentRegistry     *agent.Registry
	sessionStore      SessionLister
	view              *chat.View
	loadingModal      *feedback.Modal
	errorModal        *feedback.Modal
	// activeToolCall holds the name of the currently executing tool call during streaming.
	activeToolCall string
	streamCancel   context.CancelFunc
	// cachedScreenLayout holds the reusable ScreenLayout for View() to avoid allocations.
	cachedScreenLayout *layout.ScreenLayout
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
		streamer:        cfg.Streamer,
		agentID:         cfg.AgentID,
		sessionID:       cfg.SessionID,
		input:           "",
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
		sessionStore:    cfg.SessionStore,
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
		extraLines := i.inputLineCount() - 1
		footerHeight := 8 + extraLines
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
		i.cachedScreenLayout = layout.NewScreenLayout(&terminal.Info{Width: msg.Width, Height: msg.Height}).
			WithBreadcrumbs("Chat").
			WithFooterSeparator(true)
		return nil
	case StreamChunkMsg:
		return i.handleStreamChunkMsg(msg)
	case ToolPermissionMsg:
		i.handleToolPermission(msg)
		return nil
	case SessionSavedMsg:
		return nil
	case SpinnerTickMsg:
		if i.view.IsStreaming() {
			i.tickFrame++
			i.view.SetTickFrame(i.tickFrame)
		}
		if i.loadingModal != nil {
			i.loadingModal.AdvanceSpinner()
		}
		return tickSpinner()
	case sessionbrowser.SessionSelectedMsg:
		return i.handleSessionResult(msg)
	case sessionbrowser.SessionLoadedMsg:
		return i.handleSessionLoaded(msg)
	}
	return nil
}

// handleModalKeyMsg processes keyboard input when a feedback modal is active.
//
// Expected:
//   - msg is a tea.KeyMsg from the Bubble Tea event loop.
//
// Returns:
//   - true if a modal consumed the input, false otherwise.
//
// Side effects:
//   - Dismisses error modal on Esc or Enter.
func (i *Intent) handleModalKeyMsg(msg tea.KeyMsg) bool {
	if i.errorModal != nil {
		if msg.Type == tea.KeyEsc || msg.Type == tea.KeyEnter {
			i.errorModal = nil
		}
		return true
	}
	return i.loadingModal != nil
}

// handleScrollKey processes viewport scroll keys when the viewport is ready.
//
// Expected:
//   - msg is a tea.KeyMsg from the Bubble Tea event loop.
//
// Returns:
//   - A tea.Cmd and true if the key was a scroll key, or nil and false otherwise.
//
// Side effects:
//   - Updates the viewport position on scroll keys.
func (i *Intent) handleScrollKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if !i.vpReady {
		return nil, false
	}
	switch msg.Type {
	case tea.KeyPgUp, tea.KeyPgDown, tea.KeyUp, tea.KeyDown, tea.KeyHome, tea.KeyEnd:
		var cmd tea.Cmd
		i.msgViewport, cmd = i.msgViewport.Update(msg)
		return cmd, true
	}
	return nil, false
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
	if i.handleModalKeyMsg(msg) {
		return nil
	}
	if i.pendingPermission != nil {
		return i.handlePermissionKey(msg)
	}
	if cmd, handled := i.handleScrollKey(msg); handled {
		return cmd
	}

	switch msg.Type {
	case tea.KeyCtrlC:
		i.cancelActiveStream()
		return tea.Sequence(i.saveSession(), tea.Quit)
	case tea.KeyTab:
		return i.toggleAgent()
	case tea.KeyCtrlP:
		return i.openModelSelector()
	case tea.KeyCtrlS:
		return i.openSessionBrowser()
	case tea.KeyBackspace:
		if i.input != "" {
			i.input = i.input[:len(i.input)-1]
		}
		return nil
	case tea.KeyEnter:
		if msg.Alt {
			i.input += "\n"
			i.updateViewportForInput()
			return nil
		}
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

// inputLineCount returns the number of lines in the current input.
//
// Returns:
//   - The count of lines (1 for empty/single-line input, more for multiline).
//
// Side effects:
//   - None.
func (i *Intent) inputLineCount() int {
	return strings.Count(i.input, "\n") + 1
}

// updateViewportForInput adjusts the viewport height to account for multiline input.
//
// Side effects:
//   - Updates msgViewport.Height based on input line count.
func (i *Intent) updateViewportForInput() {
	if !i.vpReady {
		return
	}
	extraLines := i.inputLineCount() - 1
	footerHeight := 8 + extraLines
	vpHeight := i.height - footerHeight
	if vpHeight < 1 {
		vpHeight = 1
	}
	i.msgViewport.Height = vpHeight
}

// handleStreamChunk processes a streaming response chunk.
//
// Expected:
//   - msg is a StreamChunkMsg with content from the provider stream.
//
// Side effects:
//   - Delegates to view.HandleChunk for streaming state management.
//   - Counts tokens and updates the StatusBar.
func (i *Intent) handleStreamChunk(msg StreamChunkMsg) {
	errMsg := ""
	if msg.Error != nil {
		errMsg = chat.FormatErrorMessage(msg.Error)
		if chat.IsLogWorthy(msg.Error) {
			fmt.Fprintf(os.Stderr, "chat: streaming error: %v\n", msg.Error)
		}
	}

	committedSkill := false
	lastToolCall := ""
	if msg.ToolCallName != "" {
		i.activeToolCall = msg.ToolCallName
		if strings.HasPrefix(msg.ToolCallName, "skill:") {
			committedSkill = true
		}
	} else if i.activeToolCall != "" {
		lastToolCall = i.activeToolCall
		role := "tool_call"
		content := i.activeToolCall
		if strings.HasPrefix(i.activeToolCall, "skill:") {
			role = "skill_load"
			content = strings.TrimPrefix(i.activeToolCall, "skill:")
			committedSkill = true
		}
		i.view.AddMessage(chat.Message{Role: role, Content: content})
		i.activeToolCall = ""
	}

	suppressResult := isReadToolCall(lastToolCall) && !msg.ToolIsError
	if msg.ToolResult != "" && !committedSkill && !suppressResult {
		role := "tool_result"
		if msg.ToolIsError {
			role = "tool_error"
		}
		i.view.AddMessage(chat.Message{Role: role, Content: msg.ToolResult})
	}

	if msg.DelegationInfo != nil {
		i.view.HandleDelegation(msg.DelegationInfo)
	}

	i.view.HandleChunk(msg.Content, msg.Done, errMsg, msg.ToolCallName, msg.ToolStatus)

	if msg.Done && i.engine != nil {
		contextResult := i.engine.LastContextResult()
		i.tokenCount = contextResult.TokensUsed
	} else {
		tokens := i.tokenCounter.Count(msg.Content)
		i.tokenCount += tokens
	}

	i.syncStatusBar()
}

// handleStreamChunkMsg processes a StreamChunkMsg and returns the appropriate tea.Cmd.
//
// Expected:
//   - msg contains streaming data, completion status, and optional next command.
//
// Returns:
//   - A tea.Cmd that either batches the next command with spinner, saves the session, or ticks the spinner.
//
// Side effects:
//   - Calls handleStreamChunk and refreshViewport.
//   - Intercepts harness_retry events before standard chunk processing.
func (i *Intent) handleStreamChunkMsg(msg StreamChunkMsg) tea.Cmd {
	switch msg.EventType {
	case "harness_retry":
		return i.handleHarnessRetry(msg)
	case "harness_attempt_start", "harness_complete", "harness_critic_feedback":
		return i.handleHarnessEvent(msg)
	}
	i.handleStreamChunk(msg)
	i.refreshViewport()
	if !msg.Done && msg.Next != nil {
		return tea.Batch(msg.Next, tickSpinner())
	}
	if msg.Done {
		return i.saveSession()
	}
	return tickSpinner()
}

// handleHarnessRetry commits any partial response to history, adds a system
// retry notice, resets streaming state, and continues reading from the same
// stream channel.
//
// Expected:
//   - msg.EventType is "harness_retry".
//   - msg.Content carries the human-readable retry notice.
//   - msg.Next is the continuation command for reading subsequent chunks.
//
// Returns:
//   - A tea.Cmd that batches the next chunk read with a spinner tick.
//
// Side effects:
//   - Commits accumulated partial text as an assistant message.
//   - Adds a system message with the retry notice.
//   - Resets the streaming buffer via StartStreaming.
//   - Refreshes the viewport to reflect new messages.
func (i *Intent) handleHarnessRetry(msg StreamChunkMsg) tea.Cmd {
	if partial := i.view.Response(); partial != "" {
		i.view.AddMessage(chat.Message{Role: "assistant", Content: partial})
	}
	i.view.AddMessage(chat.Message{Role: "system", Content: msg.Content})
	i.view.StartStreaming()
	i.refreshViewport()
	if msg.Next != nil {
		return tea.Batch(msg.Next, tickSpinner())
	}
	return tickSpinner()
}

// handleHarnessEvent silently consumes harness observability events.
// These events are for internal tracking and do not affect the session.
//
// Expected:
//   - msg is a StreamChunkMsg with a harness event type.
//
// Returns:
//   - The next command from msg.Next if present, or nil.
//
// Side effects:
//   - None.
func (i *Intent) handleHarnessEvent(msg StreamChunkMsg) tea.Cmd {
	if msg.Next != nil {
		return msg.Next
	}
	return nil
}

// saveSession builds session metadata from the current engine state and persists
// the session asynchronously via a tea.Cmd.
//
// Returns:
//   - A tea.Cmd that writes the session to disk and returns a SessionSavedMsg.
//
// Side effects:
//   - None until the returned Cmd is executed by the Bubble Tea runtime.
func (i *Intent) saveSession() tea.Cmd {
	if i.sessionStore == nil || i.engine == nil {
		return nil
	}
	store := i.engine.ContextStore()
	if store == nil {
		return nil
	}
	sessionStore := i.sessionStore
	sessionID := i.sessionID
	meta := contextpkg.SessionMetadata{
		AgentID:      i.agentID,
		SystemPrompt: i.engine.BuildSystemPrompt(),
		LoadedSkills: i.engine.LoadedSkills(),
	}
	return func() tea.Msg {
		return SessionSavedMsg{Err: sessionStore.Save(sessionID, store, meta)}
	}
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
	i.view.SetDimensions(i.width, i.msgViewport.Height)
	content := i.view.RenderContent(i.width)
	i.msgViewport.SetContent(content)
	i.msgViewport.GotoBottom()
}

// detectAgentFromInput examines the message for planner or executor keywords and returns the matching agent.
//
// Expected:
//   - message is the raw user input string.
//
// Returns:
//   - "planner" if any planner keywords are found (takes priority).
//   - "executor" if any executor keywords are found.
//   - "" if no keywords match.
//
// Side effects:
//   - None.
func detectAgentFromInput(message string) string {
	lower := strings.ToLower(message)

	plannerKeywords := []string{
		"create a plan", "let's plan", "i want to build", "i need to",
		"how do i", "what should", "help me",
		"plan", "design", "architect", "strategy",
	}
	for _, kw := range plannerKeywords {
		if strings.Contains(lower, kw) {
			return "planner"
		}
	}

	executorKeywords := []string{
		"run the plan", "start execution", "begin execution",
		"run it", "do it",
		"execute", "implement",
	}
	for _, kw := range executorKeywords {
		if strings.Contains(lower, kw) {
			return "executor"
		}
	}

	return ""
}

// cancelActiveStream cancels the context of the current streaming producer, if any.
//
// Side effects:
//   - Calls the stored cancel function and clears it.
func (i *Intent) cancelActiveStream() {
	if i.streamCancel != nil {
		i.streamCancel()
		i.streamCancel = nil
	}
}

// sendMessage appends the current input to messages and streams a response from the engine.
//
// Returns:
//   - A tea.Cmd that starts the stream and reads the first chunk.
//
// Side effects:
//   - Appends the input to messages as a user message, clears input, and sets streaming to true.
func (i *Intent) sendMessage() tea.Cmd {
	userMessage := i.input
	i.input = ""
	i.updateViewportForInput()

	if strings.HasPrefix(userMessage, "/") {
		return i.handleSlashCommand(userMessage)
	}

	if detected := detectAgentFromInput(userMessage); detected != "" && detected != i.agentID {
		if i.agentRegistry != nil {
			if manifest, found := i.agentRegistry.Get(detected); found {
				i.engine.SetManifest(*manifest)
				i.agentID = detected
				i.syncStatusBar()
			}
		}
	}

	i.view.AddMessage(chat.Message{Role: "user", Content: userMessage})
	i.view.StartStreaming()
	i.refreshViewport()

	i.cancelActiveStream()
	ctx, cancel := context.WithCancel(context.Background())
	i.streamCancel = cancel

	return func() tea.Msg {
		stream, err := i.streamer.Stream(ctx, i.agentID, userMessage)
		if err != nil {
			return StreamChunkMsg{Content: "", Error: err, Done: true}
		}
		return i.readNextChunkFrom(stream)
	}
}

// readNextChunk reads one chunk from the active stream channel.
//
// Returns:
//   - A StreamChunkMsg with the next chunk's content, error, and done state.
//   - If the channel is closed, returns StreamChunkMsg{Done: true}.
//
// Side effects:
//   - Blocks until a chunk is available on the stream channel.
func (i *Intent) readNextChunk() tea.Msg {
	chunk, ok := <-i.streamChan
	if !ok {
		return StreamChunkMsg{Done: true}
	}

	toolCallName, toolStatus := extractToolInfo(chunk.ToolCall)

	msg := StreamChunkMsg{
		Content:        chunk.Content,
		Error:          chunk.Error,
		Done:           chunk.Done,
		EventType:      chunk.EventType,
		ToolCallName:   toolCallName,
		ToolStatus:     toolStatus,
		DelegationInfo: chunk.DelegationInfo,
	}

	if chunk.ToolResult != nil {
		msg.ToolResult = chunk.ToolResult.Content
		msg.ToolIsError = chunk.ToolResult.IsError
	}

	if !chunk.Done {
		msg.Next = func() tea.Msg {
			return i.readNextChunk()
		}
	}

	return msg
}

// readNextChunkFrom stores the stream channel and reads the first chunk.
//
// Expected:
//   - stream is a non-nil channel from engine.Stream.
//
// Returns:
//   - A StreamChunkMsg with the first chunk's content, error, and done state.
//
// Side effects:
//   - Stores the stream channel in i.streamChan for subsequent reads.
func (i *Intent) readNextChunkFrom(stream <-chan provider.StreamChunk) tea.Msg {
	i.streamChan = stream
	return i.readNextChunk()
}

// readStreamChunk reads one chunk from the given stream channel and returns a StreamChunkMsg.
//
// Expected:
//   - stream is a non-nil channel from engine.Stream.
//
// Returns:
//   - A StreamChunkMsg with content, error, done state, and a Next closure for the following chunk.
//   - If the channel is closed, returns StreamChunkMsg{Done: true} with nil Next.
//
// Side effects:
//   - Blocks until a chunk is available on the stream channel.
func readStreamChunk(stream <-chan provider.StreamChunk) StreamChunkMsg {
	chunk, ok := <-stream
	if !ok {
		return StreamChunkMsg{Done: true}
	}

	toolCallName, toolStatus := extractToolInfo(chunk.ToolCall)

	msg := StreamChunkMsg{
		Content:        chunk.Content,
		Error:          chunk.Error,
		Done:           chunk.Done,
		EventType:      chunk.EventType,
		ToolCallName:   toolCallName,
		ToolStatus:     toolStatus,
		DelegationInfo: chunk.DelegationInfo,
	}

	if chunk.ToolResult != nil {
		msg.ToolResult = chunk.ToolResult.Content
		msg.ToolIsError = chunk.ToolResult.IsError
	}

	if !chunk.Done {
		msg.Next = func() tea.Msg {
			return readStreamChunk(stream)
		}
	}

	return msg
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
	i.statusBar.SetStreaming(i.view.IsStreaming(), i.tickFrame)
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
		inputLine = i.renderInputLine()
	}

	status := i.renderStatusString()

	if i.cachedScreenLayout == nil {
		i.cachedScreenLayout = layout.NewScreenLayout(&terminal.Info{Width: i.width, Height: i.height}).
			WithBreadcrumbs("Chat").
			WithFooterSeparator(true)
	}

	sl := i.cachedScreenLayout
	sl.WithContent(content).
		WithInput(inputLine).
		WithStatusBar(i.statusBar.RenderContent(i.width)).
		WithHelp(status + "  ·  Alt+Enter: new line  ·  Enter: send  ·  /models /model /help  ·  ↑/↓ PgUp/PgDn: scroll  ·  Ctrl+C: quit")

	return i.renderModalOverlay(sl.Render())
}

// renderModalOverlay applies loading or error modal overlays to the base view.
//
// Expected:
//   - baseView is the fully rendered chat view string.
//
// Returns:
//   - The base view with any active modal overlaid, or the base view unchanged.
//
// Side effects:
//   - None.
func (i *Intent) renderModalOverlay(baseView string) string {
	if i.loadingModal != nil {
		modalContent := i.loadingModal.Render(i.width, i.height)
		return feedback.RenderOverlay(baseView, modalContent, i.width, i.height, themes.NewDefaultTheme())
	}
	if i.errorModal != nil {
		modalContent := i.errorModal.Render(i.width, i.height)
		return feedback.RenderOverlay(baseView, modalContent, i.width, i.height, themes.NewDefaultTheme())
	}
	return baseView
}

// renderInputLine renders the current input with a "> " prompt on the first line
// and "  " indent on continuation lines for multiline inputs.
//
// Returns:
//   - The formatted input string with prompts.
//
// Side effects:
//   - None.
func (i *Intent) renderInputLine() string {
	if !strings.Contains(i.input, "\n") {
		return "> " + i.input
	}
	lines := strings.Split(i.input, "\n")
	rendered := make([]string, len(lines))
	for idx, line := range lines {
		if idx == 0 {
			rendered[idx] = "> " + line
		} else {
			rendered[idx] = "  " + line
		}
	}
	return strings.Join(rendered, "\n")
}

// updateStatusIndicator updates the status indicator based on streaming state.
//
// Side effects:
//   - Updates the status indicator active state and advances frame if streaming.
func (i *Intent) updateStatusIndicator() {
	if i.view.IsStreaming() {
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
	if i.view.IsStreaming() {
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

// handleModelsCommand processes the /models command.
//
// Returns:
//   - A response message string listing available models.
//
// Side effects:
//   - None.
func (i *Intent) handleModelsCommand() string {
	availableModels, err := i.engine.ListAvailableModels()
	if err != nil {
		return "Error listing models: " + err.Error()
	}
	if len(availableModels) == 0 {
		return "No models available"
	}
	var sb strings.Builder
	sb.WriteString("Available models:\n")
	for _, m := range availableModels {
		fmt.Fprintf(&sb, "  • %s (%s, %d tokens)\n", m.ID, m.Provider, m.ContextLength)
	}
	return sb.String()
}

// handleModelCommand processes the /model command.
//
// Expected:
//   - args is in the format "provider/model".
//
// Returns:
//   - A response message string.
//
// Side effects:
//   - Updates providerName and modelName if valid format.
//   - Calls engine.SetModelPreference if valid format.
func (i *Intent) handleModelCommand(args string) string {
	if args == "" {
		return "Usage: /model <provider>/<model-name>\nExample: /model ollama/llama2"
	}
	parts := strings.Split(args, "/")
	if len(parts) != 2 {
		return "Usage: /model <provider>/<model>"
	}
	providerName := strings.TrimSpace(parts[0])
	model := strings.TrimSpace(parts[1])
	i.engine.SetModelPreference(providerName, model)
	i.providerName = providerName
	i.modelName = model
	i.tokenBudget = i.engine.ModelContextLimit()
	i.syncStatusBar()
	return "Switched to model: " + providerName + "/" + model
}

// handleAgentCommand processes the /agent command.
//
// Expected:
//   - args is the agent ID to switch to.
//
// Returns:
//   - A response message string.
//
// Side effects:
//   - Updates agentID and syncs status bar if agent is found.
//   - Calls engine.SetManifest if agent is found.
func (i *Intent) handleAgentCommand(args string) string {
	if args == "" {
		return "Usage: /agent <agent-id>\nExample: /agent planner"
	}
	if i.agentRegistry == nil {
		return "No agent registry available"
	}
	agentID := strings.TrimSpace(args)
	manifest, found := i.agentRegistry.Get(agentID)
	if !found {
		return "Unknown agent: " + agentID
	}
	i.engine.SetManifest(*manifest)
	i.agentID = agentID
	i.tokenBudget = i.engine.ModelContextLimit()
	i.syncStatusBar()
	return "Switched to agent: " + agentID
}

// handleAgentsCommand processes the /agents command.
//
// Returns:
//   - A response message string listing available agents.
//
// Side effects:
//   - None.
func (i *Intent) handleAgentsCommand() string {
	if i.agentRegistry == nil {
		return "No agent registry available"
	}
	agents := i.agentRegistry.List()
	if len(agents) == 0 {
		return "No agents available"
	}
	var sb strings.Builder
	sb.WriteString("Available agents:\n")
	for _, m := range agents {
		fmt.Fprintf(&sb, "  • %s\n", m.ID)
	}
	return sb.String()
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
			response = i.handleModelsCommand()

		case "model":
			response = i.handleModelCommand(args)

		case "agent":
			response = i.handleAgentCommand(args)

		case "agents":
			response = i.handleAgentsCommand()

		case "help":
			response = "Available slash commands:\n" +
				"  /models - List all available models\n" +
				"  /model <provider>/<model> - Switch to a model\n" +
				"  /agent <agent-id> - Switch to an agent\n" +
				"  /agents - List all available agents\n" +
				"  /help - Show this help message"

		default:
			response = "Unknown command: /" + command
		}

		i.view.AddMessage(chat.Message{Role: "system", Content: response})
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
				i.tokenBudget = i.engine.ModelContextLimit()
				i.syncStatusBar()
			},
		})
		return tuiintents.ShowModalMsg{Modal: modelIntent}
	}
}

// openSessionBrowser creates and shows the session browser as a modal overlay.
//
// Returns:
//   - A tea.Cmd that emits a ShowModalMsg to display the session browser.
//
// Side effects:
//   - None.
func (i *Intent) openSessionBrowser() tea.Cmd {
	return func() tea.Msg {
		if i.sessionStore == nil {
			return nil
		}
		sessions := i.sessionStore.List()
		entries := make([]sessionbrowser.SessionEntry, len(sessions))
		for idx := range sessions {
			s := &sessions[idx]
			title := s.Title
			if title == "" {
				switch {
				case !s.LastActive.IsZero():
					title = "Session — " + s.LastActive.Format("2 Jan 2006 15:04")
				case s.MessageCount > 0:
					title = fmt.Sprintf("Session (%d messages)", s.MessageCount)
				default:
					title = "Session " + s.ID[:8]
				}
			}
			entries[idx] = sessionbrowser.SessionEntry{
				ID:           s.ID,
				Title:        title,
				MessageCount: s.MessageCount,
				LastActive:   s.LastActive,
			}
		}
		browserIntent := sessionbrowser.NewIntent(sessionbrowser.IntentConfig{
			Sessions: entries,
		})
		return tuiintents.ShowModalMsg{Modal: browserIntent}
	}
}

// handleSessionResult dispatches the session browser outcome to the
// appropriate handler based on whether a new or existing session was chosen.
//
// Expected:
//   - msg is a SessionSelectedMsg from the session browser intent.
//
// Returns:
//   - A tea.Cmd, or nil when no further action is needed.
//
// Side effects:
//   - Delegates to createNewSession or switchToSession.
func (i *Intent) handleSessionResult(msg sessionbrowser.SessionSelectedMsg) tea.Cmd {
	if msg.IsNew {
		return i.createNewSession()
	}
	return i.switchToSession(msg.SessionID)
}

// createNewSession resets the chat to a fresh session with a new ID.
//
// Returns:
//   - nil (no async command needed).
//
// Side effects:
//   - Generates a new session ID, resets the chat view, and syncs the status bar.
func (i *Intent) createNewSession() tea.Cmd {
	i.sessionID = uuid.New().String()
	i.engine.SetContextStore(recall.NewEmptyContextStore(""))
	i.view = chat.NewView()
	i.refreshViewport()
	i.syncStatusBar()
	return nil
}

// switchToSession shows a loading modal and kicks off an async session load.
//
// Expected:
//   - sessionID is a non-empty string matching an existing session file.
//
// Returns:
//   - A tea.Cmd that loads the session from disk asynchronously.
//
// Side effects:
//   - Sets the loading modal and syncs the status bar.
func (i *Intent) switchToSession(sessionID string) tea.Cmd {
	i.sessionID = sessionID
	i.loadingModal = feedback.NewLoadingModal("Loading session\u2026", false)
	i.syncStatusBar()
	return i.loadSessionAsync(sessionID)
}

// loadSessionAsync returns a command that loads a session from disk.
//
// Expected:
//   - sessionID is a non-empty string matching an existing session file.
//
// Returns:
//   - A tea.Cmd that sends a SessionLoadedMsg when the load completes.
//
// Side effects:
//   - None (I/O happens inside the returned command).
func (i *Intent) loadSessionAsync(sessionID string) tea.Cmd {
	store := i.sessionStore
	return func() tea.Msg {
		loadedStore, err := store.Load(sessionID)
		return sessionbrowser.SessionLoadedMsg{
			SessionID: sessionID,
			Store:     loadedStore,
			Err:       err,
		}
	}
}

// handleSessionLoaded processes the result of an async session load.
//
// Expected:
//   - msg contains the loaded FileContextStore, or an error.
//
// Returns:
//   - nil (no further command needed).
//
// Side effects:
//   - Clears the loading modal.
//   - On error, shows an error modal.
//   - On success, replaces the engine's context store with the loaded store,
//     populates the chat view, and refreshes the viewport.

// toolCallSummary extracts the primary argument from a tool call and formats it as "toolName: arg".
//
// Expected:
//   - name is a valid tool name.
//   - args contains the tool call arguments.
//
// Returns:
//   - A formatted string "toolName: arg" if a primary argument is found.
//   - Just the tool name if no primary argument is found.
//   - For bash commands longer than 80 characters, truncates and appends "...".
//
// Side effects:
//   - None.
func toolCallSummary(name string, args map[string]interface{}) string {
	argKey := toolCallArgKey(name)
	if argKey == "" {
		return name
	}

	arg, ok := args[argKey].(string)
	if !ok || arg == "" {
		return name
	}

	if name == "bash" && len(arg) > 80 {
		arg = arg[:80] + "..."
	}

	return fmt.Sprintf("%s: %s", name, arg)
}

// extractToolInfo extracts the tool call name and status from a provider.ToolCall.
//
// Expected:
//   - toolCall may be nil.
//
// Returns:
//   - toolCallName: "skill:name" for skill_load, "tool: args" for other tools, or "" if toolCall is nil.
//   - toolStatus: "running" if toolCall is not nil, or "" otherwise.
//
// Side effects:
//   - None.
func extractToolInfo(toolCall *provider.ToolCall) (string, string) {
	if toolCall == nil {
		return "", ""
	}

	var toolCallName string
	if toolCall.Name == "skill_load" {
		toolCallName = "skill_load"
		if name, ok := toolCall.Arguments["name"].(string); ok && name != "" {
			toolCallName = "skill:" + name
		}
	} else {
		toolCallName = toolCallSummary(toolCall.Name, toolCall.Arguments)
	}

	return toolCallName, "running"
}

// isReadToolCall reports whether the given tool call name refers to the read tool.
//
// Expected:
//   - name is a raw tool name (e.g. "read") or a formatted summary (e.g. "read: /path").
//
// Returns:
//   - true when name is "read" or begins with "read: ".
//   - false for all other names, including empty strings.
//
// Side effects:
//   - None.
func isReadToolCall(name string) bool {
	return name == "read" || strings.HasPrefix(name, "read: ")
}

// toolCallArgKey returns the argument key for a given tool name.
//
// Expected:
//   - name is a valid tool name.
//
// Returns:
//   - The argument key for the tool (e.g., "command" for bash, "filePath" for read).
//   - An empty string if the tool is not recognized.
//
// Side effects:
//   - None.
func toolCallArgKey(name string) string {
	switch name {
	case "bash":
		return "command"
	case "read", "write", "edit":
		return "filePath"
	case "glob", "grep":
		return "pattern"
	case "skill_load":
		return "name"
	default:
		return ""
	}
}

// handleSessionLoaded processes the result of an async session load.
//
// Expected:
//   - msg contains the loaded FileContextStore, or an error.
//
// Returns:
//   - nil (no further command needed).
//
// Side effects:
//   - Clears the loading modal.
//   - On error, shows an error modal.
//   - On success, replaces the engine's context store with the loaded store,
//     populates the chat view, and refreshes the viewport.
func (i *Intent) handleSessionLoaded(msg sessionbrowser.SessionLoadedMsg) tea.Cmd {
	i.loadingModal = nil
	if msg.Err != nil {
		i.errorModal = feedback.NewErrorModal("Session Error", "Failed to load session: "+msg.Err.Error())
		return nil
	}
	i.engine.SetContextStore(msg.Store)
	i.view = chat.NewView()
	var lastToolCallName string
	for _, sm := range msg.Store.GetStoredMessages() {
		switch {
		case sm.Message.Role == "assistant" && len(sm.Message.ToolCalls) > 0 && sm.Message.Content == "":
			for _, tc := range sm.Message.ToolCalls {
				lastToolCallName = tc.Name
				role := "tool_call"
				content := toolCallSummary(tc.Name, tc.Arguments)
				if tc.Name == "skill_load" {
					role = "skill_load"
				}
				i.view.AddMessage(chat.Message{Role: role, Content: content})
			}
		case sm.Message.Role == "tool":
			if isReadToolCall(lastToolCallName) && !strings.HasPrefix(strings.ToLower(sm.Message.Content), "error:") {
				continue
			}
			role := "tool_result"
			if strings.HasPrefix(strings.ToLower(sm.Message.Content), "error:") {
				role = "tool_error"
			}
			i.view.AddMessage(chat.Message{Role: role, Content: sm.Message.Content})
		default:
			i.view.AddMessage(chat.Message{
				Role:    sm.Message.Role,
				Content: sm.Message.Content,
			})
		}
	}
	i.refreshViewport()
	i.syncStatusBar()
	return nil
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
	i.tokenBudget = i.engine.ModelContextLimit()
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
	for _, msg := range i.view.Messages() {
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
	return i.view.Response()
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
	return i.view.IsStreaming()
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
	return i.view.Messages()
}
