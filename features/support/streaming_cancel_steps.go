// Package support provides BDD step definitions and helpers.
//
// This file implements the step definitions that exercise the TUI's double-Esc
// streaming-cancel interrupt. The scenario lives at
// features/chat/streaming.feature and asserts that a user-initiated mid-stream
// cancellation (a) aborts the stream, (b) produces no error notification, and
// (c) leaves the response incomplete.
package support

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// streamTokensWaitBudget is the maximum time the "I see tokens appearing" step
// will wait for the first chunk before declaring the scenario failed. The mock
// provider emits a chunk every ~2ms so this leaves comfortable headroom.
const streamTokensWaitBudget = 500 * time.Millisecond

// streamDrainWaitBudget is the maximum time the "the stream should be
// cancelled" step will wait for the background drain goroutine to exit after
// the cancel is issued.
const streamDrainWaitBudget = 2 * time.Second

// RegisterStreamingCancelSteps registers the streaming-cancel step patterns
// on the godog scenario context.
//
// Expected:
//   - ctx is a non-nil godog ScenarioContext; s holds the shared step state.
//
// Side effects:
//   - Binds step patterns to receiver methods on s.
func RegisterStreamingCancelSteps(ctx *godog.ScenarioContext, s *StepDefinitions) {
	ctx.Step(`^the agent streams a long response$`, s.theAgentStreamsALongResponse)
	ctx.Step(`^I send "([^"]*)"$`, s.iSend)
	ctx.Step(`^I see tokens appearing$`, s.iSeeTokensAppearing)
	ctx.Step(`^I press Escape twice within 500ms$`, s.iPressEscapeTwiceWithin500ms)
	ctx.Step(`^the stream should be cancelled$`, s.theStreamShouldBeCancelled)
	ctx.Step(`^no error should be shown$`, s.noErrorShouldBeShown)
	ctx.Step(`^the response should be incomplete$`, s.theResponseShouldBeIncomplete)
}

// theAgentStreamsALongResponse configures the mock provider to emit a long,
// slow stream so the scenario can observe tokens arriving and cancel before
// natural completion.
//
// Expected:
//   - s.app has been initialised by the Before hook.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Toggles long-stream mode on the mock provider.
func (s *StepDefinitions) theAgentStreamsALongResponse() error {
	if s.app == nil || s.app.provider == nil {
		return errors.New("test app or mock provider is not initialised")
	}
	s.app.provider.SetLongStream(true)
	s.streamFullLen = LongStreamFullLen()
	return nil
}

// iSend starts a streaming request asynchronously so the scenario can observe
// and interrupt the stream mid-flight. Unlike iPressEnter, this step returns
// immediately and drains the chunks in a background goroutine.
//
// Expected:
//   - text is a non-empty user message.
//
// Returns:
//   - An error if the provider fails to start the stream.
//
// Side effects:
//   - Installs s.streamCtx / s.streamCancel, appends the user message to
//     s.app.messages, resets s.responseParts, and spawns a goroutine that
//     drains chunks into s.responseParts and closes s.streamDrainDone.
func (s *StepDefinitions) iSend(text string) error {
	msg := Message{Role: "user", Content: text}
	s.app.messages = append(s.app.messages, msg)

	ctx, cancel := context.WithCancel(s.ctx)
	s.streamCtx = ctx
	s.streamCancel = cancel
	s.streamDrainDone = make(chan struct{})
	s.streamCancelled = false
	s.streamErrSeen = false
	s.streamedLen = 0
	s.streamUserEscd = false
	s.streamDoneChunkRx = false
	s.responseParts = nil

	ch, err := s.app.provider.Stream(ctx, ChatRequest{
		Model:    "mock",
		Messages: s.app.messages,
	})
	if err != nil {
		cancel()
		close(s.streamDrainDone)
		return err
	}

	go func() {
		defer close(s.streamDrainDone)
		for chunk := range ch {
			if chunk.Error != nil {
				s.streamErrSeen = true
				continue
			}
			if chunk.Done {
				s.streamDoneChunkRx = true
			}
			s.responseParts = append(s.responseParts, chunk.Content)
			s.streamedLen += len(chunk.Content)
		}
	}()

	return nil
}

