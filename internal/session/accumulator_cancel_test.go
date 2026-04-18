package session_test

import (
	"context"
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
			//nolint:revive // intentional empty drain — we only care that the channel closes.
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
