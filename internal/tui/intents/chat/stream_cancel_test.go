package chat_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

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