// iSeeTokensAppearing waits (with a budget) for the background drain to have
// collected at least one non-empty chunk. Mirrors the user observing tokens
// render in the TUI before deciding to interrupt.
//
// Returns:
//   - nil once tokens have arrived, or an error if the budget elapses first.
//
// Side effects:
//   - None (reads s.responseParts without mutating).
func (s *StepDefinitions) iSeeTokensAppearing() error {
	deadline := time.Now().Add(streamTokensWaitBudget)
	for time.Now().Before(deadline) {
		if hasVisibleToken(s.responseParts) {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("no streaming tokens observed within %s", streamTokensWaitBudget)
}

// hasVisibleToken reports whether any collected chunk contains non-whitespace
// content.
//
// Expected:
//   - parts is a slice of streamed chunk contents (may be nil or empty).
//
// Returns:
//   - true if at least one chunk has visible content, false otherwise.
//
// Side effects:
//   - None.
func hasVisibleToken(parts []string) bool {
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			return true
		}
	}
	return false
}

// iPressEscapeTwiceWithin500ms simulates the TUI's double-Esc interrupt by
// cancelling the streaming context inside the 500ms window.
//
// Returns:
//   - nil on success, or an error if no stream is active.
//
// Side effects:
//   - Invokes s.streamCancel and records that the user initiated the cancel.
func (s *StepDefinitions) iPressEscapeTwiceWithin500ms() error {
	if s.streamCancel == nil {
		return errors.New("no active stream to cancel; call `I send` first")
	}
	// First Esc arms the interrupt; second Esc within the window fires it.
	// The mock harness models this directly via streamCancel — the 500ms
	// timing is enforced at the TUI layer, validated by the Ginkgo specs.
	s.streamUserEscd = true
	s.streamCancel()
	s.streamCancelled = true
	return nil
}

// theStreamShouldBeCancelled waits for the background drain to exit, proving
// the provider honoured the cancelled context.
//
// Returns:
//   - nil when the drain completes within budget, or a timeout error.
//
// Side effects:
//   - None.
func (s *StepDefinitions) theStreamShouldBeCancelled() error {
	if !s.streamCancelled {
		return errors.New("stream was never cancelled by the test")
	}
	if s.streamDrainDone == nil {
		return errors.New("no drain goroutine to await")
	}
	select {
	case <-s.streamDrainDone:
		return nil
	case <-time.After(streamDrainWaitBudget):
		return fmt.Errorf("stream drain did not complete within %s after cancel", streamDrainWaitBudget)
	}
}

// noErrorShouldBeShown asserts that no error chunk reached the consumer as a
// result of the user-initiated cancel. Mirrors the TUI behaviour from Defect 1:
// user-cancel is a legitimate action and must not surface as an error.
//
// Returns:
//   - nil if no error chunk was observed, error otherwise.
//
// Side effects:
//   - None.
func (s *StepDefinitions) noErrorShouldBeShown() error {
	if !s.streamUserEscd {
		return errors.New("no user-initiated cancel recorded; preconditions not met")
	}
	if s.streamErrSeen {
		return errors.New("expected no error chunk after user-initiated cancel, but one was observed")
	}
	return nil
}

// theResponseShouldBeIncomplete asserts that the cancel aborted the stream
// before it could emit its full payload.
//
// Returns:
//   - nil if the collected bytes are strictly less than the full expected
//     payload, error otherwise.
//
// Side effects:
//   - None.
func (s *StepDefinitions) theResponseShouldBeIncomplete() error {
	if s.streamFullLen == 0 {
		return errors.New("streamFullLen is zero; `the agent streams a long response` was not called")
	}
	if s.streamDoneChunkRx {
		return errors.New(
			"expected partial response but the stream emitted its final Done chunk — cancel did not interrupt mid-stream",
		)
	}
	if s.streamedLen >= s.streamFullLen {
		return fmt.Errorf(
			"expected partial response (< %d bytes), got complete response (%d bytes)",
			s.streamFullLen, s.streamedLen,
		)
	}
	return nil
}
