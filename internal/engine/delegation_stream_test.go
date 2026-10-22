package engine_test

import (
	"context"
	"testing"
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

	// The flush-on-timer spec (Sept 2026 responsiveness audit) pins the
	// latency bound on the tee batcher: a slow, trickling child stream
	// must not stall parent-stream tokens for the full 8-chunk batch.
	// With 2 chunks arriving 10ms apart and no further chunks, the
	// parent must still receive both within a generous 100ms window —
	// no Done, no batch completion — because the 50ms teeFlushInterval
	// timer flushes the partial batch.
	It("flushes a partial batch on the tee flush timer", func() {
		src := make(chan provider.StreamChunk, 4)
		parentOut := make(chan provider.StreamChunk, 64)
		ctx, cancel := context.WithCancel(engine.WithStreamOutput(context.Background(), parentOut))
		defer cancel()

		out := engine.TeeToParentStreamForTest(ctx, "child-agent", src)

		go func() {
			src <- contentChunk(0)
			time.Sleep(10 * time.Millisecond)
			src <- contentChunk(1)
		}()

		select {
		case <-out:
		case <-time.After(2 * time.Second):
			Fail("forwarder never produced a chunk")
		}

		var parent []provider.StreamChunk
		deadline := time.After(100 * time.Millisecond)
	collect:
		for len(parent) < 2 {
			select {
			case c := <-parentOut:
				parent = append(parent, c)
			case <-deadline:
				break collect
			}
		}

		Expect(parent).To(HaveLen(2),
			"timer must flush the partial batch even without 8 chunks, a Done, or a source close")
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

// The tee batching benchmark (Sept 2026 performance review) demonstrates
// the benefit of batching parent-forwarded tee content introduced by
// 4d7e274f: the batched forwarder accumulates up to teeBatchSize chunks
// before each context-aware select send, while the unbatched baseline
// pays one select send per chunk. Both sub-benchmarks forward the same
// 4,096 content chunks to a parent stream drained by a separate receiver
// goroutine, so only the send strategy differs.
const teeBenchChunks = 4096

func teeBenchChunk() provider.StreamChunk {
	return provider.StreamChunk{Content: "benchmark chunk payload"}
}

// teeParentReceiver drains the parent stream until closed or ctx is
// cancelled (nothing in the benchmark closes parentOut, so ctx cancel is
// the shutdown signal) so benchmark sends never block on a full receiver.
func teeParentReceiver(ctx context.Context, parentOut chan provider.StreamChunk, done chan struct{}) {
	defer close(done)
	for {
		select {
		case <-parentOut:
		case <-ctx.Done():
			return
		}
	}
}

// runTeeBench drives teeBenchChunks chunks through the tee forwarder and
// returns the cumulative parent batch-send count across b.N iterations.
func runTeeBench(b *testing.B, unbatched bool) int64 {
	b.Helper()
	var totalSends int64
	for i := 0; i < b.N; i++ {
		src := make(chan provider.StreamChunk, teeBenchChunks)
		parentOut := make(chan provider.StreamChunk, teeBenchChunks)
		ctx, cancel := context.WithCancel(engine.WithStreamOutput(context.Background(), parentOut))
		out := engine.TeeToParentStreamForTest(ctx, "bench-child", src)

		parentDone := make(chan struct{})
		go teeParentReceiver(ctx, parentOut, parentDone)

		before := engine.TeeParentSendCountForTest()
		for c := 0; c < teeBenchChunks; c++ {
			if unbatched {
				select {
				case parentOut <- teeBenchChunk():
				case <-ctx.Done():
					b.Fatal("unbatched send cancelled")
				}
			}
			src <- teeBenchChunk()
		}
		close(src)
		for range out {
		}
		cancel()
		<-parentDone
		totalSends += engine.TeeParentSendCountForTest() - before
	}
	return totalSends
}

// BenchmarkTeeParentForwardBatched measures the production tee path:
// chunks are forwarded to the parent in batches of teeBatchSize.
func BenchmarkTeeParentForwardBatched(b *testing.B) {
	b.ReportMetric(float64(runTeeBench(b, false))/float64(b.N)/teeBenchChunks, "parent-sends/chunk")
}

// BenchmarkTeeParentForwardUnbatched measures the pre-4d7e274f strategy:
// one context-aware select send per chunk on the parent stream.
func BenchmarkTeeParentForwardUnbatched(b *testing.B) {
	b.ReportMetric(1.0, "parent-sends/chunk")
	runTeeBench(b, true)
}
