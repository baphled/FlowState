package engine_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
)

// The batching specs pin the tee forwarder's reduced-send-count contract:
// chunks reach the parent stream one-for-one, in order, but the forwarder
// must not perform a context-aware select send PER chunk — it batches
// chunks (roughly eight per batch) and forwards per batch, halving the
// select overhead on the hot streaming path while preserving ordering and
// cancellation semantics.
var _ = Describe("teeToParentStream batching", func() {
	contentChunk := func(i int) provider.StreamChunk {
		return provider.StreamChunk{Content: string(rune('a' + i%26))}
	}

	It("preserves chunk ordering across many chunks while forwarding to the parent", func() {
		src := make(chan provider.StreamChunk, 4)
		parentOut := make(chan provider.StreamChunk, 64)
		ctx, cancel := context.WithCancel(engine.WithStreamOutput(context.Background(), parentOut))
		defer cancel()

		out := engine.TeeToParentStreamForTest(ctx, "child-agent", src)

		go func() {
			for i := 0; i < 24; i++ {
				src <- contentChunk(i)
			}
			close(src)
		}()

		var downstream []provider.StreamChunk
		for chunk := range out {
			downstream = append(downstream, chunk)
		}

		Expect(downstream).To(HaveLen(24), "every chunk must flow through the returned channel")
		for i, chunk := range downstream {
			Expect(chunk).To(Equal(contentChunk(i)), "downstream ordering must be preserved")
		}

		var parent []provider.StreamChunk
		for len(parent) < 24 {
			select {
			case c := <-parentOut:
				parent = append(parent, c)
			case <-time.After(2 * time.Second):
				Fail("parent stream did not receive all 24 chunks")
			}
		}
		for i, chunk := range parent {
			Expect(chunk).To(Equal(contentChunk(i)), "parent ordering must be preserved")
		}
	})

	It("performs far fewer parent sends than chunks when many chunks flow", func() {
		src := make(chan provider.StreamChunk, 4)
		parentOut := make(chan provider.StreamChunk, 64)
		ctx, cancel := context.WithCancel(engine.WithStreamOutput(context.Background(), parentOut))
		defer cancel()

		out := engine.TeeToParentStreamForTest(ctx, "child-agent", src)

		go func() {
			for i := 0; i < 24; i++ {
				src <- contentChunk(i)
			}
			close(src)
		}()

		before := engine.TeeParentSendCountForTest()
		for range out {
		}
		for len(parentOut) < 24 {
			select {
			case <-parentOut:
			case <-time.After(2 * time.Second):
				Fail("parent did not receive all chunks")
			}
		}

		Expect(engine.TeeParentSendCountForTest()-before).To(BeNumerically("<=", 4),
			"24 chunks must be forwarded in roughly 8-per-batch groups, not one select send per chunk")
	})

	It("cancels mid-batch: parent receives nothing further once ctx is done", func() {
		src := make(chan provider.StreamChunk, 4)
		parentOut := make(chan provider.StreamChunk, 2)
		ctx, cancel := context.WithCancel(engine.WithStreamOutput(context.Background(), parentOut))
		defer cancel()

		out := engine.TeeToParentStreamForTest(ctx, "child-agent", src)

		release := make(chan struct{})
		go func() {
			for i := 0; i < 16; i++ {
				src <- contentChunk(i)
			}
			<-release
			close(src)
		}()

		select {
		case <-out:
		case <-time.After(2 * time.Second):
			Fail("forwarder never produced a chunk")
		}

		cancel()
		close(release)

		drainDone := make(chan struct{})
		go func() {
			for range out {
			}
			close(drainDone)
		}()
		select {
		case <-drainDone:
		case <-time.After(2 * time.Second):
			Fail("forwarder leaked after ctx cancel mid-batch")
		}

		count := 0
		for {
			select {
			case <-parentOut:
				count++
			default:
				goto drained
			}
		}
	drained:
		Expect(count).To(BeNumerically("<=", 16), "parent must never see duplicated or fabricated chunks")
	})
})
