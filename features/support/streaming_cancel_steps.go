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
	// streamFullLen is later read by theResponseShouldBeIncomplete from the
	// step-executing goroutine; the write happens-before the drain goroutine
	// starts (iSend installs it), so this setter is technically race-free
	// today. Guard it anyway so the invariant "all stream-cancel fields are
	// mutex-protected" is uniform and holds under future refactors.
	s.streamMu.Lock()
	s.streamFullLen = LongStreamFullLen()
	s.streamMu.Unlock()
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

	// Initialise all streaming-cancel state under the mutex so no
	// observer can see a half-constructed pre-send snapshot.
	s.streamMu.Lock()
	s.streamCtx = ctx
	s.streamCancel = cancel
	s.streamDrainDone = make(chan struct{})
	s.streamCancelled = false
	s.streamErrSeen = false
	s.streamedLen = 0
	s.streamUserEscd = false
	s.streamDoneChunkRx = false
	s.responseParts = nil
	drainDone := s.streamDrainDone
	s.streamMu.Unlock()

	ch, err := s.app.provider.Stream(ctx, ChatRequest{
		Model:    "mock",
		Messages: s.app.messages,
	})
	if err != nil {
		cancel()
		close(drainDone)
		return err
	}

	go func() {
		defer close(drainDone)
		for chunk := range ch {
			// Every field written here is also read by step
			// assertions on the godog goroutine, so each chunk
			// mutation is guarded. The mutex is held only for the
			// duration of the state write, not across channel reads.
			s.streamMu.Lock()
			if chunk.Error != nil {
				s.streamErrSeen = true
				s.streamMu.Unlock()
				continue
			}
			if chunk.Done {
				s.streamDoneChunkRx = true
			}
			s.responseParts = append(s.responseParts, chunk.Content)
			s.streamedLen += len(chunk.Content)
			s.streamMu.Unlock()
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
		// Take a snapshot of the slice header under the mutex, then
		// inspect the snapshot lock-free. The snapshot is safe to
		// read outside the lock because hasVisibleToken only reads
		// the string contents captured at snapshot time — the drain
		// goroutine never mutates strings already appended, only the
		// slice header (pointer/len/cap).
		s.streamMu.Lock()
		parts := s.responseParts
		s.streamMu.Unlock()
		if hasVisibleToken(parts) {
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
	// Copy the cancel func under the mutex so we do not call it while
	// holding the lock (context.cancel can call into user code).
	s.streamMu.Lock()
	cancel := s.streamCancel
	s.streamMu.Unlock()
	if cancel == nil {
		return errors.New("no active stream to cancel; call `I send` first")
	}
	// First Esc arms the interrupt; second Esc within the window fires it.
	// The mock harness models this directly via streamCancel — the 500ms
	// timing is enforced at the TUI layer, validated by the Ginkgo specs.
	s.streamMu.Lock()
	s.streamUserEscd = true
	s.streamCancelled = true
	s.streamMu.Unlock()
	cancel()
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
	// Snapshot the drain channel and the cancelled flag together under
	// the mutex so we observe a consistent view. Channel sends (close)
	// from the drain goroutine happen-before the receive below, so the
	// waiting select is safe once we've captured the channel reference.
	s.streamMu.Lock()
	cancelled := s.streamCancelled
	drainDone := s.streamDrainDone
	s.streamMu.Unlock()

	if !cancelled {
		return errors.New("stream was never cancelled by the test")
	}
	if drainDone == nil {
		return errors.New("no drain goroutine to await")
	}
	select {
	case <-drainDone:
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
	s.streamMu.Lock()
	userEscd := s.streamUserEscd
	errSeen := s.streamErrSeen
	s.streamMu.Unlock()

	if !userEscd {
		return errors.New("no user-initiated cancel recorded; preconditions not met")
	}
	if errSeen {
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
	s.streamMu.Lock()
	fullLen := s.streamFullLen
	doneChunkRx := s.streamDoneChunkRx
	streamedLen := s.streamedLen
	s.streamMu.Unlock()

	if fullLen == 0 {
		return errors.New("streamFullLen is zero; `the agent streams a long response` was not called")
	}
	if doneChunkRx {
		return errors.New(
			"expected partial response but the stream emitted its final Done chunk — cancel did not interrupt mid-stream",
		)
	}
	if streamedLen >= fullLen {
		return fmt.Errorf(
			"expected partial response (< %d bytes), got complete response (%d bytes)",
			fullLen, streamedLen,
		)
	}
	return nil
}
