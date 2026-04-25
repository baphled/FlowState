// Package chat provides the chat view component for the TUI.
package chat

import (
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
)

const defaultRenderWidth = 80

// Message represents a chat message with a role and content.
type Message struct {
	Role       string
	Content    string
	ToolName   string         // set for tool_result messages
	ToolInput  string         // set for tool_result messages (primary argument)
	AgentColor lipgloss.Color // set for assistant messages (zero = use theme default)
	ModelID    string         // set for assistant messages (empty = no footer)
	Duration   time.Duration  // set on the final assistant message of a turn (zero = no duration shown)
}

// View represents the chat view component with messages and streaming state.
type View struct {
	theme.Aware

	width                 int
	height                int
	messages              []Message
	streaming             bool
	response              string
	renderFunc            func(string, int) string
	toolCallName          string
	toolCallStatus        string
	toolCallArgs          map[string]any
	toolCallResult        string
	delegationInfo        *provider.DelegationInfo
	activeDelegationBlock *CollapsibleDelegationBlock
	tickFrame             int
	agentColor            lipgloss.Color
	modelID               string
	cachedRenderer        *glamour.TermRenderer
	cachedRenderWidth     int
	renderedMessages      []string

	// turnStartedAt marks when the current streaming turn began. Set
	// by SetTurnStartedAt at the user's send time and used by the
	// streaming-time elapsed indicator (rendered at the end of
	// appendStreamingContent so the user sees how long the turn has
	// been running). Cleared by ClearTurnTiming once the duration is
	// stamped onto the final assistant message.
	turnStartedAt time.Time
	// turnDuration is the duration finaliseChunk stamps onto the
	// final assistant message of the turn. Set by SetTurnDuration
	// just before HandleChunk's Done path runs.
	turnDuration time.Duration

	// cachedPartial* throttle the streaming-time markdown re-parse.
	// Token-stream providers can deliver 14+ chunks/sec; without a
	// throttle each chunk fires a fresh Glamour re-parse on the
	// growing v.response. By the end of a long turn the response is
	// thousands of chars and each re-parse takes ~5-50ms — that
	// burns CPU during streaming. We cap the re-parse rate at ~10 fps
	// (every 100ms): the user perceives no difference at that
	// cadence, and the cost stops scaling with chunk rate.
	cachedPartialRender   string
	cachedPartialResponse string
	cachedPartialAt       time.Time

	// activeThinking carries the in-flight reasoning content the
	// agent is producing during streaming. Rendered as a faint
	// "💭 <content>" block at the top of the streaming pane so the
	// user sees what the agent is thinking AS it thinks, not only
	// once the turn finalises into a committed thinking message.
	// Cleared at stream-state boundaries.
	activeThinking string
}

// NewView creates a new chat view with default dimensions and markdown rendering.
//
// Returns:
//   - An initialised View with default width (80), height (24), and markdown renderer.
//
// Side effects:
//   - None.
func NewView() *View {
	return &View{
		width:  defaultRenderWidth,
		height: 24,
	}
}

// Width returns the current width of the view.
//
// Returns:
//   - The current view width in columns.
//
// Side effects:
//   - None.
func (v *View) Width() int {
	return v.width
}

// Height returns the current height of the view.
//
// Returns:
//   - The current view height in rows.
//
// Side effects:
//   - None.
func (v *View) Height() int {
	return v.height
}

// SetDimensions sets the width and height of the view.
//
// Expected:
//   - width and height are positive integers.
//
// Side effects:
//   - Updates the view's width and height fields.
func (v *View) SetDimensions(width, height int) {
	if v.width != width {
		v.cachedRenderer = nil
		v.renderedMessages = nil
	}
	v.width = width
	v.height = height
}

// AddMessage appends a message to the view's message list.
//
// Expected:
//   - msg is a Message with Role and Content.
//
// Side effects:
//   - Appends the message to the messages slice.
func (v *View) AddMessage(msg Message) {
	v.messages = append(v.messages, msg)
}

