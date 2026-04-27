//go:build e2e

package support

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/provider"
)

// RegisterSessionVisibilitySteps registers step definitions for tool output, skill load,
// and tool error visibility scenarios.
//
// Expected:
//   - sc is a valid godog ScenarioContext for step registration.
//   - s is the shared SessionIsolationSteps instance (provides sessionStore, loadedStore, savedSessionID).
//
// Side effects:
//   - Registers Given and Then step definitions on the provided scenario context.
func RegisterSessionVisibilitySteps(sc *godog.ScenarioContext, s *SessionIsolationSteps) {
	sc.Step(`^a session was saved with tool result messages$`, s.aSessionWasSavedWithToolResultMessages)
	sc.Step(`^a session was saved with skill load messages$`, s.aSessionWasSavedWithSkillLoadMessages)
	sc.Step(`^a session was saved with a failed tool result$`, s.aSessionWasSavedWithAFailedToolResult)
	sc.Step(`^I should see the tool result in the chat view$`, s.iShouldSeeTheToolResultInTheChatView)
	sc.Step(`^I should see the skill load in the chat view$`, s.iShouldSeeTheSkillLoadInTheChatView)
	sc.Step(`^I should see the tool error in the chat view$`, s.iShouldSeeTheToolErrorInTheChatView)
}

// aSessionWasSavedWithToolResultMessages creates a session containing a tool role message with output.
//
// Expected:
//   - No prior state is required.
//
// Returns:
//   - An error if the temp directory, session store, or save operation fails.
//
// Side effects:
//   - Creates a temporary directory with a session store containing a tool result message.
//   - Sets savedSessionID to "tool-output-session".
func (s *SessionIsolationSteps) aSessionWasSavedWithToolResultMessages() error {
	return s.saveSessionWithMessages("flowstate-tool-output-*", "tool-output-session", []provider.Message{
		{Role: "user", Content: "Run a command"},
		{Role: "tool", Content: "tool output here"},
	})
}

// aSessionWasSavedWithSkillLoadMessages creates a session containing an assistant message with a skill_load tool call.
//
// Expected:
//   - No prior state is required.
//
// Returns:
//   - An error if the temp directory, session store, or save operation fails.
//
// Side effects:
//   - Creates a temporary directory with a session store containing a skill load message.
//   - Sets savedSessionID to "skill-load-session".
func (s *SessionIsolationSteps) aSessionWasSavedWithSkillLoadMessages() error {
	return s.saveSessionWithMessages("flowstate-skill-load-*", "skill-load-session", []provider.Message{
		{Role: "user", Content: "Load a skill"},
		{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{Name: "skill_load"}}},
	})
}

// aSessionWasSavedWithAFailedToolResult creates a session containing a tool role message with an error.
//
// Expected:
//   - No prior state is required.
//
// Returns:
//   - An error if the temp directory, session store, or save operation fails.
//
// Side effects:
//   - Creates a temporary directory with a session store containing a failed tool result.
//   - Sets savedSessionID to "tool-error-session".
func (s *SessionIsolationSteps) aSessionWasSavedWithAFailedToolResult() error {
	return s.saveSessionWithMessages("flowstate-tool-error-*", "tool-error-session", []provider.Message{
		{Role: "user", Content: "Run a command"},
		{Role: "tool", Content: "Error: command failed"},
	})
}

// iShouldSeeTheToolResultInTheChatView asserts the loaded session contains a tool role message with non-empty content.
//
// Expected:
//   - iLoadTheSession has been called.
//
// Returns:
//   - An error if the loaded store is nil or has no tool message with content.
//
// Side effects:
//   - None.
func (s *SessionIsolationSteps) iShouldSeeTheToolResultInTheChatView() error {
	if s.loadedStore == nil {
		return errors.New("loaded store is nil")
	}
	messages := s.loadedStore.GetStoredMessages()
	for _, sm := range messages {
		if sm.Message.Role == "tool" && sm.Message.Content != "" {
			return nil
		}
	}
	return fmt.Errorf("expected a tool message with non-empty content, found %d messages without one", len(messages))
}

// iShouldSeeTheSkillLoadInTheChatView asserts the loaded session contains an assistant message with tool calls.
//
// Expected:
//   - iLoadTheSession has been called.
//
// Returns:
//   - An error if the loaded store is nil or has no assistant message with tool calls.
//
// Side effects:
//   - None.
func (s *SessionIsolationSteps) iShouldSeeTheSkillLoadInTheChatView() error {
	if s.loadedStore == nil {
		return errors.New("loaded store is nil")
	}
	messages := s.loadedStore.GetStoredMessages()
	for _, sm := range messages {
		if sm.Message.Role == "assistant" && len(sm.Message.ToolCalls) > 0 {
			return nil
		}
	}
	return fmt.Errorf("expected an assistant message with tool calls, found %d messages without one", len(messages))
}

// iShouldSeeTheToolErrorInTheChatView asserts the loaded session contains a tool message with error content.
//
// Expected:
//   - iLoadTheSession has been called.
//
// Returns:
//   - An error if the loaded store is nil or has no tool message starting with "Error:".
//
// Side effects:
//   - None.
func (s *SessionIsolationSteps) iShouldSeeTheToolErrorInTheChatView() error {
	if s.loadedStore == nil {
		return errors.New("loaded store is nil")
	}
	messages := s.loadedStore.GetStoredMessages()
	for _, sm := range messages {
		if sm.Message.Role == "tool" && strings.HasPrefix(sm.Message.Content, "Error:") {
			return nil
		}
	}
	return fmt.Errorf("expected a tool message starting with 'Error:', found %d messages without one", len(messages))
}
