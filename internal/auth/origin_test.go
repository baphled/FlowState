package auth_test

import (
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/auth"
)

var _ = Describe("RequireOrigin middleware", func() {
	var (
		cfg     auth.OriginConfig
		handler http.Handler
		called  bool
	)

	BeforeEach(func() {
		called = false
		cfg = auth.OriginConfig{AllowedOrigins: []string{"localhost:*"}}
		handler = auth.RequireOrigin(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		}))
	})

	// Plan ladder row 1: Origin in allowlist → pass.
	Context("with origin in the allowlist", func() {
		It("passes through to the next handler", func() {
			req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
			req.Header.Set("Origin", "http://localhost:5173")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(called).To(BeTrue())
		})
	})

	// Plan ladder row 2: Origin not in allowlist → 403.
	Context("with origin outside the allowlist", func() {
		It("returns 403 origin_rejected", func() {
			req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
			req.Header.Set("Origin", "http://evil.example")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusForbidden))
			Expect(rec.Body.String()).To(ContainSubstring("origin_rejected"))
			Expect(called).To(BeFalse())
		})
	})

	// Plan ladder row 3: No Origin + Sec-Fetch-Site: same-origin → pass.
	Context("with no Origin header and Sec-Fetch-Site: same-origin", func() {
		It("passes through (browser same-origin request)", func() {
			req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(called).To(BeTrue())
		})
	})

	// Plan ladder row 4: No Origin + Sec-Fetch-Site: cross-site → 403.
	Context("with no Origin header and Sec-Fetch-Site: cross-site", func() {
		It("returns 403 origin_rejected", func() {
			req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusForbidden))
			Expect(called).To(BeFalse())
		})
	})

	// Plan ladder row 5: Empty allowlist + Sec-Fetch-Site: same-origin → pass.
	Context("with an empty allowlist and Sec-Fetch-Site: same-origin", func() {
		It("passes through (same-origin requests are always safe)", func() {
			emptyCfg := auth.OriginConfig{AllowedOrigins: nil}
			h := auth.RequireOrigin(emptyCfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(called).To(BeTrue())
		})
	})

	// Plan ladder row 6: Empty allowlist + cross-site Origin → 403.
	Context("with an empty allowlist and a cross-site Origin", func() {
		It("returns 403 origin_rejected", func() {
			emptyCfg := auth.OriginConfig{AllowedOrigins: nil}
			h := auth.RequireOrigin(emptyCfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
			req.Header.Set("Origin", "http://evil.example")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusForbidden))
			Expect(called).To(BeFalse())
		})
	})

	// Plan ladder row 7: Safe method (GET) skips Origin check.
	Context("with a safe method", func() {
		DescribeTable("passes regardless of Origin",
			func(method string) {
				req := httptest.NewRequest(method, "/api/chat", nil)
				req.Header.Set("Origin", "http://evil.example")
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusOK))
				Expect(called).To(BeTrue())
			},
			Entry("GET", http.MethodGet),
			Entry("HEAD", http.MethodHead),
			Entry("OPTIONS", http.MethodOptions),
		)
	})

	// Plan ladder row 8: WebSocket handshake re-uses validator (shared
	// allowlist source). The WS handler reads cfg.AllowedOrigins and
	// passes it into websocket.AcceptOptions.OriginPatterns; the existing
	// internal/api/websocket_test.go suite covers the WS handshake path.
	// Here we pin the contract: MatchOrigin (the shared helper consumed
	// by both surfaces) agrees on the same allowlist semantics.
	Context("with the shared MatchOrigin helper", func() {
		It("matches the same allowlist the WS handler uses", func() {
			Expect(auth.MatchOrigin("http://localhost:5173", []string{"localhost:*"})).To(BeTrue())
			Expect(auth.MatchOrigin("http://evil.example", []string{"localhost:*"})).To(BeFalse())
			// Patterns can have multiple entries; first match wins.
			Expect(auth.MatchOrigin("http://app.flowstate.local", []string{"localhost:*", "*.flowstate.local"})).To(BeTrue())
		})

		It("returns false on empty origin (caller handles via Sec-Fetch-Site)", func() {
			Expect(auth.MatchOrigin("", []string{"localhost:*"})).To(BeFalse())
		})

		It("treats malformed patterns as non-matches (no panic)", func() {
			// path.Match returns an error for an unterminated bracket;
			// we must defensively treat that as a non-match.
			Expect(auth.MatchOrigin("http://localhost:5173", []string{"[unterminated"})).To(BeFalse())
		})
	})
})