// FlushPartialResponse commits any accumulated streaming response text as a
// permanent assistant message without ending the streaming session.
//
// This preserves chronological message ordering when a tool call arrives
// mid-stream: the response text accumulated so far is committed before the
// tool_call message, ensuring the rendered output matches the logical order
// in which content was produced.
//
// Expected:
//   - May be called at any time; is a no-op when response is empty.
//
// Side effects:
//   - If response is non-empty, appends an assistant message and clears response.
func (v *View) FlushPartialResponse() {
	if v.response == "" {
		return
	}
	// ModelID intentionally omitted — this message is mid-turn (a
	// committed partial response before a tool call). The model +
	// duration footer must appear ONLY on the final assistant message
	// of a turn, after every tool call and continuation has rendered.
	// Setting ModelID here would make the footer appear between the
	// streamed text and the inline tool widget — the "Reading… below
	// the footer" symptom. finaliseChunk (Done path) is the only
	// caller that sets ModelID on the appended message.
	v.messages = append(v.messages, Message{Role: "assistant", Content: v.response, AgentColor: v.agentColor})
	v.response = ""
	v.invalidatePartialCache()
}

// Messages returns a copy of the view's messages slice.
//
// Returns:
//   - A slice of Message values representing the chat history.
//
// Side effects:
//   - None.
func (v *View) Messages() []Message {
	return append([]Message(nil), v.messages...)
}

// StartStreaming marks the view as actively streaming and clears partial state.
//
// Side effects:
//   - Sets streaming to true, clears response and tool call state.
func (v *View) StartStreaming() {
	v.streaming = true
	v.response = ""
	v.toolCallName = ""
	v.toolCallStatus = ""
	v.toolCallArgs = nil
	v.toolCallResult = ""
	v.activeThinking = ""
	v.invalidatePartialCache()
}

// SetActiveThinking updates the live thinking buffer rendered at
// the top of the streaming pane during a turn. Mirrors the
// intent's i.activeThinking so the view can render reasoning as
// it streams without the intent reaching into view internals.
//
// Expected:
//   - thinking is the current accumulated reasoning text (may be
//     empty to clear the live block).
//
// Side effects:
//   - Updates v.activeThinking and invalidates the partial-render
//     cache so the next render reflects the new thinking text.
func (v *View) SetActiveThinking(thinking string) {
	if v.activeThinking == thinking {
		return
	}
	v.activeThinking = thinking
	v.invalidatePartialCache()
}

// invalidatePartialCache clears the throttled partial-render cache.
// Called at every stream-state boundary so a fresh stream starts
// without inheriting the previous turn's cached output.
//
// Side effects:
//   - Resets cachedPartialRender, cachedPartialResponse, cachedPartialAt.
func (v *View) invalidatePartialCache() {
	v.cachedPartialRender = ""
	v.cachedPartialResponse = ""
	v.cachedPartialAt = time.Time{}
}

// renderPartialResponseThrottled renders v.response through markdown
// at most ~10 fps regardless of how fast chunks arrive. Token-stream
// providers can deliver 14+ chunks/sec; without a throttle each
// chunk fires a fresh Glamour re-parse on the growing string and
// CPU spikes (~14% of wall-time on a typical 5k-char response). The
// throttle:
//
//   - Returns the cached render verbatim when v.response hasn't
//     changed since last call (tick chunks, no-op refreshes).
//   - Returns the cached render when v.response HAS changed but
//     less than 100ms have passed (chunk burst).
//   - Re-renders and updates the cache otherwise.
//
// On the final chunk (Done) finaliseChunk commits v.response to
// renderedMessages and clears the partial cache, so the user sees
// the fully-rendered final message at end-of-turn regardless of
// throttle state.
//
// Expected:
//   - v.response is non-empty.
//   - th is the active theme.
//   - width is the render width in columns.
//
// Returns:
//   - The (possibly cached) markdown-rendered partial response.
//
// Side effects:
//   - May update cachedPartialRender / cachedPartialResponse /
//     cachedPartialAt when a fresh render runs.
func (v *View) renderPartialResponseThrottled(th theme.Theme, width int) string {
	const minInterval = 100 * time.Millisecond

	if v.cachedPartialResponse == v.response && v.cachedPartialRender != "" {
		return v.cachedPartialRender
	}
	if v.cachedPartialRender != "" && !v.cachedPartialAt.IsZero() &&
		time.Since(v.cachedPartialAt) < minInterval {
		return v.cachedPartialRender
	}

	mw := widgets.NewMessageWidget("assistant", v.response, th)
	mw.SetAgentColor(v.agentColor)
	// ModelID intentionally omitted — see appendStreamingContent.
	if v.renderFunc != nil {
		mw.SetMarkdownRenderer(v.renderFunc)
	} else {
		mw.SetMarkdownRenderer(v.renderMarkdown)
	}
	rendered := mw.Render(width)

	v.cachedPartialRender = rendered
	v.cachedPartialResponse = v.response
	v.cachedPartialAt = time.Now()
	return rendered
}

