package auth_test

import (
	"errors"
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

var _ = Describe("SessionManager", func() {
	var (
		mem  *store.MemoryStore
		cfg  auth.SessionConfig
		mgr  *auth.SessionManager
		now  time.Time
		clock func() time.Time
	)

	BeforeEach(func() {
		mem = store.NewMemoryStore()
		now = time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
		clock = func() time.Time { return now }
		cfg = auth.SessionConfig{
			CookieName:    "flowstate_session",
			CookiePath:    "/api",
			SecureCookies: true,
			Lifetime:      168 * time.Hour,
			Mode:          identity.ModeDeploymentLogin,
			Now:           clock,
		}
		mgr = auth.NewSessionManager(mem, cfg)
	})

	// Plan §"Test Strategy" line 608 — Begin mints a Record with all
	// load-bearing fields populated. The cookie carries the token; the
	// Record carries Mode + PrincipalID + CSRFToken.
	Describe("Begin", func() {
		It("mints a session, persists the Record, sets the cookie", func() {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
			p := identity.Principal{
				ID:          "operator@example.com",
				DisplayName: "Operator",
				Mode:        identity.ModeDeploymentLogin,
			}

			token, err := mgr.Begin(w, r, p)
			Expect(err).NotTo(HaveOccurred())
			Expect(token).NotTo(BeEmpty())

			// 256-bit base64-RawURL → 43 chars
			Expect(token).To(HaveLen(43))

			// Record persisted
			rec, err := mem.Get(r.Context(), token)
			Expect(err).NotTo(HaveOccurred())
			Expect(rec.Token).To(Equal(token))
			Expect(rec.Mode).To(Equal(identity.ModeDeploymentLogin))
			Expect(rec.PrincipalID).To(Equal("operator@example.com"))
			Expect(rec.CSRFToken).NotTo(BeEmpty())
			Expect(rec.CSRFToken).To(HaveLen(43))
			Expect(rec.CSRFToken).NotTo(Equal(token)) // session ≠ csrf
			Expect(rec.CreatedAt).To(Equal(now))
			Expect(rec.ExpiresAt).To(Equal(now.Add(168 * time.Hour)))
			Expect(rec.Data["display_name"]).To(Equal("Operator"))

			// Set-Cookie attrs
			cookies := w.Result().Cookies()
			Expect(cookies).To(HaveLen(1))
			c := cookies[0]
			Expect(c.Name).To(Equal("flowstate_session"))
			Expect(c.Value).To(Equal(token))
			Expect(c.Path).To(Equal("/api"))
			Expect(c.HttpOnly).To(BeTrue())
			Expect(c.Secure).To(BeTrue())
			Expect(c.SameSite).To(Equal(http.SameSiteLaxMode))
			Expect(c.MaxAge).To(Equal(int((168 * time.Hour).Seconds())))
		})

		It("mints distinct tokens on successive calls (CSPRNG)", func() {
			seen := map[string]bool{}
			for i := 0; i < 10; i++ {
				w := httptest.NewRecorder()
				r := httptest.NewRequest(http.MethodPost, "/", nil)
				token, err := mgr.Begin(w, r, identity.Principal{
					ID:   "u",
					Mode: identity.ModeDeploymentLogin,
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(seen[token]).To(BeFalse(), "duplicate token: %s", token)
				seen[token] = true
			}
		})

		It("honours cfg.SecureCookies=false for HTTP dev", func() {
			devCfg := cfg
			devCfg.SecureCookies = false
			devMgr := auth.NewSessionManager(mem, devCfg)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			_, err := devMgr.Begin(w, r, identity.Principal{
				ID:   "u",
				Mode: identity.ModeDeploymentLogin,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(w.Result().Cookies()[0].Secure).To(BeFalse())
		})
	})

	// Plan §"Test Strategy" line 609.
	Describe("Authenticate", func() {
		It("resolves a valid cookie to the persisted Record", func() {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			token, err := mgr.Begin(w, r, identity.Principal{
				ID:          "operator@example.com",
				DisplayName: "Operator",
				Mode:        identity.ModeDeploymentLogin,
			})
			Expect(err).NotTo(HaveOccurred())

			r2 := httptest.NewRequest(http.MethodGet, "/api/chat", nil)
			r2.AddCookie(&http.Cookie{Name: "flowstate_session", Value: token})

			rec, err := mgr.Authenticate(r2)
			Expect(err).NotTo(HaveOccurred())
			Expect(rec.PrincipalID).To(Equal("operator@example.com"))
			Expect(rec.Mode).To(Equal(identity.ModeDeploymentLogin))
		})

		It("returns ErrSessionInvalid when cookie is absent", func() {
			r := httptest.NewRequest(http.MethodGet, "/api/chat", nil)
			_, err := mgr.Authenticate(r)
			Expect(errors.Is(err, auth.ErrSessionInvalid)).To(BeTrue())
		})

		It("returns ErrSessionInvalid when cookie value is empty", func() {
			r := httptest.NewRequest(http.MethodGet, "/api/chat", nil)
			r.AddCookie(&http.Cookie{Name: "flowstate_session", Value: ""})
			_, err := mgr.Authenticate(r)
			Expect(errors.Is(err, auth.ErrSessionInvalid)).To(BeTrue())
		})

		It("returns ErrSessionInvalid when token is not in the store (tampered)", func() {
			r := httptest.NewRequest(http.MethodGet, "/api/chat", nil)
			r.AddCookie(&http.Cookie{Name: "flowstate_session", Value: "nonexistent-token"})
			_, err := mgr.Authenticate(r)
			Expect(errors.Is(err, auth.ErrSessionInvalid)).To(BeTrue())
		})

		// Plan line 610.
		It("returns ErrSessionExpired when Record.ExpiresAt has passed", func() {
			// Mint at time T0.
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			token, err := mgr.Begin(w, r, identity.Principal{
				ID:   "operator@example.com",
				Mode: identity.ModeDeploymentLogin,
			})
			Expect(err).NotTo(HaveOccurred())

			// Advance the clock past ExpiresAt.
			now = now.Add(169 * time.Hour)

			r2 := httptest.NewRequest(http.MethodGet, "/api/chat", nil)
			r2.AddCookie(&http.Cookie{Name: "flowstate_session", Value: token})

			_, err = mgr.Authenticate(r2)
			// Expired records are also dropped by the store's read-time
			// check, which surfaces as ErrSessionInvalid. The SessionManager
			// MUST handle either path — pin both as acceptable.
			Expect(
				errors.Is(err, auth.ErrSessionExpired) ||
					errors.Is(err, auth.ErrSessionInvalid),
			).To(BeTrue(), "want ErrSessionExpired or ErrSessionInvalid; got %v", err)
		})

		// Round-5 B3 fold — mode-mismatch invalidates the session.
		It("returns ErrSessionModeMismatch when Record.Mode != cfg.Mode (B3 fold)", func() {
			// Manually inject a Record under a different mode (simulates
			// operator flipping auth.mode between deployments).
			r := httptest.NewRequest(http.MethodGet, "/api/chat", nil)
			injected := &store.Record{
				Token:       "stale-mode-token",
				Mode:        identity.ModeSharedSecret, // mismatch
				PrincipalID: "default",
				CSRFToken:   "anycsrf",
				CreatedAt:   now,
				ExpiresAt:   now.Add(time.Hour),
			}
			Expect(mem.Put(r.Context(), injected)).To(Succeed())
			r.AddCookie(&http.Cookie{Name: "flowstate_session", Value: "stale-mode-token"})

			_, err := mgr.Authenticate(r)
			Expect(errors.Is(err, auth.ErrSessionModeMismatch)).To(BeTrue())
		})

		// Defensive — when cfg.Mode is empty (test harness shortcut), the
		// mismatch check is skipped. Pin it so the impl doesn't quietly
		// drop the check.
		It("skips mode-mismatch check when cfg.Mode is empty", func() {
			anyMode := auth.NewSessionManager(mem, auth.SessionConfig{
				CookieName:    "flowstate_session",
				CookiePath:    "/api",
				SecureCookies: true,
				Lifetime:      time.Hour,
				Mode:          "",
				Now:           clock,
			})
			r := httptest.NewRequest(http.MethodGet, "/api/chat", nil)
			injected := &store.Record{
				Token:       "any-mode-token",
				Mode:        identity.ModeSharedSecret,
				PrincipalID: "default",
				CSRFToken:   "anycsrf",
				CreatedAt:   now,
				ExpiresAt:   now.Add(time.Hour),
			}
			Expect(mem.Put(r.Context(), injected)).To(Succeed())
			r.AddCookie(&http.Cookie{Name: "flowstate_session", Value: "any-mode-token"})

			rec, err := anyMode.Authenticate(r)
			Expect(err).NotTo(HaveOccurred())
			Expect(rec.Mode).To(Equal(identity.ModeSharedSecret))
		})
	})

	// Plan §"Test Strategy" line 612 — End/Revoke clears the cookie and
	// drops the Record.
	Describe("End", func() {
		It("clears the cookie and Delete's the Record", func() {
			// Mint a session.
			w1 := httptest.NewRecorder()
			r1 := httptest.NewRequest(http.MethodPost, "/", nil)
			token, err := mgr.Begin(w1, r1, identity.Principal{
				ID:   "operator@example.com",
				Mode: identity.ModeDeploymentLogin,
			})
			Expect(err).NotTo(HaveOccurred())

			// End it.
			w2 := httptest.NewRecorder()
			r2 := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
			r2.AddCookie(&http.Cookie{Name: "flowstate_session", Value: token})

			Expect(mgr.End(w2, r2)).To(Succeed())

			// Record gone
			_, err = mem.Get(r2.Context(), token)
			Expect(errors.Is(err, store.ErrSessionNotFound)).To(BeTrue())

			// Clear-cookie Set-Cookie present with MaxAge=-1
			cookies := w2.Result().Cookies()
			Expect(cookies).To(HaveLen(1))
			Expect(cookies[0].Name).To(Equal("flowstate_session"))
			Expect(cookies[0].Value).To(Equal(""))
			Expect(cookies[0].MaxAge).To(BeNumerically("<", 0))
		})

		It("is idempotent on a request without a cookie", func() {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
			Expect(mgr.End(w, r)).To(Succeed())
			// Still writes a clear-cookie header.
			Expect(w.Result().Cookies()).To(HaveLen(1))
		})

		It("is idempotent on a request whose cookie token is already gone", func() {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
			r.AddCookie(&http.Cookie{Name: "flowstate_session", Value: "already-gone"})
			Expect(mgr.End(w, r)).To(Succeed())
		})
	})

	Describe("Revoke", func() {
		It("Delete's the Record for the given token", func() {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			token, err := mgr.Begin(w, r, identity.Principal{
				ID:   "operator@example.com",
				Mode: identity.ModeDeploymentLogin,
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(mgr.Revoke(r.Context(), token)).To(Succeed())

			_, err = mem.Get(r.Context(), token)
			Expect(errors.Is(err, store.ErrSessionNotFound)).To(BeTrue())
		})

		It("is idempotent on an unknown token", func() {
			Expect(mgr.Revoke(httptest.NewRequest(http.MethodPost, "/", nil).Context(),
				"nonexistent")).To(Succeed())
		})
	})

	Describe("Config / SCS getters", func() {
		It("Config returns the SessionConfig the manager was constructed with", func() {
			out := mgr.Config()
			Expect(out.CookieName).To(Equal("flowstate_session"))
			Expect(out.CookiePath).To(Equal("/api"))
			Expect(out.Mode).To(Equal(identity.ModeDeploymentLogin))
			Expect(out.Lifetime).To(Equal(168 * time.Hour))
		})

		It("SCS exposes the underlying scs.SessionManager", func() {
			Expect(mgr.SCS()).NotTo(BeNil())
			// scs's Cookie config mirrors ours.
			Expect(mgr.SCS().Cookie.Name).To(Equal("flowstate_session"))
			Expect(mgr.SCS().Cookie.Path).To(Equal("/api"))
			Expect(mgr.SCS().Cookie.HttpOnly).To(BeTrue())
			Expect(mgr.SCS().Cookie.Secure).To(BeTrue())
		})
	})

	Describe("DefaultSessionConfig", func() {
		It("returns the plan §Wire Protocol defaults", func() {
			def := auth.DefaultSessionConfig()
			Expect(def.CookieName).To(Equal("flowstate_session"))
			Expect(def.CookiePath).To(Equal("/api"))
			Expect(def.SecureCookies).To(BeTrue())
			Expect(def.Lifetime).To(Equal(168 * time.Hour))
		})

		It("leaves Mode unset — caller MUST stamp it", func() {
			def := auth.DefaultSessionConfig()
			Expect(def.Mode).To(BeEmpty())
		})
	})

	Describe("cookie format invariant pin", func() {
		// The cookie header's String() form is the only thing the browser
		// ever sees — pin the load-bearing attributes against future
		// regressions (e.g. a refactor that drops HttpOnly).
		It("renders HttpOnly and Secure flags in the Set-Cookie header", func() {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			_, err := mgr.Begin(w, r, identity.Principal{
				ID:   "u",
				Mode: identity.ModeDeploymentLogin,
			})
			Expect(err).NotTo(HaveOccurred())

			header := w.Result().Header.Get("Set-Cookie")
			Expect(header).NotTo(BeEmpty())
			Expect(strings.Contains(header, "HttpOnly")).To(BeTrue(), "Set-Cookie missing HttpOnly: %s", header)
			Expect(strings.Contains(header, "Secure")).To(BeTrue(), "Set-Cookie missing Secure: %s", header)
			Expect(strings.Contains(header, "SameSite=Lax")).To(BeTrue(), "Set-Cookie missing SameSite=Lax: %s", header)
			Expect(strings.Contains(header, "Path=/api")).To(BeTrue(), "Set-Cookie missing Path=/api: %s", header)
		})
	})
})
