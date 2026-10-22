//go:build e2e

package support

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
)

// StreamingToolOutputSteps holds state for streaming tool output BDD scenarios.
type StreamingToolOutputSteps struct {
	toolOutput     string
	toolError      error
	chunks         []provider.StreamChunk
	hasToolResult  bool
	toolResultText string

	pendingToolCall    *provider.ToolCall
	captureManager     *session.Manager
	captureSessionID   string
	persistedToolInput string

	replayStreamer  *toolInputHistorySeeder
	replayManager   *session.Manager
	replaySessionID string
}

// RegisterStreamingToolOutputSteps registers step definitions for streaming tool output scenarios.
//
// Expected:
//   - sc is a valid godog ScenarioContext for step registration.
//   - s is a non-nil StreamingToolOutputSteps instance.
//
// Side effects:
//   - Registers step definitions on the provided scenario context.
func RegisterStreamingToolOutputSteps(sc *godog.ScenarioContext, s *StreamingToolOutputSteps) {
	sc.Step(`^the engine executes a tool that produces output$`, s.theEngineExecutesAToolThatProducesOutput)
	sc.Step(`^the stream is processed$`, s.theStreamIsProcessed)
	sc.Step(`^the tool result should be visible in the chat$`, s.theToolResultShouldBeVisibleInTheChat)
	sc.Step(`^the engine executes a tool with multiple arguments$`, s.theEngineExecutesAToolWithMultipleArguments)
	sc.Step(`^the engine executes a tool with sensitive arguments$`, s.theEngineExecutesAToolWithSensitiveArguments)
	sc.Step(`^the persisted tool call input should contain every argument as JSON$`, s.thePersistedToolCallInputShouldContainEveryArgumentAsJSON)
	sc.Step(`^the persisted tool call input should redact the sensitive value$`, s.thePersistedToolCallInputShouldRedactTheSensitiveValue)
	sc.Step(`^a session was saved with whole JSON tool input$`, s.aSessionWasSavedWithWholeJSONToolInput)
	sc.Step(`^a session was saved with legacy display-string tool input$`, s.aSessionWasSavedWithLegacyDisplayStringToolInput)
	sc.Step(`^the session history is replayed to the provider$`, s.theSessionHistoryIsReplayedToTheProvider)
	sc.Step(`^the tool call arguments should be restored as structured tool calls$`, s.theToolCallArgumentsShouldBeRestoredAsStructuredToolCalls)
	sc.Step(`^the tool input should replay as descriptive text in brackets$`, s.theToolInputShouldReplayAsDescriptiveTextInBrackets)
}

// theEngineExecutesAToolThatProducesOutput simulates a tool execution that produces output.
//
// Expected:
//   - No prior state is required.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Sets toolOutput to "Command executed successfully".
func (s *StreamingToolOutputSteps) theEngineExecutesAToolThatProducesOutput() error {
	s.toolOutput = "Command executed successfully"
	s.toolError = nil
	return nil
}

// theStreamIsProcessed simulates the engine emitting a tool result chunk.
// This models what streamWithToolLoop should do: after executeToolCall returns a result,
// emit it as a StreamChunk with ToolResult field before continuing the loop.
//
// Expected:
//   - theEngineExecutesAToolThatProducesOutput has been called.
//
// Returns:
//   - nil on success.
//   - An error if the tool result cannot be converted to a chunk.
//
// Side effects:
//   - Populates s.chunks with a mock StreamChunk containing ToolResult.
func (s *StreamingToolOutputSteps) theStreamIsProcessed() error {
	if s.pendingToolCall != nil {
		return s.processToolCallStream()
	}
	if s.toolOutput == "" && s.toolError == nil {
		return errors.New("no tool output or error set")
	}

	resultContent := s.toolOutput
	isError := s.toolError != nil
	if isError {
		resultContent = "Error: " + s.toolError.Error()
	}

	chunk := provider.StreamChunk{
		EventType: "tool_result",
		ToolResult: &provider.ToolResultInfo{
			Content: resultContent,
			IsError: isError,
		},
	}

	s.chunks = append(s.chunks, chunk)
	return nil
}

