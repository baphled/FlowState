package session_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
)

// Covers P1/D2 (partial) — AccumulateStream must honour a cancelled ctx and
// close its returned channel promptly, even when the upstream rawCh keeps
// producing chunks. Regression was at accumulator.go:64-81 where a plain
// `for chunk := range rawCh` parked the goroutine against the rawCh forever
// and deferred close(accumCh) until rawCh drained naturally — which a
// cancelled provider might never do in a timely fashion.
var _ = Describe("AccumulateStream cancellation (D2)", func() {
	var appender *fakeAppender

	BeforeEach(func() {
		appender = &fakeAppender{}
	})

	It("closes the returned channel promptly when ctx is cancelled", func() {
		ctx, cancel := context.WithCancel(context.Background())

		// rawCh never closes and keeps emitting chunks — the only way
		// AccumulateStream can exit is via ctx.Done().
		rawCh := make(chan provider.StreamChunk)
		go func() {
			defer GinkgoRecover()
			tick := time.NewTicker(5 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					select {
					case rawCh <- provider.StreamChunk{Content: "x"}:
					case <-ctx.Done():
						return
					}
				}
			}
		}()

		out := session.AccumulateStream(ctx, appender, "sess-c", "agent-c", rawCh)

		// Give the accumulator a moment to start forwarding, then cancel.
		time.Sleep(20 * time.Millisecond)
		cancel()

		// Drain what's left — the channel must close within a reasonable
		// budget once ctx is done.
		done := make(chan struct{})
		go func() {
			defer close(done)

			for range out {
			}
		}()

		select {
		case <-done:
			// channel closed — AccumulateStream honoured ctx.Done()
		case <-time.After(500 * time.Millisecond):
			Fail("AccumulateStream did not close its channel within 500ms of ctx cancel")
		}
	})

	It("still forwards all chunks when ctx is never cancelled (regression guard)", func() {
		ctx := context.Background()

		rawCh := make(chan provider.StreamChunk, 3)
		rawCh <- provider.StreamChunk{Content: "a"}
		rawCh <- provider.StreamChunk{Content: "b"}
		rawCh <- provider.StreamChunk{Done: true}
		close(rawCh)

		out := session.AccumulateStream(ctx, appender, "sess-ok", "agent-ok", rawCh)

		var chunks []provider.StreamChunk
		for chunk := range out {
			chunks = append(chunks, chunk)
		}
		Expect(chunks).To(HaveLen(3),
			"ctx-aware accumulator must not drop chunks when ctx is live")
	})
})

// Covers the user-cancel stop reason (stop-button reliability Slice 2):
// a turn the user stopped must flush its partial content stamped with
// StopReasonUserCancelled so the session manager's surfaced-failure flip
// records a distinguishable failure_reason. Two ingestion paths are
// probed because production can deliver the cancellation either way —
// the engine's terminal Error chunk (toolloop.go) or the accumulator's
// own cancelled ctx, which usually wins the select race because the
// session manager's inflight cancel fires both simultaneously.
var _ = Describe("AccumulateStream user-cancel stamping", func() {
	var appender *fakeAppender

	BeforeEach(func() {
		appender = &fakeAppender{}
	})

	drain := func(out <-chan provider.StreamChunk) {
		timeout := time.After(2 * time.Second)
		for {
			select {
			case _, ok := <-out:
				if !ok {
					return
				}
			case <-timeout:
				Fail("accumulator channel did not close within 2s")
			}
		}
	}

	lastAssistant := func() session.Message {
		for i := len(appender.messages) - 1; i >= 0; i-- {
			if appender.messages[i].Role == "assistant" {
				return appender.messages[i]
			}
		}
		return session.Message{}
	}

	It("stamps user_cancelled when the terminal chunk carries Error context.Canceled", func() {
		rawCh := make(chan provider.StreamChunk, 3)
		rawCh <- provider.StreamChunk{Content: "partial answer"}
		rawCh <- provider.StreamChunk{Error: context.Canceled, Done: true}
		close(rawCh)

		out := session.AccumulateStream(context.Background(), appender, "sess-cancel", "agent-cancel", rawCh)
		drain(out)

		msg := lastAssistant()
		Expect(msg.Role).To(Equal("assistant"))
		Expect(msg.Content).To(Equal("partial answer"))
		Expect(msg.StopReason).To(Equal(session.StopReasonUserCancelled),
			"a user-stopped turn must carry the user_cancelled sentinel, not a wire-truncation misclassification")
	})

	It("stamps user_cancelled when the accumulator ctx is cancelled mid-turn", func() {
		ctx, cancel := context.WithCancel(context.Background())
		rawCh := make(chan provider.StreamChunk, 3)

		out := session.AccumulateStream(ctx, appender, "sess-ctx-cancel", "agent-cancel", rawCh)
		rawCh <- provider.StreamChunk{Content: "partial answer"}
		Eventually(func() bool {
			select {
			case <-out:
				return true
			default:
				return false
			}
		}, "2s").Should(BeTrue())

		cancel()
		rawCh <- provider.StreamChunk{Error: context.Canceled, Done: true}
		close(rawCh)
		drain(out)

		Expect(lastAssistant().StopReason).To(Equal(session.StopReasonUserCancelled),
			"the ctx-aware flush path must stamp the cancel sentinel — production stop flows reach this branch when it pre-empts the terminal chunk")
	})

	It("does not stamp user_cancelled for a non-cancel terminal error", func() {
		rawCh := make(chan provider.StreamChunk, 3)
		rawCh <- provider.StreamChunk{Content: "partial answer"}
		rawCh <- provider.StreamChunk{Error: errors.New("wire cut"), Done: true}
		close(rawCh)

		out := session.AccumulateStream(context.Background(), appender, "sess-err", "agent-err", rawCh)
		drain(out)

		Expect(lastAssistant().StopReason).To(Equal(""),
			"a non-cancel terminal error must not claim the cancel sentinel")
	})

	It("preserves an explicit upstream stop reason over the cancel stamp", func() {
		rawCh := make(chan provider.StreamChunk, 3)
		rawCh <- provider.StreamChunk{Content: "partial answer"}
		rawCh <- provider.StreamChunk{EventType: "stop_reason", StopReason: "end_turn"}
		rawCh <- provider.StreamChunk{Error: context.Canceled, Done: true}
		close(rawCh)

		out := session.AccumulateStream(context.Background(), appender, "sess-reason", "agent-reason", rawCh)
		drain(out)

		Expect(lastAssistant().StopReason).To(Equal("end_turn"),
			"an explicit upstream stop reason outranks the synthetic cancel sentinel")
	})
})
