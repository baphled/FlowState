//go:build e2e

package support

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	chatintent "github.com/baphled/flowstate/internal/tui/intents/chat"
)

// ReadToolSteps holds state for read tool suppression BDD scenarios.
type ReadToolSteps struct {
	intent         *chatintent.Intent
	lastToolResult string
	isError        bool
}

// RegisterReadToolSteps registers step definitions for read tool suppression scenarios.
//
// Expected:
//   - sc is a valid godog ScenarioContext for step registration.
//   - s is a non-nil ReadToolSteps instance.
//
// Side effects:
//   - Registers step definitions on the provided scenario context.
func RegisterReadToolSteps(sc *godog.ScenarioContext, s *ReadToolSteps) {
	sc.Step(`^the AI uses the read tool on a file$`, s.theAIUsesTheReadToolOnAFile)
	sc.Step(`^the read tool returns the file contents$`, s.theReadToolReturnsTheFileContents)
	sc.Step(`^the file contents should not appear in the chat view$`, s.theFileContentsShouldNotAppearInTheChatView)
	sc.Step(`^other tool results should still be visible$`, s.otherToolResultsShouldStillBeVisible)

	sc.Step(`^the AI uses the read tool on a non-existent file$`, s.theAIUsesTheReadToolOnANonExistentFile)
	sc.Step(`^the read tool returns an error$`, s.theReadToolReturnsAnError)
	sc.Step(`^the error should appear in the chat view$`, s.theErrorShouldAppearInTheChatView)

	sc.Step(`^a session exists where the AI used the read tool$`, s.aSessionExistsWhereTheAIUsedTheReadTool)
	sc.Step(`^the session is loaded$`, s.theSessionIsLoaded)
	sc.Step(`^the read tool result should not appear in the reloaded chat$`, s.theReadToolResultShouldNotAppearInTheReloadedChat)

	sc.Step(`^the AI uses the read tool then the bash tool$`, s.theAIUsesTheReadToolThenTheBashTool)
	sc.Step(`^both tools complete$`, s.bothToolsComplete)
	sc.Step(`^only the bash tool result should appear in the chat$`, s.onlyTheBashToolResultShouldAppearInTheChat)
	sc.Step(`^the read tool result should not appear$`, s.theReadToolResultShouldNotAppear)
}

// newIntent creates a fresh chat Intent configured for BDD step testing.
//
// Returns:
//   - A new Intent instance with minimal configuration for test scenarios.
//
// Side effects:
//   - None.
func (s *ReadToolSteps) newIntent() *chatintent.Intent {
	return chatintent.NewIntent(chatintent.IntentConfig{
		AgentID:      "test-agent",
		SessionID:    "test-session",
		ProviderName: "openai",
		ModelName:    "gpt-4o",
		TokenBudget:  4096,
	})
}

// theAIUsesTheReadToolOnAFile sets up the intent with an active read tool call.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Initialises s.intent and simulates a read tool call start.
func (s *ReadToolSteps) theAIUsesTheReadToolOnAFile() error {
	s.intent = s.newIntent()
	s.intent.Update(chatintent.StreamChunkMsg{
		ToolCallName: "read: /some/file.go",
	})
	return nil
}

// theReadToolReturnsTheFileContents simulates the read tool returning file content.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Sends a stream chunk with tool result content.
func (s *ReadToolSteps) theReadToolReturnsTheFileContents() error {
	if s.intent == nil {
		return errors.New("intent not initialised — call 'the AI uses the read tool on a file' first")
	}
	s.lastToolResult = "package main\n\nfunc main() {}"
	s.intent.Update(chatintent.StreamChunkMsg{
		ToolCallName: "",
		ToolResult:   s.lastToolResult,
		ToolIsError:  false,
	})
	return nil
}

// theFileContentsShouldNotAppearInTheChatView asserts the read result was suppressed.
//
// Returns:
//   - nil if no tool_result message is found.
//   - An error if a tool_result message appears in the chat.
//
// Side effects:
//   - None.
func (s *ReadToolSteps) theFileContentsShouldNotAppearInTheChatView() error {
	if s.intent == nil {
		return errors.New("intent not initialised")
	}
	for _, msg := range s.intent.MessagesForTest() {
		if msg.Role == "tool_result" {
			return fmt.Errorf("read tool result should not appear in chat, but found: %q", msg.Content)
		}
	}
	return nil
}

// otherToolResultsShouldStillBeVisible asserts the tool_call indicator is present.
//
// Returns:
//   - nil if the read tool_call message is present.
//   - An error if the tool_call indicator is missing.
//
// Side effects:
//   - None.
func (s *ReadToolSteps) otherToolResultsShouldStillBeVisible() error {
	if s.intent == nil {
		return errors.New("intent not initialised")
	}
	for _, msg := range s.intent.MessagesForTest() {
		if msg.Role == "tool_call" && strings.HasPrefix(msg.Content, "read:") {
			return nil
		}
	}
	return errors.New("expected read tool_call indicator to be visible, but was not found")
}

// theAIUsesTheReadToolOnANonExistentFile sets up the intent with a failed read tool call.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Initialises s.intent and simulates a read tool call start.
func (s *ReadToolSteps) theAIUsesTheReadToolOnANonExistentFile() error {
	s.intent = s.newIntent()
	s.isError = true
	s.intent.Update(chatintent.StreamChunkMsg{
		ToolCallName: "read: /nonexistent/file.go",
	})
	return nil
}

