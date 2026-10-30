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
	"github.com/baphled/flowstate/internal/store"
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

// zaiTurn captures the observable outcome of one z.ai Turn so each spec
// asserts a single behavioural expectation (arrange/act helper only).
type zaiTurn struct {
	events        []store.RecordedEvent
	finalContent  string
	sessionStatus string
	turnErr       error
	sessionID     string
	store         *store.Store
}

// mustTurnOverHTTP drives a z.ai Turn against the given server; fixture
// failures panic so specs keep exactly one behavioural expectation.
func mustTurnOverHTTP(srv *httptest.Server, prompt string) zaiTurn {
	p := zai.New("test-key", zai.WithBaseURL(srv.URL))
	st := newStore()
	ctx := context.Background()
	sess, err := session.Create(ctx, st, "z.ai", "glm-4.6", session.DefaultHarness, "sha", prompt)
	if err != nil {
		panic(err)
	}
	res, turnErr := Turn(ctx, st, sess, p)
	evts, err := st.Replay(ctx, sess.ID)
	if err != nil {
		panic(err)
	}
	after, err := st.GetSession(ctx, sess.ID)
	if err != nil {
		panic(err)
	}
	final := ""
	if res != nil {
		final = res.FinalContent
	}
	return zaiTurn{events: evts, finalContent: final, sessionStatus: after.Status, turnErr: turnErr, sessionID: sess.ID, store: st}
}

var _ = Describe("Turn with z.ai over HTTP", func() {
	It("returns no error for a successful streamed turn", func() {
		srv := newSSEServer(
			`{"choices":[{"delta":{"content":"hel"}}]}`,
			`{"choices":[{"delta":{"content":"lo"}}]}`,
		)
		out := mustTurnOverHTTP(srv, "greet me")
		Expect(out.turnErr).To(Succeed())
	})

	It("persists the full event stream for a successful streamed turn", func() {
		srv := newSSEServer(
			`{"choices":[{"delta":{"content":"hel"}}]}`,
			`{"choices":[{"delta":{"content":"lo"}}]}`,
		)
		out := mustTurnOverHTTP(srv, "greet me")
		Expect(types(out.events)).To(Equal([]string{"turn.start", "user.message", "assistant.delta", "assistant.delta", "assistant.message", "turn.end"}))
	})

	It("joins streamed deltas into the final content", func() {
		srv := newSSEServer(
			`{"choices":[{"delta":{"content":"hel"}}]}`,
			`{"choices":[{"delta":{"content":"lo"}}]}`,
		)
		out := mustTurnOverHTTP(srv, "greet me")
		Expect(out.finalContent).To(Equal("hello"))
	})

	It("marks the session completed after a successful streamed turn", func() {
		srv := newSSEServer(
			`{"choices":[{"delta":{"content":"hel"}}]}`,
			`{"choices":[{"delta":{"content":"lo"}}]}`,
		)
		out := mustTurnOverHTTP(srv, "greet me")
		Expect(out.sessionStatus).To(Equal("completed"))
	})

	It("persists a provider.error event on HTTP 503", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"503","message":"upstream down"}}`))
		}))
		DeferCleanup(srv.Close)
		out := mustTurnOverHTTP(srv, "greet me")
		Expect(out.events[len(out.events)-1].Type).To(Equal("provider.error"))
	})

	It("fails the turn on HTTP 503", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"503","message":"upstream down"}}`))
		}))
		DeferCleanup(srv.Close)
		out := mustTurnOverHTTP(srv, "greet me")
		Expect(out.turnErr).To(HaveOccurred())
	})

	It("marks the session failed on HTTP 503", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"503","message":"upstream down"}}`))
		}))
		DeferCleanup(srv.Close)
		out := mustTurnOverHTTP(srv, "greet me")
		Expect(out.sessionStatus).To(Equal("failed"))
	})

	Context("resolves z.ai through the registry", func() {
		var observedIsZai bool
		var observedProviderName string
		BeforeEach(func() {
			GinkgoT().Setenv("ZAI_API_KEY", "test-key")
			p, err := registry.Resolve("z.ai")
			Expect(err).NotTo(HaveOccurred())
			zp, ok := p.(*zai.Provider)
			observedIsZai = ok
			observedProviderName = zp.Name()
		})
		It("resolves a z.ai provider", func() { Expect(observedIsZai).To(BeTrue()) })
		It("reports the z.ai provider name", func() { Expect(observedProviderName).To(Equal("z.ai")) })
	})
})