// HandleChunk processes a streaming response chunk.
//
// Expected:
//   - content is the partial response text (may be empty).
//   - done indicates whether this is the final chunk.
//   - errMsg is a pre-formatted error string (empty if no error).
//   - toolCallName and toolCallStatus describe an active tool call (may be empty).
//
// Side effects:
//   - Accumulates content in response when not done.
//   - Finalises the response and appends to messages when done.
func (v *View) HandleChunk(content string, done bool, errMsg string, toolCallName string, toolCallStatus string) {
	if !done {
		v.streaming = true
		v.response += content
		if toolCallName != "" {
			v.toolCallName = toolCallName
			v.toolCallStatus = toolCallStatus
		}
	} else {
		v.finaliseChunk(content, errMsg)
		v.toolCallName = ""
		v.toolCallStatus = ""
	}
}

// finaliseChunk completes a streaming response and appends it to messages.
//
// Expected:
//   - content is the final chunk content (may be empty).
//   - errMsg is a pre-formatted error string (empty if no error).
//
// Side effects:
//   - Appends the final assistant message to messages.
//   - Resets streaming and response state.
func (v *View) finaliseChunk(content string, errMsg string) {
	fullContent := v.response + content
	if errMsg != "" {
		if fullContent != "" {
			fullContent += "\n\n" + errMsg
		} else {
			fullContent = errMsg
		}
	}
	if fullContent != "" {
		v.AddMessage(Message{
			Role:       "assistant",
			Content:    fullContent,
			AgentColor: v.agentColor,
			ModelID:    v.modelID,
			Duration:   v.turnDuration,
		})
	}
	v.streaming = false
	v.response = ""
	v.turnStartedAt = time.Time{}
	v.turnDuration = 0
	v.activeThinking = ""
	v.invalidatePartialCache()
}

// SetTurnStartedAt records the instant the current streaming turn
// began. Drives the live elapsed indicator at the bottom of the
// streaming block and provides the basis for the duration stamped
// onto the final assistant message.
//
// Expected:
//   - t is the wall-clock instant when the user submitted the
//     message that started this turn.
//
// Side effects:
//   - Updates v.turnStartedAt.
func (v *View) SetTurnStartedAt(t time.Time) {
	v.turnStartedAt = t
}

// SetTurnDuration records the elapsed time of the current turn so
// finaliseChunk stamps it onto the appended assistant Message.
// Callers compute this on the Done chunk and call SetTurnDuration
// before view.HandleChunk(..., done=true) runs.
//
// Expected:
//   - d is a non-negative duration. Zero leaves the field cleared.
//
// Side effects:
//   - Updates v.turnDuration.
func (v *View) SetTurnDuration(d time.Duration) {
	v.turnDuration = d
}

// TurnElapsed returns the time since the current turn began, or
// zero when no turn is in flight. Used by appendStreamingContent
// to render the live elapsed indicator.
//
// Returns:
//   - Time elapsed since the turn started, or 0 if not streaming.
//
// Side effects:
//   - None.
func (v *View) TurnElapsed() time.Duration {
	if v.turnStartedAt.IsZero() {
		return 0
	}
	return time.Since(v.turnStartedAt)
}

