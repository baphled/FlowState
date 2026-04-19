package chat_test

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

// drainBatch walks a tea.Cmd and its nested tea.BatchMsg structure,
// invoking every sub-Cmd so closure side effects (like msg.Next capturing
// a bool flag) run. Mirrors cmdEventuallyProducesAppendedMsg's recursion
// pattern but is scoped to execution rather than message-type checking.
func drainBatch(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			drainBatch(sub)
		}
	}
}

// These specs cover P1/D1 — readNextChunk must honour a cancelled stream
// context and return a Done StreamChunkMsg rather than parking forever on
// the stream channel. A previous regression (intent.go:1867-1899) used a
// naked <-i.streamChan receive, so a double-Esc would never unblock the
// reader goroutine even though streamCancel had fired.
var _ = Describe("readNextChunk stream cancellation (D1)", func() {
	var intent *chat.Intent

	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
		intent = chat.NewIntent(chat.IntentConfig{
			AgentID:      "test-agent",
			SessionID:    "test-session",
			ProviderName: "openai",
			ModelName:    "gpt-4o",
			TokenBudget:  4096,
		})
	})

	AfterEach(func() {
		chat.SetRunningInTestsForTest(false)
	})

	Describe("when the stream context is already cancelled when the reader runs", func() {
		It("returns a Done StreamChunkMsg instead of blocking on the channel", func() {
			// Provider goroutine keeps sending chunks — the cancel must take
			// precedence over the channel receive. The channel is never
			// closed, so a naked <-streamChan would block forever.
			ch := make(chan provider.StreamChunk)
			go func() {
				tick := time.NewTicker(5 * time.Millisecond)
				defer tick.Stop()
				for range tick.C {
					select {
					case ch <- provider.StreamChunk{Content: "x"}:
					default:
					}
				}
			}()
			intent.SetStreamChanForTest(ch)

			// Install a cancel func and pre-cancel it. readNextChunk must
			// then observe i.streamCtx.Done() and return without blocking
			// on the channel. We fire the cancel func directly rather than
			// through cancelActiveStream so streamCtx stays populated —
			// otherwise the fallback non-ctx path would mask the fix.
			_ = intent.InstallStreamCancelForTest()
			intent.FireStreamCancelForTest()

			done := make(chan chat.StreamChunkMsg, 1)
			go func() {
				msg, ok := intent.ReadNextChunkForTest().(chat.StreamChunkMsg)
				if ok {
					done <- msg
				}
			}()

			select {
			case msg := <-done:
				Expect(msg.Done).To(BeTrue(),
					"readNextChunk must return Done=true after ctx.Done()")
			case <-time.After(500 * time.Millisecond):
				Fail("readNextChunk blocked past ctx.Done() — double-Esc cancel bug")
			}
		})
	})

	Describe("when streamCancel fires while the reader is parked", func() {
		It("unblocks and returns a Done StreamChunkMsg within 200ms", func() {
			// Channel with no senders and no close — a naked receive blocks
			// forever. The ctx-aware select must surface Done once the
			// cancel func is invoked.
			ch := make(chan provider.StreamChunk)
			intent.SetStreamChanForTest(ch)

			_ = intent.InstallStreamCancelForTest()

			done := make(chan chat.StreamChunkMsg, 1)
			go func() {
				msg, ok := intent.ReadNextChunkForTest().(chat.StreamChunkMsg)
				if ok {
					done <- msg
				}
			}()

			// Give the reader a moment to park on the channel, then cancel.
			// Fire the cancel directly rather than cancelActiveStream so
			// the reader's captured ctx is the one we cancel.
			time.Sleep(20 * time.Millisecond)
			intent.FireStreamCancelForTest()

			select {
			case msg := <-done:
				Expect(msg.Done).To(BeTrue(),
					"cancel must propagate to the reader as Done=true")
			case <-time.After(200 * time.Millisecond):
				Fail("readNextChunk did not unblock within 200ms of cancel")
			}
		})
	})

	Describe("the user-facing double-Esc path (cancelActiveStream)", func() {
		It("also unblocks the reader via the captured stream context", func() {
			// Integration-style assertion: the real flow invokes
			// cancelActiveStream, which both cancels the ctx and then
			// clears both streamCtx and streamCancel. The reader goroutine
			// has already captured streamCtx before parking, so it still
			// observes Done().
			ch := make(chan provider.StreamChunk)
			intent.SetStreamChanForTest(ch)

			_ = intent.InstallStreamCancelForTest()

			done := make(chan chat.StreamChunkMsg, 1)
			go func() {
				msg, ok := intent.ReadNextChunkForTest().(chat.StreamChunkMsg)
				if ok {
					done <- msg
				}
			}()

			time.Sleep(20 * time.Millisecond)
			intent.CancelActiveStreamForTest()

			select {
			case msg := <-done:
				Expect(msg.Done).To(BeTrue(),
					"cancelActiveStream must propagate Done=true to the reader")
			case <-time.After(200 * time.Millisecond):
				Fail("readNextChunk did not unblock within 200ms of cancelActiveStream")
			}
		})
	})
})