// theToolResultShouldBeVisibleInTheChat asserts that a tool result chunk was emitted.
//
// Expected:
//   - theStreamIsProcessed has been called.
//
// Returns:
//   - An error if no tool result chunk is found.
//
// Side effects:
//   - None.
func (s *StreamingToolOutputSteps) theToolResultShouldBeVisibleInTheChat() error {
	for i := range s.chunks {
		if s.chunks[i].EventType == "tool_result" && s.chunks[i].ToolResult != nil && s.chunks[i].ToolResult.Content != "" {
			s.hasToolResult = true
			s.toolResultText = s.chunks[i].ToolResult.Content
			return nil
		}
	}
	return fmt.Errorf("expected a tool_result chunk with non-empty content, got %d chunks", len(s.chunks))
}

// theEngineExecutesAToolWithMultipleArguments simulates a tool execution whose
// call carries more than one argument, so the persisted input contract can be
// asserted against the whole argument map.
//
// Expected:
//   - No prior state is required.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Sets pendingToolCall to a bash call with command, timeout, and workdir arguments.
func (s *StreamingToolOutputSteps) theEngineExecutesAToolWithMultipleArguments() error {
	s.pendingToolCall = &provider.ToolCall{
		Name: "bash",
		Arguments: map[string]any{
			"command": "ls -la",
			"timeout": 30,
			"workdir": "/tmp",
		},
	}
	return nil
}

// theEngineExecutesAToolWithSensitiveArguments simulates a tool execution whose
// call mixes a sensitive credential argument with a non-sensitive one, so the
// redaction contract can be asserted on the persisted input.
//
// Expected:
//   - No prior state is required.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Sets pendingToolCall to an external_api call with api_key and query arguments.
func (s *StreamingToolOutputSteps) theEngineExecutesAToolWithSensitiveArguments() error {
	s.pendingToolCall = &provider.ToolCall{
		Name: "external_api",
		Arguments: map[string]any{
			"api_key": "sk-test-do-not-leak",
			"query":   "hello",
		},
	}
	return nil
}

// ensureCaptureManager lazily builds the session manager used by the whole
// tool input capture scenarios, mirroring the failure_reason steps fixture.
//
// Returns:
//   - An error when the sessions directory or session cannot be created.
//
// Side effects:
//   - Initialises captureManager and captureSessionID on first call.
func (s *StreamingToolOutputSteps) ensureCaptureManager() error {
	if s.captureManager != nil {
		return nil
	}
	dir, err := os.MkdirTemp("", "flowstate-tool-input-*")
	if err != nil {
		return fmt.Errorf("creating sessions dir: %w", err)
	}
	s.captureManager = session.NewManager(nil)
	s.captureManager.SetSessionsDir(dir)
	sess, err := s.captureManager.CreateSession("agent-tool-input")
	if err != nil {
		return fmt.Errorf("creating session: %w", err)
	}
	s.captureSessionID = sess.ID
	return nil
}

// processToolCallStream drives the pending tool call through the real
// accumulator so the persisted tool_call message reflects the production
// capture seam, then records its ToolInput for the assertion steps.
//
// Returns:
//   - An error when the stream cannot be persisted or no tool_call message lands.
//
// Side effects:
//   - Appends a tool_call message via the accumulator and sets persistedToolInput.
func (s *StreamingToolOutputSteps) processToolCallStream() error {
	if err := s.ensureCaptureManager(); err != nil {
		return err
	}
	rawCh := make(chan provider.StreamChunk, 2)
	rawCh <- provider.StreamChunk{ToolCall: s.pendingToolCall}
	rawCh <- provider.StreamChunk{Done: true}
	close(rawCh)

	out := session.AccumulateStream(context.Background(), s.captureManager, s.captureSessionID, "agent-tool-input", rawCh)
	for chunk := range out {
		_ = chunk
	}

	sess, err := s.captureManager.GetSession(s.captureSessionID)
	if err != nil {
		return fmt.Errorf("loading persisted session: %w", err)
	}
	for _, m := range sess.Messages {
		if m.Role == "tool_call" {
			s.persistedToolInput = m.ToolInput
			return nil
		}
	}
	return errors.New("no tool_call message was persisted")
}

