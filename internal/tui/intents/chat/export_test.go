package chat

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tui/components/notification"
	tuiintents "github.com/baphled/flowstate/internal/tui/intents"
	chatview "github.com/baphled/flowstate/internal/tui/views/chat"
)

// SetRunningInTestsForTest toggles test-mode behaviour for chat intent initialisation.
func SetRunningInTestsForTest(running bool) {
	runningInTests = running
}

// FormatErrorMessageForTest exposes FormatErrorMessage for test assertions.
func FormatErrorMessageForTest(err error) string {
	return chatview.FormatErrorMessage(err)
}

// SetStreamingForTest sets the streaming state for testing purposes.
func (i *Intent) SetStreamingForTest(isStreaming bool) {
	i.view.SetStreaming(isStreaming, "")
}

// ProviderNameForTest returns the current provider name for test assertions.
func (i *Intent) ProviderNameForTest() string {
	return i.providerName
}

// ModelNameForTest returns the current model name for test assertions.
func (i *Intent) ModelNameForTest() string {
	return i.modelName
}

// SetStreamChanForTest sets the stream channel for testing readNextChunk.
func (i *Intent) SetStreamChanForTest(ch <-chan provider.StreamChunk) {
	i.streamChan = ch
}

// ReadNextChunkForTest exposes readNextChunk for test assertions.
func (i *Intent) ReadNextChunkForTest() tea.Msg {
	return i.readNextChunk()
}

// ReadStreamChunkForTest exposes readStreamChunk for test assertions.
func ReadStreamChunkForTest(ch <-chan provider.StreamChunk) StreamChunkMsg {
	return readStreamChunk(ch)
}

// SetAgentRegistryForTest sets the agent registry for testing purposes.
func (i *Intent) SetAgentRegistryForTest(reg *agent.Registry) {
	i.agentRegistry = reg
}

// ViewportHeight returns the current message viewport height for test assertions.
func (i *Intent) ViewportHeight() int {
	if i.msgViewport == nil {
		return 0
	}
	return i.msgViewport.Height
}

// ViewportYOffsetForTest returns the current message viewport Y offset for test assertions.
func (i *Intent) ViewportYOffsetForTest() int {
	if i.msgViewport == nil {
		return -1 // Return -1 to indicate nil viewport
	}
	return i.msgViewport.YOffset
}

// ViewportReadyForTest returns whether the viewport is ready for test assertions.
func (i *Intent) ViewportReadyForTest() bool {
	return i.vpReady && i.msgViewport != nil
}

// ViewportForTest returns the viewport itself for direct testing.
func (i *Intent) ViewportForTest() *viewport.Model {
	return i.msgViewport
}

// ViewportContentLineCountForTest returns the number of lines in viewport content for test assertions.
func (i *Intent) ViewportContentLineCountForTest() int {
	if i.msgViewport == nil {
		return 0
	}
	// The viewport.Model has a private 'lines' field, so we can't access it directly.
	// As a workaround, we count newlines in the viewport View() output.
	return strings.Count(i.msgViewport.View(), "\n") + 1
}

// DetectAgentFromInputForTest exposes detectAgentFromInput for test assertions.
func DetectAgentFromInputForTest(message string) string {
	return detectAgentFromInput(message)
}

// SimulateModalModelSelectionForTest calls openModelSelector, executes the Cmd
// to get the models.Intent, then simulates selecting the first model in the
// first group by pressing Enter twice (expand group, then select model).
// Returns true if the modal was successfully opened and model selected.
func (i *Intent) SimulateModalModelSelectionForTest() bool {
	cmd := i.openModelSelector()
	if cmd == nil {
		return false
	}
	msg := cmd()
	if msg == nil {
		return false
	}
	showMsg, ok := msg.(tuiintents.ShowModalMsg)
	if !ok || showMsg.Modal == nil {
		return false
	}
	modal := showMsg.Modal
	modal.Update(tea.KeyMsg{Type: tea.KeyEnter})
	modal.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return true
}

// OpenAgentPickerForTest exposes openAgentPicker for test assertions.
func (i *Intent) OpenAgentPickerForTest() tea.Cmd {
	return i.openAgentPicker()
}