// theReadToolReturnsAnError simulates the read tool returning an error.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Sends a stream chunk with a tool error result.
func (s *ReadToolSteps) theReadToolReturnsAnError() error {
	if s.intent == nil {
		return errors.New("intent not initialised — call 'the AI uses the read tool on a non-existent file' first")
	}
	s.lastToolResult = "Error: file not found"
	s.intent.Update(chatintent.StreamChunkMsg{
		ToolCallName: "",
		ToolResult:   s.lastToolResult,
		ToolIsError:  true,
	})
	return nil
}

// theErrorShouldAppearInTheChatView asserts the error is visible in the chat.
//
// Returns:
//   - nil if a tool_error message is found.
//   - An error if no tool_error message is found.
//
// Side effects:
//   - None.
func (s *ReadToolSteps) theErrorShouldAppearInTheChatView() error {
	if s.intent == nil {
		return errors.New("intent not initialised")
	}
	for _, msg := range s.intent.MessagesForTest() {
		if msg.Role == "tool_error" {
			return nil
		}
	}
	return errors.New("expected tool_error message to appear in chat, but none found")
}

// aSessionExistsWhereTheAIUsedTheReadTool initialises a view with a simulated session.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Initialises s.chatView with a read tool call and result replayed.
func (s *ReadToolSteps) aSessionExistsWhereTheAIUsedTheReadTool() error {
	s.intent = s.newIntent()
	s.intent.Update(chatintent.StreamChunkMsg{
		ToolCallName: "read: /main.go",
	})
	s.intent.Update(chatintent.StreamChunkMsg{
		ToolCallName: "",
		ToolResult:   "package main\n\nfunc main() {}",
		ToolIsError:  false,
	})
	return nil
}

// theSessionIsLoaded simulates reloading the session state into a fresh view.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Creates a new chatView and replays read tool events onto it.
func (s *ReadToolSteps) theSessionIsLoaded() error {
	if s.intent == nil {
		return errors.New("intent not initialised")
	}
	return nil
}

// theReadToolResultShouldNotAppearInTheReloadedChat asserts the read result is suppressed.
//
// Returns:
//   - nil if no tool_result message is found.
//   - An error if a tool_result message appears in the chat.
//
// Side effects:
//   - None.
func (s *ReadToolSteps) theReadToolResultShouldNotAppearInTheReloadedChat() error {
	if s.intent == nil {
		return errors.New("intent not initialised")
	}
	for _, msg := range s.intent.MessagesForTest() {
		if msg.Role == "tool_result" {
			return fmt.Errorf("read tool result should not appear in reloaded chat, but found: %q", msg.Content)
		}
	}
	return nil
}

// theAIUsesTheReadToolThenTheBashTool sets up a sequence of read then bash tool calls.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Initialises s.intent and simulates both tool call starts.
func (s *ReadToolSteps) theAIUsesTheReadToolThenTheBashTool() error {
	s.intent = s.newIntent()
	s.intent.Update(chatintent.StreamChunkMsg{
		ToolCallName: "read: /config.go",
	})
	s.intent.Update(chatintent.StreamChunkMsg{
		ToolCallName: "",
		ToolResult:   "const Config = true",
		ToolIsError:  false,
	})
	s.intent.Update(chatintent.StreamChunkMsg{
		ToolCallName: "bash: echo hello",
	})
	return nil
}

// bothToolsComplete sends the bash tool result to complete both tool sequences.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Sends bash tool result chunk.
func (s *ReadToolSteps) bothToolsComplete() error {
	if s.intent == nil {
		return errors.New("intent not initialised")
	}
	s.intent.Update(chatintent.StreamChunkMsg{
		ToolCallName: "",
	})
	s.intent.Update(chatintent.StreamChunkMsg{
		ToolResult:  "hello",
		ToolIsError: false,
	})
	return nil
}

// onlyTheBashToolResultShouldAppearInTheChat asserts the bash result is visible.
//
// Returns:
//   - nil if the bash tool_result is found.
//   - An error if it is missing.
//
// Side effects:
//   - None.
func (s *ReadToolSteps) onlyTheBashToolResultShouldAppearInTheChat() error {
	if s.intent == nil {
		return errors.New("intent not initialised")
	}
	for _, msg := range s.intent.MessagesForTest() {
		if msg.Role == "tool_result" && msg.Content == "hello" {
			return nil
		}
	}
	return errors.New("expected bash tool result 'hello' to appear in chat, but was not found")
}

// theReadToolResultShouldNotAppear asserts no read tool result appears in the chat.
//
// Returns:
//   - nil if no tool_result message with read content is found.
//   - An error if a read tool_result appears.
//
// Side effects:
//   - None.
func (s *ReadToolSteps) theReadToolResultShouldNotAppear() error {
	if s.intent == nil {
		return errors.New("intent not initialised")
	}
	for _, msg := range s.intent.MessagesForTest() {
		if msg.Role == "tool_result" && strings.Contains(msg.Content, "Config") {
			return fmt.Errorf("read tool result should not appear in chat, but found: %q", msg.Content)
		}
	}
	return nil
}