// thePersistedToolCallInputShouldContainEveryArgumentAsJSON asserts the whole
// argument map survives capture as deterministic sorted-key compact JSON.
//
// Returns:
//   - An error when the persisted input differs from the expected JSON.
func (s *StreamingToolOutputSteps) thePersistedToolCallInputShouldContainEveryArgumentAsJSON() error {
	expected := `{"command":"ls -la","timeout":30,"workdir":"/tmp"}`
	if s.persistedToolInput != expected {
		return fmt.Errorf("expected persisted tool call input %q, got %q", expected, s.persistedToolInput)
	}
	return nil
}

// thePersistedToolCallInputShouldRedactTheSensitiveValue asserts the sensitive
// argument is redacted while the non-sensitive argument survives, as
// deterministic sorted-key compact JSON.
//
// Returns:
//   - An error when the persisted input leaks the secret, drops the
//     non-sensitive argument, or differs from the expected JSON.
func (s *StreamingToolOutputSteps) thePersistedToolCallInputShouldRedactTheSensitiveValue() error {
	expected := `{"api_key":"[REDACTED]","query":"hello"}`
	if s.persistedToolInput != expected {
		return fmt.Errorf("expected persisted tool call input %q, got %q", expected, s.persistedToolInput)
	}
	if strings.Contains(s.persistedToolInput, "sk-test-do-not-leak") {
		return errors.New("persisted tool call input leaked the sensitive value")
	}
	return nil
}

// aSessionWasSavedWithWholeJSONToolInput restores a session whose tool_call
// message carries the whole-JSON ToolInput the capture contract produces.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Initialises replayManager, replayStreamer, and replaySessionID.
func (s *StreamingToolOutputSteps) aSessionWasSavedWithWholeJSONToolInput() error {
	return s.saveReplaySessionWithToolInput(`{"command":"ls -la","timeout":30}`)
}

// aSessionWasSavedWithLegacyDisplayStringToolInput restores a session whose
// tool_call message carries a pre-contract display-string ToolInput.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Initialises replayManager, replayStreamer, and replaySessionID.
func (s *StreamingToolOutputSteps) aSessionWasSavedWithLegacyDisplayStringToolInput() error {
	return s.saveReplaySessionWithToolInput("ls -la")
}

// saveReplaySessionWithToolInput seeds the replay fixture with a tool_call
// message carrying the given ToolInput, mirroring the manager test fixtures.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Restores one session into a fresh manager wired to a seeder streamer.
func (s *StreamingToolOutputSteps) saveReplaySessionWithToolInput(toolInput string) error {
	s.replayStreamer = &toolInputHistorySeeder{}
	s.replayStreamer.addChunk(provider.StreamChunk{Done: true})
	s.replayManager = session.NewManager(s.replayStreamer)
	restored := &session.Session{
		ID:      "sess-tool-input-replay",
		AgentID: "planner",
		Messages: []session.Message{
			{ID: "m1", Role: "user", Content: "Run the tool.", AgentID: "planner"},
			{
				ID:        "m2",
				Role:      "tool_call",
				Content:   "bash",
				ToolName:  "bash",
				ToolInput: toolInput,
				AgentID:   "planner",
			},
		},
	}
	s.replayManager.RestoreSessions([]*session.Session{restored})
	s.replaySessionID = restored.ID
	return nil
}