// Post-cancel chunk suppression: user reports the model continues typing after
// double-Esc. The cancel plumbing unblocks the reader (prior Describe blocks),
// but chunks already buffered in the stream channel — or chunks that race the
// cancel signal across the Update loop — still reach handleStreamChunk and get
// appended to the chat view. The fix must gate chunk application on the
// userCancelled flag so the view stops growing the moment the user requests
// cancellation, even if the provider goroutine keeps emitting into the
// channel.
//
// Tests-first RED at the chat.Intent seam using the real provider abstraction
// and the real handleStreamChunkMsg / handleEscapeKey paths — no mocks of the
// dispatch layer.
var _ = Describe("post-cancel chunk suppression (double-Esc)", func() {
	var intent *chat.Intent

	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
		intent = chat.NewIntent(chat.IntentConfig{
			AgentID:      "test-agent",
			SessionID:    "test-session",
			ProviderName: "openai",
			ModelName:    "gpt-4o",
			TokenBudget:  4096,
		})
		// The double-Esc path is gated on view.IsStreaming(); the production
		// flow flips it true via sendMessage → view.StartStreaming. Mirror
		// that precondition here so handleEscapeKey actually runs.
		intent.SetStreamingForTest(true)
	})

	AfterEach(func() {
		chat.SetRunningInTestsForTest(false)
	})

	Describe("when a chunk arrives after the user cancels via double-Esc", func() {
		It("does not append the post-cancel chunk content to the chat view", func() {
			// A provider stream that emits chunks forever, ignoring ctx. This
			// mirrors candidate failure mode #1 in the handoff — a provider
			// whose goroutine does not drop the channel on ctx cancel, so
			// buffered chunks keep arriving after the user requested stop.
			ch := make(chan provider.StreamChunk, 4)
			intent.SetStreamChanForTest(ch)
			_ = intent.InstallStreamCancelForTest()

			// Deliver one pre-cancel chunk through the full dispatch path.
			// Content "PRE" should land in view.Response (streaming buffer).
			preChunk := chat.StreamChunkMsg{Content: "PRE", Done: false}
			_ = intent.HandleStreamChunkMsgForTest(preChunk)
			Expect(intent.Response()).To(ContainSubstring("PRE"),
				"sanity: pre-cancel chunk must reach the view")

			// Fire the double-Esc path. First press arms, second press
			// cancels. handleEscapeKey sets userCancelled=true and invokes
			// cancelActiveStream, matching the real keybinding path.
			intent.SetLastEscTimeForTest(time.Now())
			_ = intent.HandleEscapeKeyForTest()
			Expect(intent.UserCancelledForTest()).To(BeTrue(),
				"double-Esc must latch userCancelled before any post-cancel chunk arrives")
			Expect(intent.StreamCancelClearedForTest()).To(BeTrue(),
				"cancelActiveStream must have cleared streamCancel")

			// Provider keeps emitting — the channel has buffered capacity
			// and/or the provider goroutine never observed ctx.Done(). This
			// chunk races the cancel across the Update loop.
			postChunk := chat.StreamChunkMsg{Content: "POST-CANCEL", Done: false}
			_ = intent.HandleStreamChunkMsgForTest(postChunk)

			// The view must not have grown. "POST-CANCEL" is the fingerprint
			// the user sees as "the model still continues".
			Expect(intent.Response()).NotTo(ContainSubstring("POST-CANCEL"),
				"post-cancel chunks must not be appended to the chat view")
		})

		It("suppresses a trailing Done chunk's content and error after cancel", func() {
			// A provider may deliver one final Done=true chunk (e.g. a
			// terminal "thinking" or error chunk) after the user cancels.
			// handleStreamChunk routes Done chunks through finaliseChunk
			// which commits accumulated response + content into a permanent
			// message. Post-cancel that commit must not include any
			// post-cancel content.
			ch := make(chan provider.StreamChunk, 2)
			intent.SetStreamChanForTest(ch)
			_ = intent.InstallStreamCancelForTest()

			_ = intent.HandleStreamChunkMsgForTest(chat.StreamChunkMsg{Content: "partial ", Done: false})

			intent.SetLastEscTimeForTest(time.Now())
			_ = intent.HandleEscapeKeyForTest()

			// Post-cancel Done chunk carrying extra content.
			_ = intent.HandleStreamChunkMsgForTest(chat.StreamChunkMsg{Content: "LATE-TAIL", Done: true})

			msgs := intent.AllViewMessagesForTest()
			for _, m := range msgs {
				Expect(m.Content).NotTo(ContainSubstring("LATE-TAIL"),
					"no committed message may contain post-cancel tail content")
			}
		})
	})
})

