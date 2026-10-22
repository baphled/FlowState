package auth_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/auth"
	"github.com/baphled/flowstate/internal/auth/identity"
	"github.com/baphled/flowstate/internal/auth/store"
)

var _ = Describe("RequireSession middleware (load-bearing PR2 milestone)", func() {
	var (
		mem    *store.MemoryStore
		mgr    *auth.SessionManager
		now    time.Time
		cfg    auth.AuthConfig
		inner  http.Handler
		called bool
	)

	BeforeEach(func() {
		now = time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
		// QA BUG-3 fix (May 2026): thread the same clock into the
		// MemoryStore so Get's read-time expiry check honours the
		// frozen clock too. See session_test.go BeforeEach for the
		// full rationale.
		clock := func() time.Time { return now }
		mem = store.NewMemoryStore(store.WithNow(clock))
		mgr = auth.NewSessionManager(mem, auth.SessionConfig{
			CookieName:    "flowstate_session",
			CookiePath:    "/api",
			SecureCookies: true,
			Lifetime:      time.Hour,
			Mode:          identity.ModeDeploymentLogin,
			Now:           clock,
		})
		cfg = auth.AuthConfig{
			Enabled: true,
			Mode:    identity.ModeDeploymentLogin,
		}
		called = false
		inner = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			rec := auth.RecordFrom(r)
			if rec != nil {
				w.Header().Set("X-Principal-Id", rec.PrincipalID)
			}
			w.WriteHeader(http.StatusOK)
		})
	})

	mintCookie := func(principal identity.Principal) *http.Cookie {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		_, err := mgr.Begin(w, r, principal)
		Expect(err).NotTo(HaveOccurred())
		return w.Result().Cookies()[0]
	}

	Describe("flag-gated rollout (PR2 ships flag-off; PR5 flips default)", func() {
		It("passes through unconditionally when cfg.Enabled is false", func() {
			cfg.Enabled = false
			h := auth.RequireSession(mgr, cfg)(inner)
			req := httptest.NewRequest(http.MethodGet, "/api/chat", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(called).To(BeTrue())
		})
	})

	Describe("session validation", func() {
		It("returns 200 + attaches Record to ctx on valid cookie", func() {
			cookie := mintCookie(identity.Principal{
				ID:   "operator@example.com",
				Mode: identity.ModeDeploymentLogin,
			})

			h := auth.RequireSession(mgr, cfg)(inner)
			req := httptest.NewRequest(http.MethodGet, "/api/chat", nil)
			req.AddCookie(cookie)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(rec.Header().Get("X-Principal-Id")).To(Equal("operator@example.com"))
			Expect(called).To(BeTrue())
		})

		It("returns 401 unauthenticated on missing cookie", func() {
			h := auth.RequireSession(mgr, cfg)(inner)
			req := httptest.NewRequest(http.MethodGet, "/api/chat", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
			Expect(rec.Body.String()).To(ContainSubstring("unauthenticated"))
			Expect(called).To(BeFalse())
		})

		It("returns 401 on unknown cookie token", func() {
			h := auth.RequireSession(mgr, cfg)(inner)
			req := httptest.NewRequest(http.MethodGet, "/api/chat", nil)
			req.AddCookie(&http.Cookie{Name: "flowstate_session", Value: "ghost-token"})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
			Expect(called).To(BeFalse())
		})

		// Round-5 B3 fold — mode-mismatch → 401 + slog.Warn.
		It("returns 401 when Record.Mode != cfg.Mode (B3 fold)", func() {
			// Mint a cookie under shared-secret mode, then probe a
			// per-deployment-login-mode middleware.
			sharedMgr := auth.NewSessionManager(mem, auth.SessionConfig{
				CookieName:    "flowstate_session",
				CookiePath:    "/api",
				SecureCookies: true,
				Lifetime:      time.Hour,
				Mode:          identity.ModeSharedSecret,
				Now:           func() time.Time { return now },
			})
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			_, err := sharedMgr.Begin(w, r, identity.Principal{
				ID:   "default",
				Mode: identity.ModeSharedSecret,
			})
			Expect(err).NotTo(HaveOccurred())
			cookie := w.Result().Cookies()[0]

			// Probe via the deployment-login middleware.
			h := auth.RequireSession(mgr, cfg)(inner)
			req := httptest.NewRequest(http.MethodGet, "/api/chat", nil)
			req.AddCookie(cookie)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
			Expect(called).To(BeFalse())
		})

		It("returns 401 on expired Record", func() {
			cookie := mintCookie(identity.Principal{
				ID:   "operator@example.com",
				Mode: identity.ModeDeploymentLogin,
			})
			// Advance clock past Lifetime.
			now = now.Add(2 * time.Hour)
			h := auth.RequireSession(mgr, cfg)(inner)
			req := httptest.NewRequest(http.MethodGet, "/api/chat", nil)
			req.AddCookie(cookie)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})

		// Uniform wire response — no information leakage on rejection
		// reason. Matches B8's mode-fingerprint discipline applied to
		// the session layer.
		It("returns identical wire body on every rejection reason", func() {
			h := auth.RequireSession(mgr, cfg)(inner)

			// No cookie
			req1 := httptest.NewRequest(http.MethodGet, "/api/chat", nil)
			rec1 := httptest.NewRecorder()
			h.ServeHTTP(rec1, req1)
			body1 := rec1.Body.String()
			code1 := rec1.Code

			// Unknown token
			req2 := httptest.NewRequest(http.MethodGet, "/api/chat", nil)
			req2.AddCookie(&http.Cookie{Name: "flowstate_session", Value: "ghost"})
			rec2 := httptest.NewRecorder()
			h.ServeHTTP(rec2, req2)

			Expect(rec2.Code).To(Equal(code1))
			Expect(rec2.Body.String()).To(Equal(body1))
		})
	})

	// Plan §"What this plan delivers" line 124 + §"CSRF" line 296 —
	// fixed composition order: Origin → Session → CSRF (gorilla then
	// Record-bound). Pin the order observable via early-exit codes.
	Describe("composition order assertion (Origin → Session → CSRF)", func() {
		var (
			originCfg auth.OriginConfig
			csrfCfg   auth.CSRFConfig
			chain     http.Handler
		)

		BeforeEach(func() {
			originCfg = auth.OriginConfig{AllowedOrigins: []string{"localhost:*"}}
			csrfCfg = auth.CSRFConfig{
				AuthKey:       []byte("32-byte-test-key-padding-ok-yes!"),
				CookieName:    "_csrf",
				CookiePath:    "/api",
				SecureCookies: true,
			}
			chain = auth.Protected(originCfg, mgr, cfg, csrfCfg, inner)
		})

		// Layer 1: Origin fires first. A cross-origin POST with a valid
		// session cookie + valid CSRF triple still gets 403 — because
		// Origin runs BEFORE Session.
		It("Origin rejects cross-origin BEFORE Session check fires", func() {
			cookie := mintCookie(identity.Principal{
				ID:   "operator@example.com",
				Mode: identity.ModeDeploymentLogin,
			})
			req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(""))
			req.Header.Set("Origin", "http://evil.example")
			req.AddCookie(cookie)
			rec := httptest.NewRecorder()
			chain.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusForbidden))
			Expect(rec.Body.String()).To(ContainSubstring("origin_rejected"))
			Expect(called).To(BeFalse())
		})

		// Layer 2: Session fires after Origin. An allowed-origin POST
		// with NO cookie gets 401 unauthenticated (not 403 csrf_invalid
		// — Session catches the missing-cookie case before CSRF).
		It("Session rejects no-cookie request BEFORE CSRF check fires", func() {
			req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(""))
			req.Header.Set("Origin", "http://localhost:5173")
			rec := httptest.NewRecorder()
			chain.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
			Expect(rec.Body.String()).To(ContainSubstring("unauthenticated"))
			Expect(called).To(BeFalse())
		})

		// Layer 1 — safe method bypasses unsafe-only checks. Origin
		// (safe-method skip) + Session (still fires + needs cookie) +
		// CSRF (safe-method skip). A safe GET without a cookie reaches
		// Session, fails with 401.
		It("safe GET without cookie returns 401 (Session still fires on safe methods)", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/chat", nil)
			rec := httptest.NewRecorder()
			chain.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
			Expect(called).To(BeFalse())
		})

		// Composition smoke: safe GET WITH cookie passes through
		// everything (Origin skips safe, Session passes, CSRF skips safe).
		It("safe GET with valid cookie reaches the inner handler", func() {
			cookie := mintCookie(identity.Principal{
				ID:   "operator@example.com",
				Mode: identity.ModeDeploymentLogin,
			})
			req := httptest.NewRequest(http.MethodGet, "/api/chat", nil)
			req.AddCookie(cookie)
			rec := httptest.NewRecorder()
			chain.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(called).To(BeTrue())
		})
	})

	Describe("LoginChain (login endpoint composition — Origin + CSRF, no Session)", func() {
		It("returns a non-nil chain", func() {
			originCfg := auth.OriginConfig{AllowedOrigins: []string{"localhost:*"}}
			csrfCfg := auth.CSRFConfig{
				AuthKey:       []byte("32-byte-test-key-padding-ok-yes!"),
				CookieName:    "_csrf",
				CookiePath:    "/api",
				SecureCookies: true,
			}
			h := auth.LoginChain(originCfg, csrfCfg, inner)
			Expect(h).NotTo(BeNil())
		})
	})
})
