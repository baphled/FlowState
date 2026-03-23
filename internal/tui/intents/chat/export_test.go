package chat

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/agent"
	tuiintents "github.com/baphled/flowstate/internal/tui/intents"
	chatview "github.com/baphled/flowstate/internal/tui/views/chat"
)

// FormatErrorMessageForTest exposes FormatErrorMessage for test assertions.
func FormatErrorMessageForTest(err error) string {
	return chatview.FormatErrorMessage(err)
}

// SetStreamingForTest sets the streaming state for testing purposes.
func (i *Intent) SetStreamingForTest(streaming bool) {
	i.view.SetStreaming(streaming, "")
}

// ProviderNameForTest returns the current provider name for test assertions.
func (i *Intent) ProviderNameForTest() string {
	return i.providerName
}

// ModelNameForTest returns the current model name for test assertions.
func (i *Intent) ModelNameForTest() string {
	return i.modelName
}

// TODO: SetStreamChanForTest and ReadNextChunkForTest are broken stubs.
// The Intent struct does not have streamChan field or readNextChunk method.
// These need to be implemented when streaming is fully integrated.
// See: intent_test.go and integration_test.go for usage.

// SetStreamChanForTest sets the stream channel for testing readNextChunk.
// func (i *Intent) SetStreamChanForTest(ch <-chan provider.StreamChunk) {
// 	i.streamChan = ch
// }

// ReadNextChunkForTest exposes readNextChunk for test assertions.
// func (i *Intent) ReadNextChunkForTest() tea.Msg {
// 	return i.readNextChunk()
// }

// SetAgentRegistryForTest sets the agent registry for testing purposes.
func (i *Intent) SetAgentRegistryForTest(reg *agent.Registry) {
	i.agentRegistry = reg
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