// Stall regression: after a double-Esc cancel, if the provider never emits a
// terminal Done chunk that reaches the userCancelled gate (because the reader
// is parked on a naked channel receive with a nilled streamCtx — see
// cancelActiveStream clearing both streamCancel AND streamCtx), the
// userCancelled flag stays latched. The *next* user turn sees every chunk
// dropped by the gate at handleStreamChunkMsg, including the Done chunk that
// would ordinarily terminate the spinner. The UI shows a stalled streaming
// state with no chunks landing — the user reports "the assistant stops
// mid-turn and no further chunks arrive, but the UI does not error out".
//
// This is a chat.Intent-layer assertion: userCancelled MUST NOT survive across
// the start of a fresh turn. The contract is that sendMessage (or any path
// that starts a new stream) begins with a clean cancel latch, because the
// previous turn's cancel has already completed as far as the user is
// concerned. Continuing to drop chunks on the new turn's behalf is the bug.
var _ = Describe("userCancelled latch across turns (stall regression)", func() {
	var intent *chat.Intent

	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
		intent = chat.NewIntent(chat.IntentConfig{
			AgentID:      "test-agent",
			SessionID:    "test-session",
			ProviderName: "openai",
			ModelName:    "gpt-4o",
			TokenBudget:  4096,
		})
		intent.SetStreamingForTest(true)
	})

	AfterEach(func() {
		chat.SetRunningInTestsForTest(false)
	})

	Describe("when the user double-Escs and the provider never emits a Done chunk", func() {
		It("delivers chunks on the next turn even when no Done chunk closed the previous one", func() {
			// Arrange: an in-flight stream. User cancels mid-turn — no Done
			// chunk flows through the gate because the reader is parked on
			// the post-cancel channel (streamCtx nilled by
			// cancelActiveStream). The provider goroutine was cancelled at
			// the engine boundary and emits nothing further, so the dispatch
			// loop never reaches the `if msg.Done { userCancelled = false }`
			// clearing path at intent.go:1744-1745.
			ch := make(chan provider.StreamChunk, 4)
			intent.SetStreamChanForTest(ch)
			_ = intent.InstallStreamCancelForTest()

			intent.SetLastEscTimeForTest(time.Now())
			_ = intent.HandleEscapeKeyForTest()

			// Act: a *new* user turn starts. The production flow is
			// sendMessage → per-turn state reset → view.StartStreaming →
			// readNextChunkFrom. BeginTurnForTest mirrors the state-reset
			// half without plumbing an engine; it is the production site
			// where the D1 stall fix belongs (sendMessage clears
			// userCancelled at the turn boundary).
			intent.BeginTurnForTest("fresh user message")
			intent.SetStreamingForTest(true)
			newChunk := chat.StreamChunkMsg{Content: "NEW-TURN-CHUNK", Done: false}
			_ = intent.HandleStreamChunkMsgForTest(newChunk)

			// Assert: the new turn's content reaches the view. If
			// userCancelled is still latched from the previous turn, the
			// gate at intent.go:1743 short-circuits and Response() stays
			// empty — the stall.
			Expect(intent.Response()).To(ContainSubstring("NEW-TURN-CHUNK"),
				"chunks on a fresh turn must land in the view; "+
					"a latched userCancelled from the previous cancel is the stall bug")
		})

		It("schedules msg.Next on the next turn's non-Done chunk so the reader keeps pumping", func() {
			// The user-facing stall has two ingredients: (1) content is
			// dropped, and (2) msg.Next is not chained — so even if a Done
			// chunk eventually arrived it would never be read. The gate at
			// intent.go:1748 returns nil for every non-Done chunk while
			// userCancelled latches, so the reader goroutine halts.
			//
			// This spec pins the pump contract: for a fresh turn after the
			// cancel, handleStreamChunkMsg must return a non-nil Cmd when
			// the chunk carries a Next continuation. The symptom of the
			// latch is a nil return, leaving the Tea loop with no scheduled
			// continuation and the spinner spinning on a dead channel.
			ch := make(chan provider.StreamChunk, 4)
			intent.SetStreamChanForTest(ch)
			_ = intent.InstallStreamCancelForTest()

			intent.SetLastEscTimeForTest(time.Now())
			_ = intent.HandleEscapeKeyForTest()

			intent.BeginTurnForTest("fresh user message")
			intent.SetStreamingForTest(true)
			nextCalled := false
			freshChunk := chat.StreamChunkMsg{
				Content: "x",
				Done:    false,
				Next: func() tea.Msg {
					nextCalled = true
					return chat.StreamChunkMsg{Done: true}
				},
			}
			cmd := intent.HandleStreamChunkMsgForTest(freshChunk)

			Expect(cmd).NotTo(BeNil(),
				"non-Done chunk on a fresh turn must return a follow-up Cmd "+
					"so the reader keeps pumping — a nil return is the stall")
			// Drain the Cmd. handleStreamChunkMsg wraps the reader pump inside
			// a tea.Batch, so we must walk the BatchMsg and invoke each
			// sub-Cmd to actually exercise msg.Next.
			drainBatch(cmd)
			Expect(nextCalled).To(BeTrue(),
				"the returned Cmd must include msg.Next so the reader advances")
		})

		It("does not leak userCancelled across the turn boundary", func() {
			// Complements the two behavioural specs above with an
			// intent-state assertion: by the time the fresh turn begins its
			// work (via BeginTurnForTest, matching the sendMessage reset
			// site), the latch that would otherwise trap the new turn's
			// chunks has already been cleared.
			ch := make(chan provider.StreamChunk, 4)
			intent.SetStreamChanForTest(ch)
			_ = intent.InstallStreamCancelForTest()

			intent.SetLastEscTimeForTest(time.Now())
			_ = intent.HandleEscapeKeyForTest()

			// Precondition: the latch is set after cancel — this is the
			// production contract the post-cancel suppression specs rely on.
			Expect(intent.UserCancelledForTest()).To(BeTrue(),
				"sanity: double-Esc must latch userCancelled for the cancelled turn")

			intent.BeginTurnForTest("fresh user message")

			Expect(intent.UserCancelledForTest()).To(BeFalse(),
				"a fresh turn must start with a cleared latch, otherwise "+
					"handleStreamChunkMsg drops every chunk (STALL)")
		})
	})
})