// SimulateAgentPickerSelectionForTest calls openAgentPicker, executes the Cmd
// to get the agentpicker.Intent, then simulates selecting the given agent by
// navigating down and pressing Enter. Returns true if selection succeeded.
func (i *Intent) SimulateAgentPickerSelectionForTest(targetIndex int) bool {
	cmd := i.openAgentPicker()
	if cmd == nil {
		return false
	}
	msg := cmd()
	if msg == nil {
		return false
	}
	showMsg, ok := msg.(tuiintents.ShowModalMsg)
	if !ok || showMsg.Modal == nil {
		return false
	}
	modal := showMsg.Modal
	for range targetIndex {
		modal.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	modal.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return true
}

// SetSessionStoreForTest sets the session store for testing purposes.
func (i *Intent) SetSessionStoreForTest(store SessionLister) {
	i.sessionStore = store
}

// SessionIDForTest returns the current session ID for test assertions.
func (i *Intent) SessionIDForTest() string {
	return i.sessionID
}

// SetEngineForTest sets the engine for testing purposes.
func (i *Intent) SetEngineForTest(eng *engine.Engine) {
	i.engine = eng
}

// SaveSessionForTest exposes saveSession for test assertions.
func (i *Intent) SaveSessionForTest() tea.Cmd {
	return i.saveSession()
}

// ActiveToolCallForTest returns the current activeToolCall value for test assertions.
func (i *Intent) ActiveToolCallForTest() string {
	return i.activeToolCall
}

// AllViewMessagesForTest returns all messages from the chat view for test assertions.
func (i *Intent) AllViewMessagesForTest() []chatview.Message {
	return i.view.Messages()
}

// HandleStreamChunkForTest exposes handleStreamChunk for test assertions.
func (i *Intent) HandleStreamChunkForTest(msg StreamChunkMsg) {
	i.handleStreamChunk(msg)
}

// ToolCallSummaryForTest exposes toolCallSummary for test assertions.
func ToolCallSummaryForTest(name string, args map[string]interface{}) string {
	return toolCallSummary(name, args)
}

// IsReadToolCallForTest exposes isReadToolCall for test assertions.
func IsReadToolCallForTest(name string) bool {
	return isReadToolCall(name)
}

// ToolResultMessageForTest exposes toolResultMessage for test assertions.
func ToolResultMessageForTest(toolName, result string, isError bool) chatview.Message {
	return toolResultMessage(toolName, result, isError)
}

// AtBottomForTest returns whether the viewport is tracking the bottom position for test assertions.
func (i *Intent) AtBottomForTest() bool {
	return i.atBottom
}

// NotificationManagerForTest returns the notification manager for test assertions.
func (i *Intent) NotificationManagerForTest() notification.Manager {
	return i.notificationManager
}

// NotificationsViewForTest returns the rendered notification view for test assertions.
func (i *Intent) NotificationsViewForTest() string {
	return i.notifications.View()
}

// StreamingEventMetaForTest exposes streamingEventMeta for test assertions.
func StreamingEventMetaForTest(eventType string) (string, notification.Level) {
	return streamingEventMeta(eventType)
}

// SetBackgroundManagerForTest sets the background manager for testing purposes.
func (i *Intent) SetBackgroundManagerForTest(mgr *engine.BackgroundTaskManager) {
	i.backgroundManager = mgr
}

// BackgroundManagerForTest returns the background manager for test assertions.
func (i *Intent) BackgroundManagerForTest() *engine.BackgroundTaskManager {
	return i.backgroundManager
}

// HandleBackgroundTaskCompletedForTest exposes handleBackgroundTaskCompleted for test assertions.
func (i *Intent) HandleBackgroundTaskCompletedForTest(msg BackgroundTaskCompletedMsg) tea.Cmd {
	return i.handleBackgroundTaskCompleted(msg)
}

// CompletionChanForTest returns the completion channel for test assertions.
func (i *Intent) CompletionChanForTest() <-chan streaming.CompletionNotificationEvent {
	return i.completionChan
}

// FormatCompletionReminderForTest exposes formatCompletionReminder for test assertions.
func FormatCompletionReminderForTest(msg BackgroundTaskCompletedMsg) string {
	return formatCompletionReminder(msg)
}

// SplitToolSummaryForTest exposes splitToolSummary for test assertions.
func SplitToolSummaryForTest(summary string) (name, input string) {
	return splitToolSummary(summary)
}

// SetModelNameForTest sets the model name for testing mid-stream model changes.
func (i *Intent) SetModelNameForTest(name string) {
	i.modelName = name
}

// RenderSessionContentForTest exposes renderSessionContent for external test assertions.
func (i *Intent) RenderSessionContentForTest(sess *session.Session) string {
	return i.renderSessionContent(sess)
}

// SetSessionViewerModalForTest injects a session viewer modal for test assertions.
func SetSessionViewerModalForTest(i *Intent, sessionID, content string, width, height int) {
	i.sessionViewerModal = chatview.NewSessionViewerModal(sessionID, content, width, height)
}

// BreadcrumbPathForTest returns the current breadcrumb path for test assertions.
func BreadcrumbPathForTest(i *Intent) string {
	return i.breadcrumbPath
}

// SetBreadcrumbPathForTest sets the breadcrumb path for test assertions.
func SetBreadcrumbPathForTest(i *Intent, path string) {
	i.breadcrumbPath = path
	i.cachedScreenLayout = nil
}

// SimulateDelegationEnterForTest simulates selecting a session from the delegation picker for test assertions.
func SimulateDelegationEnterForTest(i *Intent, sessionID, content string) {
	i.sessionViewerModal = chatview.NewSessionViewerModal(sessionID, content, i.width, i.height)
	if len(sessionID) >= 8 {
		i.breadcrumbPath = "Chat > " + sessionID[:8]
	} else {
		i.breadcrumbPath = "Chat > " + sessionID
	}
	i.cachedScreenLayout = nil
}
