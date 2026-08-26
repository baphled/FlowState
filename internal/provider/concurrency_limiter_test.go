package provider_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
)

// gatedProvider is a controllable fake Provider whose Stream returns a channel
// the test holds open until it deliberately closes it. This lets a spec assert
// that the concurrency limiter blocks a queued caller until an in-flight
// stream's channel drains and closes.
type gatedProvider struct {
	name string

	// inStream is incremented when inner.Stream is entered and is the count
	// of Stream calls that have proceeded past the semaphore acquire.
	inStream atomic.Int32

	mu       sync.Mutex
	channels []chan provider.StreamChunk

	streamErr error
}

func newGatedProvider(name string) *gatedProvider {
	return &gatedProvider{name: name}
}

func (g *gatedProvider) Name() string { return g.name }

func (g *gatedProvider) Stream(
	ctx context.Context, req provider.ChatRequest,
) (<-chan provider.StreamChunk, error) {
	if g.streamErr != nil {
		return nil, g.streamErr
	}
	g.inStream.Add(1)
	ch := make(chan provider.StreamChunk)
	g.mu.Lock()
	g.channels = append(g.channels, ch)
	g.mu.Unlock()
	return ch, nil
}

// closeOldest closes the earliest still-open stream channel, simulating a
// provider finishing its stream so the limiter can release that slot.
func (g *gatedProvider) closeOldest() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.channels) == 0 {
		return
	}
	ch := g.channels[0]
	g.channels = g.channels[1:]
	close(ch)
}

func (g *gatedProvider) Chat(
	ctx context.Context, req provider.ChatRequest,
) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (g *gatedProvider) Embed(
	ctx context.Context, req provider.EmbedRequest,
) ([]float64, error) {
	return nil, nil
}

func (g *gatedProvider) Models() ([]provider.Model, error) { return nil, nil }

