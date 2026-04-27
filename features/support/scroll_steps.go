//go:build e2e

package support

import (
	"errors"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

// ScrollingSteps holds state for viewport scrolling BDD scenarios.
type ScrollingSteps struct {
	intent              *chat.Intent
	initialYOffset      int
	windowWidth         int
	windowHeight        int
	scrollPositionAfter int
}

// RegisterScrollingSteps registers step definitions for viewport scrolling scenarios.
//
// Expected: scenario context and ScrollingSteps instance to register steps on.
//
// Side effects: Registers 18 step definitions with the scenario context for scrolling BDD tests.
func RegisterScrollingSteps(sc *godog.ScenarioContext, s *ScrollingSteps) {
	sc.Step(`^a chat intent is initialised with a window size of (\d+)x(\d+)$`, s.aChatIntentIsInitialisedWithWindowSize)
	sc.Step(`^the chat has enough messages to require scrolling$`, s.theChatHasEnoughMessagesToRequireScrolling)
	sc.Step(`^the viewport is at the bottom$`, s.theViewportIsAtTheBottom)
	sc.Step(`^the viewport is not at the bottom$`, s.theViewportIsNotAtTheBottom)
	sc.Step(`^I scroll up with PgUp$`, s.iScrollUpWithPgUp)
	sc.Step(`^I press PgUp$`, s.iScrollUpWithPgUp)
	sc.Step(`^I press End to return to the bottom$`, s.iPressEndToReturnToTheBottom)
	sc.Step(`^I press PgDn$`, s.iPressPgDn)
	sc.Step(`^I type "([^"]*)" and press Enter$`, s.iTypeAndPressEnter)
	sc.Step(`^a new stream chunk arrives$`, s.aNewStreamChunkArrives)
	sc.Step(`^the viewport should not be at the bottom$`, s.theViewportShouldNotBeAtTheBottom)
	sc.Step(`^the viewport should be at the bottom$`, s.theViewportShouldBeAtTheBottom)
	sc.Step(`^the viewport should remain at my scrolled position$`, s.theViewportShouldRemainAtMyScrolledPosition)
	sc.Step(`^the viewport should stay at the bottom$`, s.theViewportShouldStayAtTheBottom)
	sc.Step(`^the viewport should move towards the bottom$`, s.theViewportShouldMoveTowardsTheBottom)
}

// aChatIntentIsInitialisedWithWindowSize initialises a chat intent with the given dimensions.
//
// Returns: error if intent creation fails.
//
// Expected: width and height are positive integers.
//
// Side effects: Creates intent, stores window size in ScrollingSteps, sends WindowSizeMsg to intent.
func (s *ScrollingSteps) aChatIntentIsInitialisedWithWindowSize(width, height int) error {
	s.windowWidth = width
	s.windowHeight = height

	s.intent = chat.NewIntent(chat.IntentConfig{
		AgentID:      "test-agent",
		SessionID:    "test-session",
		ProviderName: "test",
		ModelName:    "test-model",
		TokenBudget:  4096,
	})

	// Send window size message to initialise viewport
	s.intent.Update(tea.WindowSizeMsg{Width: width, Height: height})

	return nil
}

// theChatHasEnoughMessagesToRequireScrolling adds enough content to require scrolling.
//
// Returns: error if intent not initialised or viewport assertion fails.
//
// Side effects: Updates intent with multiple StreamChunkMsg; verifies viewport is at bottom.
func (s *ScrollingSteps) theChatHasEnoughMessagesToRequireScrolling() error {
	if s.intent == nil {
		return errors.New("intent not initialised")
	}

	// Add enough messages to require scrolling (more lines than viewport height)
	// Each message is roughly 5-6 lines, so we need at least height/6 + 1 messages
	messageCount := (s.windowHeight / 6) + 5

	for i := range messageCount {
		chunk := chat.StreamChunkMsg{
			Content: fmt.Sprintf("Assistant message %d:\nThis is a multi-line message.\nIt should take up space.\nTo fill the viewport.\n", i),
			Done:    false,
		}
		s.intent.Update(chunk)
	}

	// Mark streaming as done
	s.intent.Update(chat.StreamChunkMsg{Content: "", Done: true})

	// Ensure we're at the bottom initially
	if !s.intent.AtBottom() {
		offset := s.intent.ViewportYOffset()
		return fmt.Errorf("expected to start at bottom, but atBottom=false (offset=%d)", offset)
	}

	return nil
}

// theViewportIsAtTheBottom asserts the viewport is at the bottom.
//
// Returns: error if viewport is not at bottom.
//
// Side effects: None (assertion only).
func (s *ScrollingSteps) theViewportIsAtTheBottom() error {
	if !s.intent.AtBottom() {
		return errors.New("expected viewport to be at the bottom, but atBottom=false")
	}
	return nil
}

// theViewportIsNotAtTheBottom ensures viewport is not at bottom by scrolling up.
//
// Returns: error if viewport is at bottom after scroll attempt.
//
// Side effects: Updates intent with KeyPgUp; records scroll position in scrollPositionAfter.
func (s *ScrollingSteps) theViewportIsNotAtTheBottom() error {
	// Scroll up to ensure we're not at the bottom
	s.intent.Update(tea.KeyMsg{Type: tea.KeyPgUp})

	if s.intent.AtBottom() {
		return errors.New("expected viewport to not be at the bottom after scrolling up")
	}

	s.scrollPositionAfter = s.intent.ViewportYOffset()
	return nil
}

// iScrollUpWithPgUp simulates pressing Page Up.
//
// Returns: error if intent not initialised.
//
// Side effects: Records initial offset; updates intent with KeyPgUp; records final offset.
func (s *ScrollingSteps) iScrollUpWithPgUp() error {
	s.initialYOffset = s.intent.ViewportYOffset()
	s.intent.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	s.scrollPositionAfter = s.intent.ViewportYOffset()
	return nil
}

// iPressEndToReturnToTheBottom simulates pressing End to jump to the bottom.
//
// Returns: error if intent is not initialised.
//
// Side effects: Updates intent with KeyEnd message; scrolls viewport to bottom.
func (s *ScrollingSteps) iPressEndToReturnToTheBottom() error {
	s.intent.Update(tea.KeyMsg{Type: tea.KeyEnd})
	return nil
}

// iPressPgDn simulates pressing Page Down.
//
// Returns: error if intent is not initialised.
//
// Side effects: Updates intent with KeyPgDown message; records initial and final viewport offsets.
func (s *ScrollingSteps) iPressPgDn() error {
	s.initialYOffset = s.intent.ViewportYOffset()
	s.intent.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	s.scrollPositionAfter = s.intent.ViewportYOffset()
	return nil
}

// iTypeAndPressEnter simulates typing a message and pressing Enter (sending it).
//
// Returns: error if intent is not initialised.
//
// Expected: text is non-empty message content.
//
// Side effects: Updates intent with KeyRunes messages for each character, then KeyEnter to send.
func (s *ScrollingSteps) iTypeAndPressEnter(text string) error {
	// Type the message character by character
	for _, r := range text {
		s.intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	// Press Enter to send
	s.intent.Update(tea.KeyMsg{Type: tea.KeyEnter})

	return nil
}

// aNewStreamChunkArrives simulates a new chunk arriving from the stream.
//
// Returns: error if intent is not initialised.
//
// Side effects: Records current viewport offset before chunk arrival; updates intent with StreamChunkMsg.
func (s *ScrollingSteps) aNewStreamChunkArrives() error {
	s.scrollPositionAfter = s.intent.ViewportYOffset()

	// Add a new chunk
	s.intent.Update(chat.StreamChunkMsg{
		Content: "New streaming content...\n",
		Done:    false,
	})

	return nil
}

// theViewportShouldNotBeAtTheBottom asserts the viewport is not at the bottom.
//
// Returns: error if viewport is at bottom when not expected.
//
// Side effects: None (assertion only).
func (s *ScrollingSteps) theViewportShouldNotBeAtTheBottom() error {
	if s.intent.AtBottom() {
		return errors.New("expected viewport to not be at the bottom, but atBottom=true")
	}
	return nil
}

// theViewportShouldBeAtTheBottom asserts the viewport is at the bottom.
//
// Returns: error if viewport is not at bottom when expected.
//
// Side effects: None (assertion only).
func (s *ScrollingSteps) theViewportShouldBeAtTheBottom() error {
	if !s.intent.AtBottom() {
		offset := s.intent.ViewportYOffset()
		return fmt.Errorf("expected viewport to be at the bottom, but atBottom=false (offset=%d)", offset)
	}
	return nil
}

// theViewportShouldRemainAtMyScrolledPosition asserts viewport stayed at the same position.
//
// Returns: error if viewport offset changed when not expected.
//
// Side effects: None (assertion only).
func (s *ScrollingSteps) theViewportShouldRemainAtMyScrolledPosition() error {
	currentOffset := s.intent.ViewportYOffset()

	if currentOffset != s.scrollPositionAfter {
		return fmt.Errorf("expected viewport to remain at offset %d, but got %d", s.scrollPositionAfter, currentOffset)
	}

	return nil
}

// theViewportShouldStayAtTheBottom asserts viewport is still at the bottom.
//
// Returns: error if viewport is not at bottom when expected.
//
// Side effects: None (delegates to theViewportShouldBeAtTheBottom assertion).
func (s *ScrollingSteps) theViewportShouldStayAtTheBottom() error {
	return s.theViewportShouldBeAtTheBottom()
}

// theViewportShouldMoveTowardsTheBottom asserts viewport moved closer to the bottom.
//
// Returns: error if viewport did not move closer to bottom (offset did not decrease).
//
// Side effects: None (assertion only).
func (s *ScrollingSteps) theViewportShouldMoveTowardsTheBottom() error {
	currentOffset := s.intent.ViewportYOffset()

	// When moving down (towards bottom), offset should decrease
	if currentOffset >= s.initialYOffset {
		return fmt.Errorf("expected viewport to move towards bottom (lower offset), but went from %d to %d", s.initialYOffset, currentOffset)
	}

	return nil
}
