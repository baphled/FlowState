package chat

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tui/components/notification"
	tuiintents "github.com/baphled/flowstate/internal/tui/intents"
	"github.com/baphled/flowstate/internal/tui/uikit/navigation"
	chatview "github.com/baphled/flowstate/internal/tui/views/chat"
)

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

// AllViewMessagesForTest returns all messages from the chat view for test assertions.
func (i *Intent) AllViewMessagesForTest() []chatview.Message {
	return i.view.Messages()
}

// RenderedViewportContentForTest returns the rendered chat view content as
// it would be pushed into the message viewport via SetContent. Used by the
// long-stream regression spec to assert the full committed assistant
// response survives the throttled-render path end-to-end (intent →
// view.RenderContent), independent of Bubble Tea's viewport internals.
func (i *Intent) RenderedViewportContentForTest(width int) string {
	return i.view.RenderContent(width)
}

// SetMarkdownRendererForTest swaps the view's markdown renderer with the
// supplied function. Used by the long-stream regression spec to inject an
// identity renderer so the substring assertions are not foiled by
// glamour's per-character ANSI colour codes interleaving with the chunk
// markers.
func (i *Intent) SetMarkdownRendererForTest(fn func(string, int) string) {
	i.view.SetMarkdownRenderer(fn)
}

// HandleStreamChunkForTest exposes handleStreamChunk for test assertions.
func (i *Intent) HandleStreamChunkForTest(msg StreamChunkMsg) {
	i.handleStreamChunk(msg)
}

// HandleStreamChunkMsgForTest exposes handleStreamChunkMsg for test
// assertions. Used by the P7/C2 specs to exercise the full dispatch path —
// including the premature-delegation-misfire detector — without standing up
// a provider stream.
func (i *Intent) HandleStreamChunkMsgForTest(msg StreamChunkMsg) tea.Cmd {
	return i.handleStreamChunkMsg(msg)
}