// IsStreaming returns whether the view is currently streaming a response.
//
// Returns:
//   - true if streaming, false otherwise.
//
// Side effects:
//   - None.
func (v *View) IsStreaming() bool {
	return v.streaming
}

// Response returns the current partial streaming response.
//
// Returns:
//   - The partial response string accumulated during streaming.
//
// Side effects:
//   - None.
func (v *View) Response() string {
	return v.response
}

// SetStreaming sets the streaming state and partial response content.
//
// Expected:
//   - streaming is a boolean indicating if streaming is active.
//   - response is the partial response content (may be empty).
//
// Side effects:
//   - Updates the streaming and response fields.
func (v *View) SetStreaming(streaming bool, response string) {
	v.streaming = streaming
	v.response = response
}

// SetToolCall sets the active tool call for rendering.
//
// Expected:
//   - name is the tool name (e.g., "web_search").
//   - status is one of "running", "complete", "error".
//
// Side effects:
//   - Updates the toolCallName and toolCallStatus fields.
func (v *View) SetToolCall(name, status string) {
	v.toolCallName = name
	v.toolCallStatus = status
}

// SetToolCallArgs records the raw provider tool-call arguments map for
// rendering by the inline ToolCallWidget. Callers should pair this with
// SetToolCall (or HandleChunk) so the widget has both the status and
// the args available when it renders.
//
// Expected:
//   - args is the provider.ToolCall.Arguments map; nil clears prior args.
//
// Side effects:
//   - Updates the toolCallArgs field.
func (v *View) SetToolCallArgs(args map[string]any) {
	v.toolCallArgs = args
}

// SetToolCallResult records the tool-result body (or error text) for
// rendering an inline preview under the completed/errored tool widget.
//
// Expected:
//   - result is the tool output as received from the engine; empty clears.
//
// Side effects:
//   - Updates the toolCallResult field.
func (v *View) SetToolCallResult(result string) {
	v.toolCallResult = result
}

// SetMarkdownRenderer sets a custom function for rendering markdown content.
//
// Expected:
//   - fn is a function that takes content and width, returns rendered string.
//
// Side effects:
//   - Updates the renderFunc field.
func (v *View) SetMarkdownRenderer(fn func(string, int) string) {
	v.renderFunc = fn
	v.cachedRenderer = nil
	v.renderedMessages = nil
}

// SetAgentColor stores the colour for assistant messages created by this view.
//
// Expected:
//   - c is a lipgloss.Color; zero value means use theme default.
//
// Side effects:
//   - Updates the agentColor field for future assistant messages.
func (v *View) SetAgentColor(c lipgloss.Color) {
	v.agentColor = c
}

// SetModelID stores the model identifier for assistant message footers.
//
// Expected:
//   - id is the model identifier string; empty means no footer.
//
// Side effects:
//   - Updates the modelID field for future assistant messages.
func (v *View) SetModelID(id string) {
	v.modelID = id
}

// SetTheme sets the theme for the chat view.
//
// Expected:
//   - th is a valid theme instance (can be nil).
//
// Side effects:
//   - Updates the embedded theme.Aware with the provided theme.
func (v *View) SetTheme(th theme.Theme) {
	v.Aware.SetTheme(th)
	v.cachedRenderer = nil
	v.renderedMessages = nil
}