// theSessionHistoryIsReplayedToTheProvider sends a follow-up message so the
// manager projects the restored history into the provider message list.
//
// Returns:
//   - An error when the follow-up turn fails.
func (s *StreamingToolOutputSteps) theSessionHistoryIsReplayedToTheProvider() error {
	_, err := s.replayManager.SendMessage(context.Background(), s.replaySessionID, "Continue.")
	if err != nil {
		return fmt.Errorf("replaying session history: %w", err)
	}
	return nil
}

// seededToolCallMessage returns the projected tool_call message from the
// seeder capture, falling back to an error when history was not seeded.
//
// Returns:
//   - The projected provider.Message and nil, or the zero message and an error.
func (s *StreamingToolOutputSteps) seededToolCallMessage() (provider.Message, error) {
	msgs := s.replayStreamer.seededMessages
	if len(msgs) < 2 {
		return provider.Message{}, fmt.Errorf("expected at least 2 seeded messages, got %d", len(msgs))
	}
	return msgs[len(msgs)-1], nil
}

// theToolCallArgumentsShouldBeRestoredAsStructuredToolCalls asserts the
// replayed tool_call message carries its arguments as structured ToolCalls
// with empty descriptive content.
//
// Returns:
//   - An error when the projection is not a structured tool call.
func (s *StreamingToolOutputSteps) theToolCallArgumentsShouldBeRestoredAsStructuredToolCalls() error {
	msg, err := s.seededToolCallMessage()
	if err != nil {
		return err
	}
	if msg.Role != "assistant" {
		return fmt.Errorf("expected replayed tool call to project as assistant, got role %q", msg.Role)
	}
	if msg.Content != "" {
		return fmt.Errorf("expected structured replay to carry empty content, got %q", msg.Content)
	}
	if len(msg.ToolCalls) != 1 {
		return fmt.Errorf("expected 1 structured tool call, got %d", len(msg.ToolCalls))
	}
	call := msg.ToolCalls[0]
	if call.Name != "bash" {
		return fmt.Errorf("expected tool call name %q, got %q", "bash", call.Name)
	}
	if call.Arguments["command"] != "ls -la" {
		return fmt.Errorf("expected command argument %q, got %v", "ls -la", call.Arguments["command"])
	}
	timeout, ok := call.Arguments["timeout"].(float64)
	if !ok || timeout != 30 {
		return fmt.Errorf("expected timeout argument 30, got %v", call.Arguments["timeout"])
	}
	return nil
}

// theToolInputShouldReplayAsDescriptiveTextInBrackets asserts a legacy
// display-string ToolInput falls back to the bracketed descriptive text.
//
// Returns:
//   - An error when the legacy fallback wrap is not produced.
func (s *StreamingToolOutputSteps) theToolInputShouldReplayAsDescriptiveTextInBrackets() error {
	msg, err := s.seededToolCallMessage()
	if err != nil {
		return err
	}
	expected := "[bash with input: ls -la]"
	if msg.Role != "assistant" {
		return fmt.Errorf("expected replayed tool call to project as assistant, got role %q", msg.Role)
	}
	if msg.Content != expected {
		return fmt.Errorf("expected legacy replay content %q, got %q", expected, msg.Content)
	}
	if len(msg.ToolCalls) != 0 {
		return fmt.Errorf("expected no structured tool calls for legacy input, got %d", len(msg.ToolCalls))
	}
	return nil
}

// toolInputHistorySeeder implements the streamer and history seeder
// interfaces, capturing the projected provider messages for assertions.
type toolInputHistorySeeder struct {
	chunks         []provider.StreamChunk
	seededMessages []provider.Message
}

func (m *toolInputHistorySeeder) addChunk(chunk provider.StreamChunk) {
	m.chunks = append(m.chunks, chunk)
}

func (m *toolInputHistorySeeder) Stream(_ context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, len(m.chunks))
	for _, c := range m.chunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}

func (m *toolInputHistorySeeder) SeedHistory(_ string, messages []provider.Message) {
	m.seededMessages = messages
}
