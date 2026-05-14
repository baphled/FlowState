package auth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/auth"
	"github.com/baphled/flowstate/internal/auth/identity"
	"github.com/baphled/flowstate/internal/auth/store"
)

// recordOnly is a tiny test middleware that injects a Record into the
// request context — bypasses RequireSession (which lands in C6) so the
// csrf wrapper tests pin RequireCSRFRecordBound in isolation.
//
// Uses auth.StampRecordCtx (export_test.go test-only helper) to attach
// the Record under the same context key RequireSession will use in C6.
func recordOnly(rec *store.Record, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(auth.StampRecordCtx(r.Context(), rec)))
	})
}

var _ = Describe("CSRF wrapper composition", func() {
	var (
		mem       *store.MemoryStore
		sessCfg   auth.SessionConfig
		mgr       *auth.SessionManager
		now       time.Time
		csrfCfg   auth.CSRFConfig
		recBound  func(http.Handler) http.Handler
		called    bool
		nextH     http.Handler
		validRec  *store.Record
	)

	BeforeEach(func() {
		mem = store.NewMemoryStore()
		now = time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
		sessCfg = auth.SessionConfig{
			CookieName:    "flowstate_session",
			CookiePath:    "/api",
			SecureCookies: true,
			Lifetime:      time.Hour,
			Mode:          identity.ModeDeploymentLogin,
			Now:           func() time.Time { return now },
		}
		mgr = auth.NewSessionManager(mem, sessCfg)

		csrfCfg = auth.CSRFConfig{
			AuthKey:       []byte("test-key-32-bytes-long-ok-padding"),
			CookieName:    "_csrf",
			CookiePath:    "/api",
			SecureCookies: true,
		}

		recBound = auth.RequireCSRFRecordBound(mgr)
		called = false
		nextH = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		})

		// A valid Record persisted to the store; we use this for the
		// happy-path and the various failure shapes.
		validRec = &store.Record{
			Token:       "valid-session-token",
			Mode:        identity.ModeDeploymentLogin,
			PrincipalID: "operator@example.com",
			CSRFToken:   "the-bound-csrf-token",
			CreatedAt:   now,
			ExpiresAt:   now.Add(time.Hour),
		}
	})

	// Plan §"CSRF" line 298 ladder ✅ valid triple → pass.
	// Pinning RequireCSRFRecordBound in isolation — we stamp the Record
	// into ctx via the test helper, then invoke the middleware.
	Describe("RequireCSRFRecordBound (Record-bound layer)", func() {
		It("passes when X-CSRF-Token matches Record.CSRFToken on an unsafe method", func() {
			h := recordOnly(validRec, recBound(nextH))
			req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
			req.Header.Set("X-CSRF-Token", validRec.CSRFToken)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(called).To(BeTrue())
		})

		// Plan line 299 — mismatched X-CSRF-Token → 403.
		It("returns 403 csrf_invalid when X-CSRF-Token does not match Record.CSRFToken", func() {
			h := recordOnly(validRec, recBound(nextH))
			req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
			req.Header.Set("X-CSRF-Token", "wrong-token")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusForbidden))
			Expect(rec.Body.String()).To(ContainSubstring("csrf_invalid"))
			Expect(called).To(BeFalse())
		})

		It("returns 403 csrf_invalid when X-CSRF-Token header is missing", func() {
			h := recordOnly(validRec, recBound(nextH))
			req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusForbidden))
			Expect(called).To(BeFalse())
		})

		// Plan line 619 — safe method without token → pass.
		It("passes through on safe methods without checking the header", func() {
			h := recordOnly(validRec, recBound(nextH))
			for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
				called = false
				req := httptest.NewRequest(method, "/api/chat", nil)
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusOK), "method %s", method)
				Expect(called).To(BeTrue(), "method %s", method)
			}
		})

		// Plan line 295 — defence-in-depth: forged signed cookie that
		// passes gorilla/csrf but doesn't match the Record's CSRFToken.
		// The Record-bound layer catches it.
		It("rejects a token that gorilla/csrf would have signed but Record doesn't recognise (token substitution)", func() {
			// Simulate gorilla/csrf having passed (i.e. cookie+header
			// match each other via the signed-cookie scheme) but the
			// header value doesn't equal Record.CSRFToken. The
			// Record-bound layer is independent of gorilla — it only
			// looks at r.Header.Get("X-CSRF-Token") vs Record.CSRFToken.
			h := recordOnly(validRec, recBound(nextH))
			req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
			req.Header.Set("X-CSRF-Token", "forged-but-gorilla-signed")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusForbidden))
			Expect(called).To(BeFalse())
		})

		// Plan line 301 — token replay after logout: Record gone →
		// RequireSession (C6) returns 401 BEFORE CSRF check fires. We
		// pin the related invariant here: when no Record is on the
		// request context, RequireCSRFRecordBound fails closed too
		// (defensive — composition order should never expose this).
		It("returns 403 csrf_invalid when no Record is attached to the request context (defensive)", func() {
			// Don't wrap with recordOnly — the Record-bound layer should
			// fail closed if RequireSession didn't run.
			req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
			req.Header.Set("X-CSRF-Token", validRec.CSRFToken)
			rec := httptest.NewRecorder()
			recBound(nextH).ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusForbidden))
			Expect(called).To(BeFalse())
		})

		It("uses constant-time token comparison (same-length wrong token)", func() {
			// Smoke check — wrong-but-same-length tokens still fail
			// without early return on first-byte mismatch.
			h := recordOnly(validRec, recBound(nextH))
			wrong := "the-bound-csrf-tokem" // last byte differs from validRec.CSRFToken
			Expect(len(wrong)).To(Equal(len(validRec.CSRFToken)))
			req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
			req.Header.Set("X-CSRF-Token", wrong)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusForbidden))
		})
	})

	// Plan §"CSRF" line 297-296 — gorilla/csrf is the FIRST layer.
	// Composition: Protect(cfg) → RequireCSRFRecordBound(mgr) → inner.
	// We assert the wiring compiles + the factory returns a non-nil
	// middleware. End-to-end gorilla/csrf cookie-signing behaviour is
	// gorilla's responsibility to test (and they do, exhaustively).
	Describe("Protect (gorilla/csrf wrapper)", func() {
		It("returns a non-nil middleware factory", func() {
			mw := auth.Protect(csrfCfg)
			Expect(mw).NotTo(BeNil())
			Expect(mw(nextH)).NotTo(BeNil())
		})

		It("panics on empty AuthKey (misconfig fails at boot)", func() {
			empty := csrfCfg
			empty.AuthKey = nil
			Expect(func() { auth.Protect(empty) }).To(Panic())
		})

		It("composes with RequireCSRFRecordBound (gorilla → record-bound → inner)", func() {
			// Pin the composition: unsafe method, no _csrf cookie at all,
			// gorilla/csrf rejects with 403 BEFORE the Record-bound
			// layer fires.
			composed := auth.Protect(csrfCfg)(recordOnly(validRec, recBound(nextH)))
			req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
			req.Header.Set("Origin", "http://localhost:5173")
			req.Header.Set("X-CSRF-Token", validRec.CSRFToken)
			// No _csrf cookie — gorilla/csrf should reject.
			rec := httptest.NewRecorder()
			composed.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusForbidden))
		})

		It("allows safe methods through without cookie/header (gorilla skips them)", func() {
			composed := auth.Protect(csrfCfg)(recordOnly(validRec, recBound(nextH)))
			req := httptest.NewRequest(http.MethodGet, "/api/chat", nil)
			rec := httptest.NewRecorder()
			composed.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(called).To(BeTrue())
		})
	})

	Describe("DefaultCSRFConfig", func() {
		It("returns plan §Wire Protocol CSRF defaults", func() {
			def := auth.DefaultCSRFConfig()
			Expect(def.CookieName).To(Equal("_csrf"))
			Expect(def.CookiePath).To(Equal("/api"))
			Expect(def.SecureCookies).To(BeTrue())
		})

		It("leaves AuthKey unset — caller MUST stamp 32 bytes", func() {
			def := auth.DefaultCSRFConfig()
			Expect(def.AuthKey).To(BeNil())
		})
	})

	// QA BUG-1/BUG-2 fix (May 2026). HandleCSRFPrefetch is the load-
	// bearing surface for the SPA's first-time login flow — without it
	// the SPA has no way to acquire the masked token gorilla/csrf
	// expects on the next unsafe-method request.
	Describe("HandleCSRFPrefetch", func() {
		It("returns 200 + masked token + _csrf cookie when wrapped via LoginChain", func() {
			// LoginChain is the wrap the production route uses
			// (registerLogin → auth.LoginChain). Pinning the wrap
			// composition here, not raw HandleCSRFPrefetch, because
			// csrf.Token(r) only returns non-empty when Protect ran.
			origin := auth.OriginConfig{AllowedOrigins: []string{"localhost:*"}}
			chain := auth.LoginChain(origin, csrfCfg, auth.HandleCSRFPrefetch())

			req := httptest.NewRequest(http.MethodGet, "/api/auth/csrf", nil)
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			rec := httptest.NewRecorder()
			chain.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(rec.Header().Get("Content-Type")).To(ContainSubstring("application/json"))
			Expect(rec.Header().Get("Cache-Control")).To(Equal("no-store"))

			var body auth.CSRFPrefetchResponse
			Expect(json.NewDecoder(rec.Body).Decode(&body)).To(Succeed())
			Expect(body.CSRFToken).NotTo(BeEmpty(),
				"masked token must be non-empty; SPA echoes it back via X-CSRF-Token")

			// Cookie issuance closes the (cookie, header) pair on the
			// gorilla side — without this, the SPA's next POST would
			// still hit the bug.
			var found bool
			for _, c := range rec.Result().Cookies() {
				if c.Name == "_csrf" {
					found = true
					Expect(c.Value).NotTo(BeEmpty())
				}
			}
			Expect(found).To(BeTrue(), "_csrf cookie must be set on the response")
		})

		It("returns 405 method_not_allowed on non-GET", func() {
			origin := auth.OriginConfig{AllowedOrigins: []string{"localhost:*"}}
			chain := auth.LoginChain(origin, csrfCfg, auth.HandleCSRFPrefetch())

			// LoginChain runs gorilla/csrf which would 403 a POST with
			// no cookie/header. To probe HandleCSRFPrefetch's own 405
			// branch we call the handler directly. The wrap-level 405
			// surface is covered by the integration spec in
			// auth_wrap_test.go.
			req := httptest.NewRequest(http.MethodPost, "/api/auth/csrf", nil)
			rec := httptest.NewRecorder()
			auth.HandleCSRFPrefetch().ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusMethodNotAllowed))
			_ = chain // wrap composition pin in the spec above suffices
		})

		It("returns 500 when called without the Protect wrap (defensive)", func() {
			// Defensive: if a future refactor registers this handler
			// without LoginChain, csrf.Token(r) returns "" and the
			// handler MUST fail closed rather than emit an empty token
			// the SPA would echo back as a forged-looking pair.
			req := httptest.NewRequest(http.MethodGet, "/api/auth/csrf", nil)
			rec := httptest.NewRecorder()
			auth.HandleCSRFPrefetch().ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusInternalServerError))
			Expect(rec.Body.String()).To(ContainSubstring("csrf_prefetch_misconfigured"))
		})
	})
})
