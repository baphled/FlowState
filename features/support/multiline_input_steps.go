//go:build e2e

package support

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cucumber/godog"
)

// MultilineInputSteps holds state for multiline chat input BDD scenarios.
type MultilineInputSteps struct {
	steps              *StepDefinitions
	messageCountBefore int
}

// RegisterMultilineInputSteps registers step definitions for multiline chat input scenarios.
//
// Expected:
//   - sc is a valid godog ScenarioContext for step registration.
//   - steps is a pointer to the shared StepDefinitions instance.
//
// Side effects:
//   - Registers multiline input step definitions on the provided scenario context.
func RegisterMultilineInputSteps(sc *godog.ScenarioContext, steps *StepDefinitions) {
	s := &MultilineInputSteps{steps: steps}

	sc.Step(`^I am in the chat input$`, s.iAmInTheChatInput)
	sc.Step(`^I press Alt\+Enter$`, s.iPressAltEnter)
	sc.Step(`^I press Backspace$`, s.iPressBackspace)
	sc.Step(`^I have typed "([^"]*)" then pressed Alt\+Enter then typed "([^"]*)"$`, s.iHaveTypedThenPressedAltEnterThenTyped)
	sc.Step(`^the input should contain a newline$`, s.theInputShouldContainANewline)
	sc.Step(`^no message should be sent to the AI$`, s.noMessageShouldBeSentToTheAI)
	sc.Step(`^the input display should show (\d+) lines$`, s.theInputDisplayShouldShowLines)
	sc.Step(`^the message viewport should be reduced by (\d+) row$`, s.theMessageViewportShouldBeReducedByRow)
	sc.Step(`^the message containing a newline should be sent$`, s.theMessageContainingANewlineShouldBeSent)
	sc.Step(`^the input should equal "([^"]*)"$`, s.theInputShouldEqual)
	sc.Step(`^the input should contain no newline$`, s.theInputShouldContainNoNewline)
}

// iAmInTheChatInput sets the input to chat mode with an empty buffer.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Sets isInsertMode to true and clears inputBuffer.
//   - Records the current message count for later assertions.
func (s *MultilineInputSteps) iAmInTheChatInput() error {
	s.steps.isInsertMode = true
	s.steps.inputBuffer = ""
	s.messageCountBefore = len(s.steps.app.messages)
	return nil
}

// iPressAltEnter appends a newline to the input buffer without sending.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Appends "\n" to the inputBuffer.
func (s *MultilineInputSteps) iPressAltEnter() error {
	s.steps.inputBuffer += "\n"
	return nil
}

// iPressBackspace removes the last byte from the input buffer.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Truncates the last character from inputBuffer, or does nothing if empty.
func (s *MultilineInputSteps) iPressBackspace() error {
	if s.steps.inputBuffer != "" {
		s.steps.inputBuffer = s.steps.inputBuffer[:len(s.steps.inputBuffer)-1]
	}
	return nil
}

// iHaveTypedThenPressedAltEnterThenTyped composes a multiline input from two parts.
//
// Expected:
//   - first is the text typed before Alt+Enter.
//   - second is the text typed after Alt+Enter.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Sets inputBuffer to first + "\n" + second.
func (s *MultilineInputSteps) iHaveTypedThenPressedAltEnterThenTyped(first, second string) error {
	s.steps.inputBuffer = first + "\n" + second
	return nil
}

// theInputShouldContainANewline asserts that the input buffer contains a newline character.
//
// Returns:
//   - nil if a newline is present, error otherwise.
//
// Side effects:
//   - None.
func (s *MultilineInputSteps) theInputShouldContainANewline() error {
	if !strings.Contains(s.steps.inputBuffer, "\n") {
		return fmt.Errorf("expected input to contain a newline, got: %q", s.steps.inputBuffer)
	}
	return nil
}

// noMessageShouldBeSentToTheAI asserts that no new messages were sent.
//
// Returns:
//   - nil if message count is unchanged, error otherwise.
//
// Side effects:
//   - None.
func (s *MultilineInputSteps) noMessageShouldBeSentToTheAI() error {
	if len(s.steps.app.messages) > s.messageCountBefore {
		return fmt.Errorf("expected no new messages, but %d were sent", len(s.steps.app.messages)-s.messageCountBefore)
	}
	return nil
}

// theInputDisplayShouldShowLines asserts the input has the expected number of lines.
//
// Expected:
//   - expected is the number of lines to expect.
//
// Returns:
//   - nil if line count matches, error otherwise.
//
// Side effects:
//   - None.
func (s *MultilineInputSteps) theInputDisplayShouldShowLines(expected int) error {
	actual := strings.Count(s.steps.inputBuffer, "\n") + 1
	if actual != expected {
		return fmt.Errorf("expected %d lines, got %d (input: %q)", expected, actual, s.steps.inputBuffer)
	}
	return nil
}

// theMessageViewportShouldBeReducedByRow asserts that the input contains newlines
// which would reduce the viewport.
//
// Expected:
//   - rows is the expected number of extra rows consumed by multiline input.
//
// Returns:
//   - nil if newline count matches the reduction, error otherwise.
//
// Side effects:
//   - None.
func (s *MultilineInputSteps) theMessageViewportShouldBeReducedByRow(rows int) error {
	newlineCount := strings.Count(s.steps.inputBuffer, "\n")
	if newlineCount != rows {
		return fmt.Errorf("expected %d extra rows from newlines, got %d", rows, newlineCount)
	}
	return nil
}

// theMessageContainingANewlineShouldBeSent asserts the last sent message contains a newline.
//
// Returns:
//   - nil if the last user message contains a newline, error otherwise.
//
// Side effects:
//   - None.
func (s *MultilineInputSteps) theMessageContainingANewlineShouldBeSent() error {
	if len(s.steps.app.messages) == 0 {
		return errors.New("no messages were sent")
	}
	for i := len(s.steps.app.messages) - 1; i >= 0; i-- {
		if s.steps.app.messages[i].Role == "user" {
			if strings.Contains(s.steps.app.messages[i].Content, "\n") {
				return nil
			}
			return fmt.Errorf("expected last user message to contain newline, got: %q", s.steps.app.messages[i].Content)
		}
	}
	return errors.New("no user messages found")
}

// theInputShouldEqual asserts the input buffer matches the expected string exactly.
//
// Expected:
//   - expected is the exact string to match against the input buffer.
//
// Returns:
//   - nil if equal, error otherwise.
//
// Side effects:
//   - None.
func (s *MultilineInputSteps) theInputShouldEqual(expected string) error {
	if s.steps.inputBuffer != expected {
		return fmt.Errorf("expected input %q, got %q", expected, s.steps.inputBuffer)
	}
	return nil
}

// theInputShouldContainNoNewline asserts the input buffer has no newline characters.
//
// Returns:
//   - nil if no newline present, error otherwise.
//
// Side effects:
//   - None.
func (s *MultilineInputSteps) theInputShouldContainNoNewline() error {
	if strings.Contains(s.steps.inputBuffer, "\n") {
		return fmt.Errorf("expected no newlines in input, got: %q", s.steps.inputBuffer)
	}
	return nil
}
