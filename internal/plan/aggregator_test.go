package plan_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plan"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("Aggregator", func() {
	var agg *plan.Aggregator

	BeforeEach(func() {
		agg = &plan.Aggregator{}
	})

	DescribeTable("Aggregate",
		func(chunks []provider.StreamChunk, expect string, expectErr string) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			DeferCleanup(cancel)

			chunkCh := make(chan provider.StreamChunk, len(chunks))
			for _, c := range chunks {
				chunkCh <- c
			}
			close(chunkCh)

			result, err := agg.Aggregate(ctx, chunkCh)
			if expectErr == "" {
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(expect))
			} else {
				Expect(err).To(MatchError(expectErr))
			}
		},
		Entry("valid stream", []provider.StreamChunk{
			{Content: "Hello, "}, {Content: "world!"},
		}, "Hello, world!", ""),
		Entry("empty stream", []provider.StreamChunk{}, "", "empty stream: no content received"),
		Entry("stream error", []provider.StreamChunk{
			{Content: "partial"}, {Error: errors.New("fail")},
		}, "", "fail"),
		Entry("size limit exceeded", func() []provider.StreamChunk {
			big := make([]byte, 1024*1024+1)
			for i := range big {
				big[i] = 'a'
			}
			return []provider.StreamChunk{{Content: string(big)}}
		}(), "", "plan exceeds maximum size of 1MB"),
	)

	It("returns context.Canceled when context is cancelled before Aggregate is called", func() {
		chunkCh := make(chan provider.StreamChunk)
		cancelledCtx, cancelFunc := context.WithCancel(context.Background())
		cancelFunc()

		result, err := agg.Aggregate(cancelledCtx, chunkCh)
		Expect(result).To(Equal(""))
		Expect(err).To(MatchError(context.Canceled))
	})
})