// SetTurnUserMessageForTest seeds the per-turn user message the chat intent
// uses to detect premature delegation misfires (P7/C2). Production code
// populates this field from sendMessage; tests use this hook to bypass the
// full send path and drive only the chunk handler.
func SetTurnUserMessageForTest(i *Intent, msg string) {
	i.turnUserMessage = msg
	i.turnHasText = false
	i.prematureWarningFired = false
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

// SetAtBottomForTest seeds i.atBottom so the end-of-stream scroll
// heuristic spec can simulate a user-scrolled state without driving a
// real key/mouse event through the viewport.
func (i *Intent) SetAtBottomForTest(v bool) {
	i.atBottom = v
}

// SetLastUserScrollAtForTest seeds i.lastUserScrollAt so the
// end-of-stream scroll heuristic spec can stage "user actively
// scrolled N seconds ago" or "user has not scrolled" cases without
// timing dependencies.
func (i *Intent) SetLastUserScrollAtForTest(t time.Time) {
	i.lastUserScrollAt = t
}

// LastUserScrollAtForTest returns i.lastUserScrollAt so a spec can
// assert the field is updated by key/mouse handlers.
func (i *Intent) LastUserScrollAtForTest() time.Time {
	return i.lastUserScrollAt
}

// SetNotificationManagerForTest swaps the notification manager so the
// heuristic spec can capture surfaced notifications.
func (i *Intent) SetNotificationManagerForTest(mgr notification.Manager) {
	i.notificationManager = mgr
}

// MsgViewportDebugForTest returns YOffset, Height, and the underlying
// content for inspection. Used by the long-stream viewport reproducer
// to pinpoint whether truncation lives in RenderContent, in viewport
// height computation, or in the GotoBottom call site.
func (i *Intent) MsgViewportDebugForTest() (yOffset, height int, content string) {
	if i.msgViewport == nil {
		return 0, 0, ""
	}
	return i.msgViewport.YOffset, i.msgViewport.Height, i.msgViewport.View()
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

// SetSessionViewerForTest injects a session viewer viewport for test assertions.
func SetSessionViewerForTest(i *Intent, sessionID, content string, width, height int) {
	svpHeight := height - 6
	if svpHeight < 1 {
		svpHeight = 1
	}
	vp := viewport.New(width, svpHeight)
	vp.SetContent(content)
	i.sessionViewport = &vp
	i.sessionViewerActive = true
	i.sessionViewerID = sessionID
}

// IsSessionViewerActive returns whether the session viewer is currently active.
func IsSessionViewerActive(i *Intent) bool {
	return i.sessionViewerActive
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
	svpHeight := i.height - 6
	if svpHeight < 1 {
		svpHeight = 1
	}
	vp := viewport.New(i.width, svpHeight)
	vp.SetContent(content)
	i.sessionViewport = &vp
	i.sessionViewerActive = true
	i.sessionViewerID = sessionID
	if len(sessionID) >= 8 {
		i.breadcrumbPath = "Chat > " + sessionID[:8]
	} else {
		i.breadcrumbPath = "Chat > " + sessionID
	}
	i.cachedScreenLayout = nil
}

// WaitForCompletionForTest executes the waitForCompletion command synchronously
// and returns the resulting tea.Msg, enabling direct inspection in tests.
func (i *Intent) WaitForCompletionForTest() tea.Msg {
	cmd := i.waitForCompletion()
	return cmd()
}

// ResponseTokenCountForTest returns the accumulated response token count for test assertions.
func (i *Intent) ResponseTokenCountForTest() int {
	return i.responseTokenCount
}

// SyncStatusBarForTest exposes syncStatusBar for test assertions.
func (i *Intent) SyncStatusBarForTest() {
	i.syncStatusBar()
}

// EventNotifChanForTest returns the event bus notification channel for test assertions.
func (i *Intent) EventNotifChanForTest() chan EventBusNotificationMsg {
	return i.eventNotifChan
}

// SetEventNotifChanForTest sets the event bus notification channel for testing.
func (i *Intent) SetEventNotifChanForTest(ch chan EventBusNotificationMsg) {
	i.eventNotifChan = ch
}

// HandleEventBusNotificationForTest exposes handleEventBusNotification for test assertions.
func (i *Intent) HandleEventBusNotificationForTest(msg EventBusNotificationMsg) tea.Cmd {
	return i.handleEventBusNotification(msg)
}

// SetLastEscTimeForTest sets the last Esc press timestamp for test assertions.
func (i *Intent) SetLastEscTimeForTest(t time.Time) {
	i.lastEscTime = t
}

// LastEscTimeForTest returns the last Esc press timestamp for test assertions.
func (i *Intent) LastEscTimeForTest() time.Time {
	return i.lastEscTime
}

// SwarmStoreForTest returns the swarm event store for test assertions.
func (i *Intent) SwarmStoreForTest() streaming.SwarmEventStore {
	return i.swarmStore
}

// SetSwarmStoreForTest replaces the swarm event store for test isolation.
func (i *Intent) SetSwarmStoreForTest(store streaming.SwarmEventStore) {
	i.swarmStore = store
}

// SwarmEventFromChunkForTest exposes swarmEventFromChunk for test assertions.
func SwarmEventFromChunkForTest(msg StreamChunkMsg, fallbackAgent string) (streaming.SwarmEvent, bool) {
	return swarmEventFromChunk(msg, fallbackAgent)
}

// InstallStreamCancelForTest installs a cancellable context cancel func on the
// intent and returns a pointer to a flag that will flip to true when the cancel
// function is invoked. Useful for asserting double-Esc cancellation without a
// live stream. The matching streamCtx is also installed so readNextChunk's
// ctx-aware select path exercises the same context the cancel func governs.
func (i *Intent) InstallStreamCancelForTest() *bool {
	cancelled := false
	ctx, cancel := context.WithCancel(context.Background())
	i.streamCtx = ctx
	i.streamCancel = func() {
		cancelled = true
		cancel()
	}
	return &cancelled
}

// StreamCancelClearedForTest reports whether the stream cancel func has been
// cleared (i.e. cancelActiveStream ran).
func (i *Intent) StreamCancelClearedForTest() bool {
	return i.streamCancel == nil
}

// CancelActiveStreamForTest exposes cancelActiveStream for test assertions.
// Used by the P1/D1 stream-cancel specs to simulate a double-Esc interrupt
// without a live stream pipeline.
func (i *Intent) CancelActiveStreamForTest() {
	i.cancelActiveStream()
}

// FireStreamCancelForTest invokes the stored streamCancel function without
// running the full cancelActiveStream cleanup, leaving streamCtx populated
// so the D1 specs can assert the ctx-aware select observes Done() on a
// context that is still addressable from the reader path.
func (i *Intent) FireStreamCancelForTest() {
	if i.streamCancel != nil {
		i.streamCancel()
	}
}

// HandleEscapeKeyForTest exposes handleEscapeKey for the D1-suppress specs so
// they can drive the real double-Esc path (not just cancelActiveStream).
// The escape key handler is the only code path that both sets userCancelled
// and invokes cancelActiveStream, so tests exercising "post-cancel chunks
// must not render" must go through it to catch regressions in ordering or in
// the userCancelled flag.
func (i *Intent) HandleEscapeKeyForTest() tea.Cmd {
	return i.handleEscapeKey()
}

// BeginTurnForTest invokes the production beginTurn helper so the D1
// stall-regression specs exercise the exact same per-turn reset path that
// sendMessage runs before starting a fresh stream. Intentionally delegates
// to the production method rather than mirroring its body, so a regression
// that drops the userCancelled clear from beginTurn fails both the
// production sendMessage path and this test's assertions.
//
// Expected:
//   - userMessage is the user input the production call would capture for
//     the premature-delegation-misfire detector. Tests that do not care
//     about that detector pass an empty string.
//
// Side effects:
//   - Whatever beginTurn does: clears turnUserMessage, turnHasText,
//     prematureWarningFired, and userCancelled.
func (i *Intent) BeginTurnForTest(userMessage string) {
	i.beginTurn(userMessage)
}

// SessionTrailForTest returns the session trail for test assertions.
func SessionTrailForTest(i *Intent) *navigation.SessionTrail {
	return i.sessionTrail
}

// RefreshSessionTrailForTest exposes refreshSessionTrail for test assertions.
func RefreshSessionTrailForTest(i *Intent) {
	i.refreshSessionTrail()
}

// SetSessionManagerForTest sets the session manager for testing purposes.
func (i *Intent) SetSessionManagerForTest(mgr SessionManager) {
	i.sessionManager = mgr
}

// SetSessionIDForTest sets the session ID for testing purposes.
func (i *Intent) SetSessionIDForTest(id string) {
	i.sessionID = id
}

// SessionTrailHeightForTest returns the session trail height for test assertions.
func (i *Intent) SessionTrailHeightForTest() int {
	return i.sessionTrailHeight()
}

// RenderSessionTrailLineForTest exposes renderSessionTrailLine for test assertions.
func (i *Intent) RenderSessionTrailLineForTest() string {
	return i.renderSessionTrailLine()
}

// SetChildSessionListerForTest sets the child session lister for testing purposes.
func SetChildSessionListerForTest(i *Intent, lister SessionChildLister) {
	i.childSessionLister = lister
}

// OpenEventDetailsForTest exposes openEventDetails for test assertions.
func (i *Intent) OpenEventDetailsForTest() tea.Cmd {
	return i.openEventDetails()
}

// RecordSwarmEventForTest exposes recordSwarmEvent for test assertions.
// The returned tea.Cmd resolves to a SwarmEventAppendedMsg when a
// SwarmEvent was appended, or nil when the chunk carried no actionable
// metadata. Tests use this to verify the P3 B7 dispatch contract without
// reaching into the full streaming pipeline.
func (i *Intent) RecordSwarmEventForTest(msg StreamChunkMsg) tea.Cmd {
	return i.recordSwarmEvent(msg)
}

// SwarmVisibleTypesForTest returns a defensive copy of the chat intent's
// authoritative swarmVisibleTypes map for test assertions. Tests use this
// to verify the P3 A3 contract that the intent holds visibility state and
// reasserts it on every render.
func (i *Intent) SwarmVisibleTypesForTest() map[streaming.SwarmEventType]bool {
	if i.swarmVisibleTypes == nil {
		return nil
	}
	out := make(map[streaming.SwarmEventType]bool, len(i.swarmVisibleTypes))
	for k, v := range i.swarmVisibleTypes {
		out[k] = v
	}
	return out
}

// ShowModalMsgForTest is a type alias for tuiintents.ShowModalMsg exported for
// test assertions in external test packages.
type ShowModalMsgForTest = tuiintents.ShowModalMsg

// SwarmFilterProfileAllForTest exposes the profileAll sentinel for P11
// cycle-order assertions. Tests compare against this rather than hard-coded
// integers so the cycle can be reordered without touching every test file.
func SwarmFilterProfileAllForTest() int {
	return int(swarmFilterProfileAll)
}

// SwarmFilterProfileToolsOnlyForTest exposes the profileToolsOnly sentinel
// for P11 cycle-order assertions.
func SwarmFilterProfileToolsOnlyForTest() int {
	return int(swarmFilterProfileToolsOnly)
}

// SwarmFilterProfileDelegationsOnlyForTest exposes the
// profileDelegationsOnly sentinel for P11 cycle-order assertions.
func SwarmFilterProfileDelegationsOnlyForTest() int {
	return int(swarmFilterProfileDelegationsOnly)
}

// SwarmFilterProfileForTest returns the chat intent's current
// swarmFilterProfile as an int so test code in an external package can
// compare it against the *ForTest sentinels.
func (i *Intent) SwarmFilterProfileForTest() int {
	return int(i.swarmFilterProfile)
}

// SetDelegationPickerForTest injects a fully-constructed DelegationPickerModal
// onto the intent so external specs can drive the picker's key handling
// (left/right + up/down + j/k/h/l) without standing up a session manager
// or calling openDelegationPicker.
func (i *Intent) SetDelegationPickerForTest(modal *chatview.DelegationPickerModal) {
	i.delegationPickerModal = modal
}

// DelegationPickerSelectedAgentForTest returns the AgentID of the currently
// selected session in the picker, or the empty string when no picker is
// open or the picker has no sessions. Used by the left/right cycling
// specs to assert cursor movement.
func (i *Intent) DelegationPickerSelectedAgentForTest() string {
	if i.delegationPickerModal == nil {
		return ""
	}
	sel := i.delegationPickerModal.Selected()
	if sel == nil {
		return ""
	}
	return sel.AgentID
}
