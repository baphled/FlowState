package streaming_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
)

type contextCapturingStreamer struct {
	capturedCtx context.Context
	chunks      []provider.StreamChunk
	err         error
}

func (m *contextCapturingStreamer) Stream(ctx context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
	m.capturedCtx = ctx
	if m.err != nil {
		return nil, m.err
	}
	ch := make(chan provider.StreamChunk, len(m.chunks))
	for i := range m.chunks {
		ch <- m.chunks[i]
	}
	close(ch)
	return ch, nil
}

var _ = Describe("SessionContextStreamer", func() {
	var (
		ctx   context.Context
		inner *contextCapturingStreamer
	)

	BeforeEach(func() {
		ctx = context.Background()
		inner = &contextCapturingStreamer{}
	})

	Describe("Interface compliance", func() {
		It("implements the Streamer interface", func() {
			var _ streaming.Streamer = (*streaming.SessionContextStreamer)(nil)
		})
	})

	Describe("Stream", func() {
		Context("when session ID is present", func() {
			It("injects the session ID into the context", func() {
				inner.chunks = []provider.StreamChunk{
					{Content: "hello"},
					{Done: true},
				}

				scs := streaming.NewSessionContextStreamer(inner, func() string {
					return "sess-123"
				}, session.IDKey{})
				_, err := scs.Stream(ctx, "agent-1", "test message")
				Expect(err).NotTo(HaveOccurred())

				val, ok := inner.capturedCtx.Value(session.IDKey{}).(string)
				Expect(ok).To(BeTrue())
				Expect(val).To(Equal("sess-123"))
			})
		})

		Context("when session ID is empty", func() {
			It("does not inject session ID into the context", func() {
				inner.chunks = []provider.StreamChunk{
					{Content: "hello"},
					{Done: true},
				}

				scs := streaming.NewSessionContextStreamer(inner, func() string {
					return ""
				}, session.IDKey{})
				_, err := scs.Stream(ctx, "agent-1", "test message")
				Expect(err).NotTo(HaveOccurred())

				val := inner.capturedCtx.Value(session.IDKey{})
				Expect(val).To(BeNil())
			})
		})

		Context("when passing through to inner streamer", func() {
			It("forwards all chunks from the inner streamer", func() {
				inner.chunks = []provider.StreamChunk{
					{Content: "chunk-1 "},
					{Content: "chunk-2"},
					{Done: true},
				}

				scs := streaming.NewSessionContextStreamer(inner, func() string {
					return "sess-456"
				}, session.IDKey{})
				ch, err := scs.Stream(ctx, "agent-1", "test message")
				Expect(err).NotTo(HaveOccurred())

				var contents []string
				for c := range ch {
					if c.Content != "" {
						contents = append(contents, c.Content)
					}
				}
				Expect(contents).To(Equal([]string{"chunk-1 ", "chunk-2"}))
			})

			It("propagates errors from the inner streamer", func() {
				inner.err = errors.New("inner stream failed")

				scs := streaming.NewSessionContextStreamer(inner, func() string {
					return "sess-789"
				}, session.IDKey{})
				ch, err := scs.Stream(ctx, "agent-1", "test message")
				Expect(err).To(MatchError("inner stream failed"))
				Expect(ch).To(BeNil())
			})
		})

		Context("when session ID changes dynamically", func() {
			It("uses the current value from the getter on each call", func() {
				inner.chunks = []provider.StreamChunk{
					{Content: "data"},
					{Done: true},
				}

				callCount := 0
				scs := streaming.NewSessionContextStreamer(inner, func() string {
					callCount++
					if callCount == 1 {
						return "first-session"
					}
					return "second-session"
				}, session.IDKey{})

				_, err := scs.Stream(ctx, "agent-1", "msg-1")
				Expect(err).NotTo(HaveOccurred())
				val, ok := inner.capturedCtx.Value(session.IDKey{}).(string)
				Expect(ok).To(BeTrue())
				Expect(val).To(Equal("first-session"))

				inner.chunks = []provider.StreamChunk{
					{Content: "data"},
					{Done: true},
				}
				_, err = scs.Stream(ctx, "agent-1", "msg-2")
				Expect(err).NotTo(HaveOccurred())
				val, ok = inner.capturedCtx.Value(session.IDKey{}).(string)
				Expect(ok).To(BeTrue())
				Expect(val).To(Equal("second-session"))
			})
		})
	})
})