// termRenderer returns a cached glamour renderer for the given width, recreating it when width changes.
//
// Expected:
//   - width is a positive integer representing the terminal column count.
//
// Returns:
//   - A glamour.TermRenderer ready for use, or nil if creation failed.
//
// Side effects:
//   - Creates and caches a new renderer when width changes.
func (v *View) termRenderer(width int) *glamour.TermRenderer {
	if v.cachedRenderer != nil && v.cachedRenderWidth == width {
		return v.cachedRenderer
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStylePath("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil
	}
	v.cachedRenderer = r
	v.cachedRenderWidth = width
	return r
}

// renderMarkdown renders markdown content using the cached glamour renderer.
//
// Expected:
//   - content is markdown text.
//   - width is the terminal width for word wrapping.
//
// Returns:
//   - Rendered markdown as a string, or original content if rendering fails.
//
// Side effects:
//   - May create and cache a new renderer when width changes.
func (v *View) renderMarkdown(content string, width int) string {
	r := v.termRenderer(width)
	if r == nil {
		return content
	}
	out, err := r.Render(content)
	if err != nil {
		return content
	}
	return strings.TrimRight(out, "\n")
}

// RenderContent renders the chat view content including messages and streaming response.
//
// Expected:
//   - width is the terminal width in columns.
//
// Returns:
//   - A rendered chat view string with messages and partial streaming response.
//
// Side effects:
//   - None.
func (v *View) RenderContent(width int) string {
	th := v.Theme()

	msgCount := len(v.messages)
	if cap(v.renderedMessages) < msgCount {
		extended := make([]string, len(v.renderedMessages), msgCount+8)
		copy(extended, v.renderedMessages)
		v.renderedMessages = extended
	}

	for idx := len(v.renderedMessages); idx < msgCount; idx++ {
		v.renderedMessages = append(v.renderedMessages, v.renderMessage(v.messages[idx], th, width))
	}

	// The previous unconditional last-message re-render during streaming
	// was redundant: the in-flight partial response and the active tool
	// call are emitted by appendStreamingContent below, not by mutating
	// the committed message cache. Re-rendering renderedMessages[-1] each
	// frame doubled the markdown-render work for every Bubble Tea tick
	// (10-50 Hz), which manifested as visible TUI lag. Keep the per-frame
	// work bounded to the streaming partials.

	estimatedSize := msgCount * 256
	var sb strings.Builder
	sb.Grow(estimatedSize)

	for _, rendered := range v.renderedMessages {
		sb.WriteString(rendered)
		sb.WriteString("\n\n")
	}

	v.appendStreamingContent(&sb, th, width)

	return sb.String()
}

// renderMessage renders a single message using the configured renderer.
//
// Expected:
//   - msg is a valid Message with Role and Content.
//   - th is the active theme (may be nil).
//   - width is the render width in columns.
//
// Returns:
//   - The rendered message string.
//
// Side effects:
//   - None.
func (v *View) renderMessage(msg Message, th theme.Theme, width int) string {
	mw := widgets.NewMessageWidget(msg.Role, msg.Content, th)
	mw.SetToolName(msg.ToolName)
	mw.SetToolInput(msg.ToolInput)
	mw.SetAgentColor(msg.AgentColor)
	mw.SetModelID(msg.ModelID)
	mw.SetDuration(msg.Duration)
	if v.renderFunc != nil {
		mw.SetMarkdownRenderer(v.renderFunc)
	} else {
		mw.SetMarkdownRenderer(v.renderMarkdown)
	}
	return mw.Render(width)
}

// SetTickFrame updates the current animation frame for spinners.
//
// Expected:
//   - frame is the current spinner frame.
//
// Returns:
//   - None.
//
// Side effects:
//   - Updates the tick frame used by spinner rendering.
func (v *View) SetTickFrame(frame int) {
	v.tickFrame = frame
}

// HandleDelegation updates the delegation status.
//
// Expected:
//   - info is nil or contains the current delegation state.
//
// Returns:
//   - None.
//
// Side effects:
//   - Updates the delegation display state.
//   - May append a system message when delegation completes or fails.
func (v *View) HandleDelegation(info *provider.DelegationInfo) {
	if info == nil {
		return
	}

	switch info.Status {
	case "completed", "failed":
		v.delegationInfo = nil
		v.FlushPartialResponse()
		block := NewCollapsibleDelegationBlock(info, v.Theme())
		v.AddMessage(Message{
			Role:    "system",
			Content: block.Render(),
		})
	default:
		v.delegationInfo = info
		v.streaming = true
	}
}

// appendStreamingContent adds the current streaming response to the rendered view.
//
// Expected:
//   - sb is ready for appended content.
//   - th is the active theme.
//   - width is the render width.
//
// Returns:
//   - None.
//
// Side effects:
//   - Writes streaming tool call and assistant content into sb when streaming is active.
func (v *View) appendStreamingContent(sb *strings.Builder, th theme.Theme, width int) {
	if !v.streaming && v.delegationInfo == nil {
		return
	}

	// Live thinking block at the very top of the streaming pane:
	// the agent's reasoning content as it streams. Faint + italic
	// so it visually recedes behind the actual response and tool
	// blocks below. Cleared once the turn finalises (flushThinking
	// commits a Role: "thinking" Message which then renders in the
	// committed history below the response).
	if v.activeThinking != "" {
		thinkingStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Faint(true).Italic(true)
		sb.WriteString(thinkingStyle.Render("💭 " + v.activeThinking))
		sb.WriteString("\n")
	}

	if v.delegationInfo != nil {
		if v.activeDelegationBlock == nil {
			v.activeDelegationBlock = NewCollapsibleDelegationBlock(v.delegationInfo, th)
		}
		v.activeDelegationBlock.SetFrame(v.tickFrame)
		sb.WriteString(v.activeDelegationBlock.Render())
		sb.WriteString("\n")
	}

	if v.response != "" {
		sb.WriteString(v.renderPartialResponseThrottled(th, width))
		sb.WriteString("\n")
	}
	if v.toolCallName != "" && v.toolCallStatus != "" {
		tcw := widgets.NewToolCallWidget(v.toolCallName, v.toolCallStatus).
			SetArgs(v.toolCallArgs).
			SetResult(v.toolCallResult)
		sb.WriteString(tcw.Render())
		sb.WriteString("\n")
	}
	// Live elapsed indicator at the bottom of the streaming block:
	// renders only while a turn is in flight. The spinner tick chain
	// re-renders the View every 100ms so the displayed seconds value
	// stays current. Sits BELOW any active tool widget so the user
	// reads "Reading… │ 5s elapsed" as a coherent in-progress block.
	if elapsed := v.TurnElapsed(); elapsed > 0 {
		muted := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Faint(true)
		sb.WriteString(muted.Render("⏱  " + formatStreamingElapsed(elapsed)))
		sb.WriteString("\n")
	}
}

// formatStreamingElapsed formats the live elapsed duration shown
// during streaming. Renders whole-second precision (the indicator
// updates ~10x per second via the spinner tick, but per-100ms
// jitter would be visual noise) for elapsed under a minute,
// minutes+seconds beyond.
//
// Expected:
//   - d is a non-negative duration.
//
// Returns:
//   - "<N>s" for elapsed under a minute.
//   - "<M>m <S>s" for elapsed of a minute or more.
//
// Side effects:
//   - None.
func formatStreamingElapsed(d time.Duration) string {
	if d < time.Second {
		return "0s"
	}
	if d < time.Minute {
		s := int(d.Seconds())
		return strconv.Itoa(s) + "s"
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return strconv.Itoa(m) + "m " + strconv.Itoa(s) + "s"
}

// ToggleActiveDelegationBlock toggles the collapsed state of the active delegation block.
//
// Expected:
//   - Called when user clicks on the delegation block area.
//
// Returns:
//   - None.
//
// Side effects:
//   - Toggles the collapsed state of the current active delegation block, if any.
func (v *View) ToggleActiveDelegationBlock() {
	if v.delegationInfo == nil {
		return
	}
	if v.activeDelegationBlock == nil {
		v.activeDelegationBlock = NewCollapsibleDelegationBlock(v.delegationInfo, v.Theme())
	}
	v.activeDelegationBlock.Toggle()
}

// ResultSend represents the result of sending a chat message.
type ResultSend struct {
	Message string
}

// ResultCancel represents the result of cancelling a chat operation.
type ResultCancel struct{}
