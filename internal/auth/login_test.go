package auth_test

import (
	"bytes"
	"encoding/json"
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

var _ = Describe("HandleLogin (B8 mode-fingerprint defence)", func() {
	var (
		mem *store.MemoryStore
		mgr *auth.SessionManager
		now time.Time
	)

	BeforeEach(func() {
		mem = store.NewMemoryStore()
		now = time.Date(2030, 5, 13, 12, 0, 0, 0, time.UTC)
	})

	newMgr := func(mode string) *auth.SessionManager {
		return auth.NewSessionManager(mem, auth.SessionConfig{
			CookieName:    "flowstate_session",
			CookiePath:    "/api",
			SecureCookies: true,
			Lifetime:      168 * time.Hour,
			Mode:          mode,
			Now:           func() time.Time { return now },
		})
	}

	post := func(handler http.HandlerFunc, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// ---- Happy paths ----

	Describe("shared-secret mode (plan §Test Strategy line 623)", func() {
		It("returns 200 + cookie + CSRF token on valid secret", func() {
			src := identity.NewSharedSecretSource("hunter2")
			mgr = newMgr(identity.ModeSharedSecret)
			h := auth.HandleLogin(src, mgr)

			rec := post(h, `{"secret":"hunter2"}`)
			Expect(rec.Code).To(Equal(http.StatusOK))

			// Set-Cookie present
			cookies := rec.Result().Cookies()
			Expect(cookies).To(HaveLen(1))
			Expect(cookies[0].Name).To(Equal("flowstate_session"))
			Expect(cookies[0].Value).NotTo(BeEmpty())

			// Body
			var resp auth.LoginResponse
			Expect(json.NewDecoder(rec.Body).Decode(&resp)).To(Succeed())
			Expect(resp.CSRFToken).NotTo(BeEmpty())
			Expect(resp.Principal.ID).To(Equal("default"))
			Expect(resp.Principal.Mode).To(Equal(identity.ModeSharedSecret))
		})

		It("returns a CSRF token that passes the protected unsafe-method gate", func() {
			src := identity.NewSharedSecretSource("hunter2")
			mgr = newMgr(identity.ModeSharedSecret)
			originCfg := auth.OriginConfig{AllowedOrigins: []string{"localhost:*"}}
			csrfCfg := auth.CSRFConfig{
				AuthKey:        []byte("32-byte-test-key-padding-ok-yes!"),
				CookieName:     "_csrf",
				CookiePath:     "/api",
				SecureCookies:  false,
				TrustedOrigins: []string{"localhost:5173"},
			}
			csrfH := auth.LoginChain(originCfg, csrfCfg, auth.HandleCSRFPrefetch())

			csrfReq := httptest.NewRequest(http.MethodGet, "/api/auth/csrf", nil)
			csrfReq.Header.Set("Sec-Fetch-Site", "same-origin")
			csrfRec := httptest.NewRecorder()
			csrfH.ServeHTTP(csrfRec, csrfReq)
			Expect(csrfRec.Code).To(Equal(http.StatusOK))

			var preflight struct {
				CSRFToken string `json:"csrf_token"`
			}
			Expect(json.NewDecoder(csrfRec.Body).Decode(&preflight)).To(Succeed())
			Expect(preflight.CSRFToken).NotTo(BeEmpty())

			var csrfCookie *http.Cookie
			for _, c := range csrfRec.Result().Cookies() {
				if c.Name == "_csrf" {
					csrfCookie = c
				}
			}
			Expect(csrfCookie).NotTo(BeNil())

			loginH := auth.LoginChain(originCfg, csrfCfg, auth.HandleLogin(src, mgr))
			loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login",
				strings.NewReader(`{"secret":"hunter2"}`))
			loginReq.Header.Set("Origin", "http://localhost:5173")
			loginReq.Header.Set("Sec-Fetch-Site", "same-origin")
			loginReq.Header.Set("Content-Type", "application/json")
			loginReq.Header.Set("X-CSRF-Token", preflight.CSRFToken)
			loginReq.AddCookie(csrfCookie)
			loginRec := httptest.NewRecorder()
			loginH.ServeHTTP(loginRec, loginReq)
			Expect(loginRec.Code).To(Equal(http.StatusOK))

			var loginResp auth.LoginResponse
			Expect(json.NewDecoder(loginRec.Body).Decode(&loginResp)).To(Succeed())
			Expect(loginResp.CSRFToken).NotTo(BeEmpty())

			var sessionCookie *http.Cookie
			for _, c := range loginRec.Result().Cookies() {
				if c.Name == "flowstate_session" {
					sessionCookie = c
				}
				if c.Name == "_csrf" {
					csrfCookie = c
				}
			}
			Expect(sessionCookie).NotTo(BeNil())
			Expect(csrfCookie).NotTo(BeNil())

			protectedH := auth.Protected(originCfg, mgr,
				auth.AuthConfig{Enabled: true, Mode: identity.ModeSharedSecret},
				csrfCfg,
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				}),
			)
			protectedReq := httptest.NewRequest(http.MethodPost, "/api/v1/sessions",
				strings.NewReader(`{"agent_id":"planner"}`))
			protectedReq.Header.Set("Origin", "http://localhost:5173")
			protectedReq.Header.Set("Sec-Fetch-Site", "same-origin")
			protectedReq.Header.Set("Content-Type", "application/json")
			protectedReq.Header.Set("X-CSRF-Token", loginResp.CSRFToken)
			protectedReq.AddCookie(csrfCookie)
			protectedReq.AddCookie(sessionCookie)
			protectedRec := httptest.NewRecorder()
			protectedH.ServeHTTP(protectedRec, protectedReq)

			Expect(protectedRec.Code).To(Equal(http.StatusNoContent), protectedRec.Body.String())
		})

		// Plan §"Test Strategy" line 624.
		It("returns 401 invalid_credentials on wrong secret", func() {
			src := identity.NewSharedSecretSource("hunter2")
			mgr = newMgr(identity.ModeSharedSecret)
			h := auth.HandleLogin(src, mgr)

			rec := post(h, `{"secret":"wrong"}`)
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
			Expect(rec.Body.String()).To(MatchJSON(`{"error":"invalid_credentials"}`))
		})
	})

	Describe("per-deployment-login mode (plan §Test Strategy line 625)", func() {
		It("returns 200 + principal-id from config on valid secret", func() {
			src := identity.NewDeploymentLoginSource("hunter2", "operator@example.com", "Operator")
			mgr = newMgr(identity.ModeDeploymentLogin)
			h := auth.HandleLogin(src, mgr)

			rec := post(h, `{"secret":"hunter2"}`)
			Expect(rec.Code).To(Equal(http.StatusOK))

			var resp auth.LoginResponse
			Expect(json.NewDecoder(rec.Body).Decode(&resp)).To(Succeed())
			Expect(resp.Principal.ID).To(Equal("operator@example.com"))
			Expect(resp.Principal.DisplayName).To(Equal("Operator"))
			Expect(resp.Principal.Mode).To(Equal(identity.ModeDeploymentLogin))
			Expect(resp.CSRFToken).NotTo(BeEmpty())
			// ExpiresAt should be cfg.Now + Lifetime.
			Expect(resp.ExpiresAt).To(Equal(now.Add(168 * time.Hour)))
		})
	})

	// ----------- B8 — load-bearing —------------
	// Plan §"Wire Protocol" line 484: returning 400 with field name leaks
	// active mode. Every login-shape failure MUST collapse to the same
	// 401 wire shape regardless of mode.
	Describe("B8 — uniform 401 invalid_credentials across all login failure shapes (plan line 484)", func() {
		// The B8 matrix: every cell returns identical wire shape.
		//
		//                    | shared-secret | per-deployment | multi-user
		// --------------------|---------------|----------------|-----------
		// shared-shape body   |    happy 200  |    happy 200   |   401
		// multi-user body     |    401        |    401         |   401 (PR4 real)
		// malformed JSON      |    401        |    401         |   401
		// extra unknown field |    401 or 200 |    401 or 200  |   401
		// completely empty {} |    401        |    401         |   401

		newHandler := func(mode string, src identity.Source) http.HandlerFunc {
			mgr = newMgr(mode)
			return auth.HandleLogin(src, mgr)
		}

		// emptyMultiUserSource constructs a MultiUserSource with no users
		// configured (empty path → zero users; every Authenticate returns
		// ErrInvalidCredentials). This is the PR4/C9 replacement for the
		// removed `identity.NewMultiUserSource()` zero-arg stub form —
		// the wire-collapse path produces the same uniform 401 either way.
		emptyMultiUserSource := func() identity.Source {
			src, err := identity.NewMultiUserSource("")
			Expect(err).NotTo(HaveOccurred())
			return src
		}

		expectUniformBody := func(rec *httptest.ResponseRecorder) {
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
			Expect(rec.Header().Get("Content-Type")).To(Equal("application/json"))
			Expect(rec.Body.String()).To(MatchJSON(`{"error":"invalid_credentials"}`))
		}

		// B8 row 1 — multi-user-shape body to a shared-secret-mode server
		// MUST return 401 invalid_credentials, NOT 400 unknown_field:secret.
		It("multi-user-shape body to shared-secret server → uniform 401 (NOT 400)", func() {
			h := newHandler(identity.ModeSharedSecret,
				identity.NewSharedSecretSource("hunter2"))
			rec := post(h, `{"username":"alice","password":"wonderland"}`)
			expectUniformBody(rec)
			Expect(rec.Body.String()).NotTo(ContainSubstring("unknown_field"))
			Expect(rec.Body.String()).NotTo(ContainSubstring("secret"))
		})

		// B8 row 2 — shared-secret-shape body to a multi-user-mode server
		// MUST also return 401 invalid_credentials. PR4/C9: the real
		// MultiUserSource with no users configured returns
		// ErrInvalidCredentials → wire collapse keeps the byte-identical
		// 401 shape that the stub used to produce via ErrNotImplemented.
		It("shared-secret-shape body to multi-user server → uniform 401", func() {
			h := newHandler(identity.ModeMultiUser, emptyMultiUserSource())
			rec := post(h, `{"secret":"hunter2"}`)
			expectUniformBody(rec)
		})

		// B8 row 3 — per-deployment-login server with multi-user-shape body.
		It("multi-user-shape body to per-deployment-login server → uniform 401", func() {
			h := newHandler(identity.ModeDeploymentLogin,
				identity.NewDeploymentLoginSource("hunter2", "op@example.com", ""))
			rec := post(h, `{"username":"alice","password":"wonderland"}`)
			expectUniformBody(rec)
		})

		// B8 row 4 — multi-user server with NO users configured.
		// PR4/C9 replaces the PR2 stub with the real impl; an empty
		// users.json (the bootstrap state per plan §"Bootstrap UX"
		// multi-user) makes every login fail closed with the same
		// uniform 401 the stub used to return.
		It("valid multi-user-shape body to empty-users-multi-user server → uniform 401", func() {
			h := newHandler(identity.ModeMultiUser, emptyMultiUserSource())
			rec := post(h, `{"username":"alice","password":"wonderland"}`)
			expectUniformBody(rec)
		})

		// B8 row 5 — malformed JSON.
		It("malformed JSON to shared-secret server → uniform 401 (plan line 629)", func() {
			h := newHandler(identity.ModeSharedSecret,
				identity.NewSharedSecretSource("hunter2"))
			rec := post(h, `{not valid json`)
			expectUniformBody(rec)
		})

		It("malformed JSON to per-deployment-login server → uniform 401", func() {
			h := newHandler(identity.ModeDeploymentLogin,
				identity.NewDeploymentLoginSource("hunter2", "op", ""))
			rec := post(h, `{not valid json`)
			expectUniformBody(rec)
		})

		It("malformed JSON to multi-user server → uniform 401", func() {
			h := newHandler(identity.ModeMultiUser, emptyMultiUserSource())
			rec := post(h, `{not valid json`)
			expectUniformBody(rec)
		})

		// B8 row 6 — completely empty body.
		It("empty body to shared-secret server → uniform 401", func() {
			h := newHandler(identity.ModeSharedSecret,
				identity.NewSharedSecretSource("hunter2"))
			rec := post(h, ``)
			expectUniformBody(rec)
		})

		// B8 row 7 — extra unknown field with WRONG secret → uniform 401
		// (plan line 630: 401 NOT 400-with-field-name). Per the round-3
		// design DisallowUnknownFields=false; extra fields are silently
		// dropped. With the correct secret, this returns 200 happy; with
		// the wrong secret, the wire shape stays 401 — both paths preserve
		// the B8 invariant (no field-name leakage).
		It("extra unknown field + wrong secret → uniform 401, no field-name leak", func() {
			h := newHandler(identity.ModeSharedSecret,
				identity.NewSharedSecretSource("hunter2"))
			rec := post(h, `{"secret":"wrong","extra":"y"}`)
			expectUniformBody(rec)
			Expect(rec.Body.String()).NotTo(ContainSubstring("unknown_field"))
			Expect(rec.Body.String()).NotTo(ContainSubstring("extra"))
		})

		// B8 invariant pin — the cross-mode probe matrix.
		// For each (server mode, probe body shape) pair, the failure
		// wire shape MUST be byte-identical.
		It("cross-mode probe matrix: all (mode, body) failure pairs return byte-identical 401", func() {
			modes := []struct {
				mode string
				src  identity.Source
			}{
				{identity.ModeSharedSecret, identity.NewSharedSecretSource("hunter2")},
				{identity.ModeDeploymentLogin, identity.NewDeploymentLoginSource("hunter2", "op", "")},
				{identity.ModeMultiUser, emptyMultiUserSource()},
			}
			probes := []string{
				`{"secret":"wrong"}`,
				`{"username":"alice","password":"wrong"}`,
				`{not valid json`,
				``,
				`{}`,
				`{"secret":"wrong","extra":"y"}`,
				`{"username":"alice","password":"wrong","extra":"y"}`,
			}

			seen := map[string]bool{}
			for _, m := range modes {
				h := newHandler(m.mode, m.src)
				for _, probe := range probes {
					rec := post(h, probe)
					if rec.Code == http.StatusOK {
						// Skip happy paths (none expected with the
						// probe shapes above, but a future probe might
						// be a valid login).
						continue
					}
					sig := rec.Header().Get("Content-Type") + "|" + rec.Body.String()
					seen[sig] = true
				}
			}
			// All failure responses MUST collapse to ONE signature.
			Expect(seen).To(HaveLen(1),
				"B8 violated: failure responses are not byte-identical across (mode, body) pairs. Distinct signatures: %v", seen)
			for sig := range seen {
				Expect(sig).To(ContainSubstring("application/json"))
				Expect(sig).To(ContainSubstring(`{"error":"invalid_credentials"}`))
			}
		})
	})

	Describe("HandleLogin method discipline", func() {
		It("returns 405 on non-POST", func() {
			h := auth.HandleLogin(
				identity.NewSharedSecretSource("hunter2"),
				newMgr(identity.ModeSharedSecret),
			)
			req := httptest.NewRequest(http.MethodGet, "/api/auth/login", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusMethodNotAllowed))
		})
	})

	Describe("HandleLogout (plan §Test Strategy line 627)", func() {
		It("clears the cookie and deletes the Record", func() {
			// Login first.
			mgr = newMgr(identity.ModeDeploymentLogin)
			loginH := auth.HandleLogin(
				identity.NewDeploymentLoginSource("hunter2", "op@example.com", ""),
				mgr,
			)
			loginRec := post(loginH, `{"secret":"hunter2"}`)
			Expect(loginRec.Code).To(Equal(http.StatusOK))
			cookie := loginRec.Result().Cookies()[0]

			// Logout.
			logoutH := auth.HandleLogout(mgr)
			req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
			req.AddCookie(cookie)
			rec := httptest.NewRecorder()
			logoutH.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))
			cookies := rec.Result().Cookies()
			Expect(cookies).To(HaveLen(1))
			Expect(cookies[0].MaxAge).To(BeNumerically("<", 0))

			// Record gone.
			_, err := mem.Get(req.Context(), cookie.Value)
			Expect(err).To(HaveOccurred())
		})

		It("is idempotent on no-cookie request", func() {
			mgr = newMgr(identity.ModeDeploymentLogin)
			h := auth.HandleLogout(mgr)
			req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("returns 405 on non-POST", func() {
			mgr = newMgr(identity.ModeDeploymentLogin)
			h := auth.HandleLogout(mgr)
			req := httptest.NewRequest(http.MethodGet, "/api/auth/logout", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusMethodNotAllowed))
		})
	})

	// ParseCredentials handles binary-body case: very large body, etc.
	// Pinning a couple of edge shapes here so future refactors don't drop
	// the B8 wire collapse.
	Describe("HandleLogin edge shapes", func() {
		It("returns 401 on a binary body that isn't JSON", func() {
			h := auth.HandleLogin(
				identity.NewSharedSecretSource("hunter2"),
				newMgr(identity.ModeSharedSecret),
			)
			req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
				bytes.NewReader([]byte{0x00, 0x01, 0x02, 0x03}))
			req.Header.Set("Content-Type", "application/octet-stream")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
			Expect(rec.Body.String()).To(MatchJSON(`{"error":"invalid_credentials"}`))
		})

		It("returns 401 on JSON-array body (not JSON-object)", func() {
			h := auth.HandleLogin(
				identity.NewSharedSecretSource("hunter2"),
				newMgr(identity.ModeSharedSecret),
			)
			rec := post(h, `["secret","hunter2"]`)
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})
	})
})