var _ = Describe("ConcurrencyLimitedProvider", func() {
	Describe("Name passthrough", func() {
		It("returns the inner provider's name", func() {
			inner := newGatedProvider("zai")
			limited := provider.NewConcurrencyLimitedProvider(inner, 2)
			Expect(limited.Name()).To(Equal("zai"))
		})
	})

	Describe("Stream slot acquisition", func() {
		It("blocks a third concurrent Stream until a slot frees by channel close", func() {
			inner := newGatedProvider("zai")
			limited := provider.NewConcurrencyLimitedProvider(inner, 2)
			ctx := context.Background()

			// Fill both slots.
			ch1, err := limited.Stream(ctx, provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())
			ch2, err := limited.Stream(ctx, provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())
			_ = ch1
			_ = ch2

			// Launch a third call; it must NOT proceed past acquire.
			var thirdReturned atomic.Bool
			go func() {
				defer GinkgoRecover()
				ch3, err := limited.Stream(ctx, provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())
				_ = ch3
				thirdReturned.Store(true)
			}()

			// The third call must stay blocked while two slots are held.
			Consistently(thirdReturned.Load, 200*time.Millisecond, 20*time.Millisecond).
				Should(BeFalse())
			Expect(inner.inStream.Load()).To(Equal(int32(2)))

			// Drain/close one of the first two streams — releases a slot.
			inner.closeOldest()

			// Now the third call proceeds.
			Eventually(thirdReturned.Load).Should(BeTrue())
			Eventually(func() int32 { return inner.inStream.Load() }).
				Should(Equal(int32(3)))
		})

		It("releases the slot only after the stream channel drains, not before", func() {
			inner := newGatedProvider("zai")
			limited := provider.NewConcurrencyLimitedProvider(inner, 1)
			ctx := context.Background()

			_, err := limited.Stream(ctx, provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			var secondReturned atomic.Bool
			go func() {
				defer GinkgoRecover()
				_, err := limited.Stream(ctx, provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())
				secondReturned.Store(true)
			}()

			// With max=1 and the first channel still open, the second is blocked.
			Consistently(secondReturned.Load, 150*time.Millisecond, 20*time.Millisecond).
				Should(BeFalse())

			// Closing the first channel frees the single slot.
			inner.closeOldest()
			Eventually(secondReturned.Load).Should(BeTrue())
		})

		It("releases the slot immediately when inner.Stream returns an error", func() {
			inner := newGatedProvider("zai")
			inner.streamErr = errors.New("boom")
			limited := provider.NewConcurrencyLimitedProvider(inner, 1)
			ctx := context.Background()

			// First call errors and must release its slot right away.
			_, err := limited.Stream(ctx, provider.ChatRequest{})
			Expect(err).To(MatchError("boom"))

			// A subsequent call must be able to acquire the freed slot.
			done := make(chan struct{})
			go func() {
				defer GinkgoRecover()
				_, err := limited.Stream(ctx, provider.ChatRequest{})
				Expect(err).To(MatchError("boom"))
				close(done)
			}()
			Eventually(done).Should(BeClosed())
		})
	})

	Describe("Context cancellation while queued", func() {
		It("returns ctx.Err() promptly without acquiring a slot", func() {
			inner := newGatedProvider("zai")
			limited := provider.NewConcurrencyLimitedProvider(inner, 1)

			// Occupy the only slot with a background call.
			_, err := limited.Stream(context.Background(), provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			// A queued call with a cancellable ctx should bail when cancelled.
			ctx, cancel := context.WithCancel(context.Background())
			var queuedErr error
			done := make(chan struct{})
			go func() {
				defer GinkgoRecover()
				_, queuedErr = limited.Stream(ctx, provider.ChatRequest{})
				close(done)
			}()

			// It should still be queued (slot held by the first call).
			Consistently(func() bool {
				select {
				case <-done:
					return true
				default:
					return false
				}
			}, 100*time.Millisecond, 20*time.Millisecond).Should(BeFalse())

			cancel()
			Eventually(done).Should(BeClosed())
			Expect(queuedErr).To(MatchError(context.Canceled))
			// The queued call must not have entered inner.Stream.
			Expect(inner.inStream.Load()).To(Equal(int32(1)))
		})
	})

	Describe("Chat", func() {
		It("acquires and releases a slot around the inner Chat call", func() {
			inner := newGatedProvider("zai")
			limited := provider.NewConcurrencyLimitedProvider(inner, 1)
			ctx := context.Background()

			// Two sequential Chat calls must both succeed (slot released by defer).
			_, err := limited.Chat(ctx, provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())
			_, err = limited.Chat(ctx, provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("Stats accessors", func() {
		It("returns MaxConcurrent from the constructor argument", func() {
			inner := newGatedProvider("zai")
			limited := provider.NewConcurrencyLimitedProvider(inner, 3)
			Expect(limited.MaxConcurrent()).To(Equal(3))
		})

		It("returns zero InFlight when no calls are active", func() {
			inner := newGatedProvider("zai")
			limited := provider.NewConcurrencyLimitedProvider(inner, 2)
			Expect(limited.InFlight()).To(Equal(0))
		})

		It("returns zero QueueDepth when no callers are waiting", func() {
			inner := newGatedProvider("zai")
			limited := provider.NewConcurrencyLimitedProvider(inner, 2)
			Expect(limited.QueueDepth()).To(Equal(0))
		})

		It("reports correct InFlight during concurrent Stream calls", func() {
			inner := newGatedProvider("zai")
			limited := provider.NewConcurrencyLimitedProvider(inner, 2)
			ctx := context.Background()

			ch1, err := limited.Stream(ctx, provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())
			ch2, err := limited.Stream(ctx, provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())
			_ = ch1
			_ = ch2

			// Both slots acquired.
			Expect(limited.InFlight()).To(Equal(2))

			// Close one stream — in-flight drops to 1.
			inner.closeOldest()
			Eventually(limited.InFlight).Should(Equal(1))

			// Close the other — in-flight drops to 0.
			inner.closeOldest()
			Eventually(limited.InFlight).Should(Equal(0))
		})

		It("reports QueueDepth while a caller waits for a slot", func() {
			inner := newGatedProvider("zai")
			limited := provider.NewConcurrencyLimitedProvider(inner, 1)
			ctx := context.Background()

			// Occupy the only slot.
			ch1, err := limited.Stream(ctx, provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())
			_ = ch1

			// Launch a second call that must queue.
			var secondReturned atomic.Bool
			go func() {
				defer GinkgoRecover()
				_, err := limited.Stream(ctx, provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())
				secondReturned.Store(true)
			}()

			// The second caller is queued.
			Eventually(limited.QueueDepth).Should(Equal(1))
			Expect(limited.InFlight()).To(Equal(1))

			// Release the slot by closing the stream.
			inner.closeOldest()
			Eventually(secondReturned.Load).Should(BeTrue())

			// Queue drains and in-flight stays at 1 (the former waiter now holds the slot).
			Eventually(limited.QueueDepth).Should(Equal(0))
			Expect(limited.InFlight()).To(Equal(1))
		})
	})
})
