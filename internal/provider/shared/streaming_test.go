package shared_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/shared"
)

var _ = Describe("SendChunk", func() {
	Context("when the context is active", func() {
		It("sends the chunk to the channel and returns true", func() {
			ctx := context.Background()
			ch := make(chan provider.StreamChunk, 1)
			chunk := provider.StreamChunk{Content: "hello"}

			result := shared.SendChunk(ctx, ch, chunk)

			Expect(result).To(BeTrue())
			Expect(<-ch).To(Equal(chunk))
		})
	})

	Context("when the context is already cancelled", func() {
		It("sends an error chunk and returns false", func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			ch := make(chan provider.StreamChunk, 2)
			chunk := provider.StreamChunk{Content: "hello"}

			result := shared.SendChunk(ctx, ch, chunk)

			Expect(result).To(BeFalse())
			received := <-ch
			Expect(received.Done).To(BeTrue())
			Expect(errors.Is(received.Error, context.Canceled)).To(BeTrue())
		})

		It("does not block when the channel is full", func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			ch := make(chan provider.StreamChunk) // unbuffered = always full

			result := shared.SendChunk(ctx, ch, provider.StreamChunk{Content: "hello"})

			Expect(result).To(BeFalse())
		})
	})
})
