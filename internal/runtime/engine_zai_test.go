package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider/registry"
	"github.com/baphled/flowstate/internal/provider/zai"
	"github.com/baphled/flowstate/internal/session"
)

func newSSEServer(chunks ...string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, c := range chunks {
			_, _ = w.Write([]byte("data: " + c + "\n\n"))
			fl.Flush()
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		fl.Flush()
	}))
	DeferCleanup(srv.Close)
	return srv
}

var _ = Describe("Turn with z.ai over HTTP", func() {
	It("persists events for a successful streamed turn", func() {
		srv := newSSEServer(
			`{"choices":[{"delta":{"content":"hel"}}]}`,
			`{"choices":[{"delta":{"content":"lo"}}]}`,
		)
		p := zai.New("test-key", zai.WithBaseURL(srv.URL))
		st := newStore()
		ctx := context.Background()
		sess, err := session.Create(ctx, st, "z.ai", "glm-4.6", session.DefaultHarness, "sha", "greet me")
		Expect(err).NotTo(HaveOccurred())

		res, err := Turn(ctx, st, sess, p)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.FinalContent).To(Equal("hello"))
		evts, err := st.Replay(ctx, sess.ID)
		Expect(err).NotTo(HaveOccurred())
		want := []string{"turn.start", "user.message", "assistant.delta", "assistant.delta", "assistant.message", "turn.end"}
		Expect(types(evts)).To(Equal(want))
		after, _ := st.GetSession(ctx, sess.ID)
		Expect(after.Status).To(Equal("completed"))
	})

	It("persists a provider.error event and fails the session on HTTP 503", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"503","message":"upstream down"}}`))
		}))
		DeferCleanup(srv.Close)
		p := zai.New("test-key", zai.WithBaseURL(srv.URL))
		st := newStore()
		ctx := context.Background()
		sess, err := session.Create(ctx, st, "z.ai", "glm-4.6", session.DefaultHarness, "sha", "greet me")
		Expect(err).NotTo(HaveOccurred())

		_, err = Turn(ctx, st, sess, p)
		Expect(err).To(HaveOccurred())
		evts, err := st.Replay(ctx, sess.ID)
		Expect(err).NotTo(HaveOccurred())
		last := evts[len(evts)-1]
		Expect(last.Type).To(Equal("provider.error"))
		after, _ := st.GetSession(ctx, sess.ID)
		Expect(after.Status).To(Equal("failed"))
	})

	It("resolves z.ai through the registry", func() {
		GinkgoT().Setenv("ZAI_API_KEY", "test-key")
		p, err := registry.Resolve("z.ai")
		Expect(err).NotTo(HaveOccurred())
		zp, ok := p.(*zai.Provider)
		Expect(ok).To(BeTrue())
		Expect(zp.Name()).To(Equal("z.ai"))
	})
})
