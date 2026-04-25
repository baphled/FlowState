package chat_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/views/chat"
)

var _ = Describe("ChatView streaming render cache", func() {
	var (
		view  *chat.View
		calls int
	)

	BeforeEach(func() {
		view = chat.NewView()
		calls = 0
		// Inject a counting markdown renderer so we can detect when a
		// committed (non-streaming) message gets re-rendered redundantly.
		view.SetMarkdownRenderer(func(content string, _ int) string {
			calls++
			return content
		})
	})

	Context("when streaming is active and no new committed message has been appended", func() {
		It("does not re-render the last committed message on every RenderContent call", func() {
			view.AddMessage(chat.Message{Role: "assistant", Content: "settled reply"})

			// First render warms the cache.
			_ = view.RenderContent(80)
			baseline := calls

			// Now flip into streaming mode (mid-turn) and call RenderContent
			// repeatedly. The committed assistant message above is *not*
			// the one being streamed — its rendered string is cached and
			// must not be recomputed each frame.
			view.SetStreaming(true, "draft response so far")

			for range 50 {
				_ = view.RenderContent(80)
			}

			// The streaming partial response IS allowed to call the
			// markdown renderer (it lives in appendStreamingContent and
			// is not cached). What we forbid is the cached committed
			// message getting re-rendered in the for-loop body. So
			// (calls - baseline) must equal the number of streaming
			// renders only — i.e. it must equal 50, not 100+.
			Expect(calls-baseline).To(BeNumerically("<=", 50),
				"committed message must not be re-rendered each streaming frame")
		})
	})
})
