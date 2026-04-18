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
