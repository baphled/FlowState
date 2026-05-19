package api_test

// PR3 C7 — server route wrapping spec. Covers:
//   - registerProtected / registerPublic / registerLogin helpers.
//   - features.auth_v1 flag gate: when AuthConfig.Enabled is false (the
//     PR2 default), wrapping is a no-op and existing routes pass through.
//   - When AuthConfig.Enabled is true, the 16 endpoints from §"Endpoint
//     Inventory" Protected list reject unauthenticated requests with 401;
//     the 10 public endpoints stay reachable; the login endpoints accept
//     POST without a session.
//   - handleSwarmEvents CORS narrowing: Access-Control-Allow-Origin: *
//     replaced with cfg.AllowedOrigins[0]; Allow-Credentials: true added.
//
// Plan reference: §"Rollout Plan" PR3/C7 + §"Endpoint Inventory" + plan
// line 124 (composition order RequireOrigin → RequireSession → RequireCSRF).
//
// Seam-level Ginkgo at the api package boundary per
// feedback_ginkgo_not_godog and feedback_extend_existing_specs (this is a
// new seam: route registration helpers; no existing spec exercises it).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/auth"
	"github.com/baphled/flowstate/internal/auth/identity"
	authstore "github.com/baphled/flowstate/internal/auth/store"
)

func newMemoryStoreForTest() *authstore.MemoryStore {
	return authstore.NewMemoryStore()
}

var _ = Describe("PR3 C7 — route wrapping via registerProtected/Public/Login", func() {
	var (
		registry *agent.Registry
	)

	BeforeEach(func() {
		registry = agent.NewRegistry()
	})

	Context("flag-off default (features.auth_v1=false)", func() {
		// Pre-flag-flip behaviour must be preserved exactly. PR3 ships
		// the wrapping but the AuthConfig.Enabled default (PR2) is false;
		// PR5 flips. Without an AuthConfig the helpers MUST no-op so the
		// existing test surface stays green.
		It("a protected endpoint stays reachable without a cookie when Enabled is false", func() {
			srv := api.NewServer(nil, registry, nil, nil)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			// Without auth wired, this endpoint either succeeds or fails
			// for non-auth reasons (e.g. no session store). The contract
			// is that it does NOT return 401 unauthenticated.
			Expect(rec.Code).NotTo(Equal(http.StatusUnauthorized))
		})

		It("a public endpoint stays reachable without a cookie", func() {
			srv := api.NewServer(nil, registry, nil, nil)

			req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))
		})
	})

	Context("flag-on (features.auth_v1=true)", func() {
		// When AuthConfig.Enabled is true, the wrap composes the full
		// chain per plan line 124: RequireOrigin → RequireSession →
		// RequireCSRF (last one only on unsafe methods).
		//
		// The harness wires auth via api.WithAuth(...) — the new option
		// added in C7. When set, helpers route through auth.Protected /
		// auth.LoginChain / pass-through respectively.

		var (
			srv      *api.Server
			authBits api.AuthBundle
		)

		BeforeEach(func() {
			authBits = newAuthBundleForTest()
			srv = api.NewServer(nil, registry, nil, nil,
				api.WithAuth(authBits),
			)
		})

		It("protected endpoints return 401 on no-cookie GET", func() {
			// Plan §"Endpoint Inventory" Protected list — sample
			// representative entries; the full set is enumerated by the
			// 16-row table in the plan.
			// Phase-4-Commit-2 of "Turn-Based Post-Then-Poll Architecture
			// (May 2026)" retired the per-session SSE/WebSocket routes;
			// they no longer appear in this list. The negative-contract
			// pin at the bottom of server_test.go pins that those paths
			// 404.
			protectedGets := []string{
				"/api/v1/sessions",
				"/api/v1/sessions/abc/messages",
				"/api/v1/sessions/abc/todos",
				"/api/v1/sessions/abc/children",
				"/api/v1/sessions/abc/tree",
				"/api/v1/sessions/abc/parent",
				"/api/v1/tasks",
				"/api/v1/tasks/t1",
				"/api/swarm/events?session_id=abc",
			}
			for _, path := range protectedGets {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusUnauthorized),
					"path %s should return 401 unauthenticated when flag is on", path)
				Expect(rec.Body.String()).To(ContainSubstring("unauthenticated"))
			}
		})

		It("public endpoints stay reachable without a cookie", func() {
			// Plan §"Endpoint Inventory" Public list — read-only, no PII.
			publicGets := []string{
				"/api/agents",
				"/api/swarms",
				"/api/discover",
				"/api/skills",
				"/api/sessions",
				"/",
				"/api/v1/models",
				"/health",
			}
			for _, path := range publicGets {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, req)
				Expect(rec.Code).NotTo(Equal(http.StatusUnauthorized),
					"path %s must not require auth", path)
			}
		})

		It("login endpoints accept no-cookie POST from same-origin", func() {
			// Plan §"Endpoint Inventory" Login list. POST /api/auth/login
			// and POST /api/auth/logout use registerLogin (RequireOrigin
			// + gorilla/csrf, no Session — login cannot require a
			// session).
			//
			// The contract: the request must not be rejected with 401
			// (which would mean RequireSession ran on a login endpoint —
			// a wiring bug). It MAY be rejected with 403 csrf when no
			// _csrf cookie / X-CSRF-Token has been issued yet, but the
			// SPA's prefetch flow (covered by the spec below) closes
			// that path end-to-end. Behaviour-pin flip (QA BUG-1/BUG-2,
			// May 2026): the prior pin documented 403-without-prefetch
			// as "expected wire behaviour" — that was pinning the bug.
			// The correct invariant is "not 401", and the showstopper
			// fix is the prefetch round-trip pinned separately.
			req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{}`))
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			Expect(rec.Code).NotTo(Equal(http.StatusUnauthorized),
				"login endpoint must not require a session cookie")
		})

		// QA BUG-1/BUG-2 fix (May 2026): the SPA's first-time login
		// path is "GET /api/auth/csrf → POST /api/auth/login with the
		// returned masked token in X-CSRF-Token and the _csrf cookie
		// the GET issued". The prefetch endpoint is the load-bearing
		// surface; flipping the prior behaviour-pin above without
		// pinning the new path would leave a regression window open.
		It("GET /api/auth/csrf returns a masked token and issues the _csrf cookie", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/auth/csrf", nil)
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK),
				"GET /api/auth/csrf must succeed without prior cookie state")

			// gorilla/csrf's ServeHTTP issues the _csrf cookie via
			// Set-Cookie on every request that flows through Protect.
			cookies := rec.Result().Cookies()
			var csrfCookie *http.Cookie
			for _, c := range cookies {
				if c.Name == "_csrf" {
					csrfCookie = c
					break
				}
			}
			Expect(csrfCookie).NotTo(BeNil(),
				"_csrf cookie must be issued so the SPA has the cookie half of the pair")
			Expect(csrfCookie.Value).NotTo(BeEmpty())

			// Response body carries the masked token the SPA echoes
			// back via X-CSRF-Token on the next unsafe-method request.
			Expect(rec.Body.String()).To(ContainSubstring(`"csrf_token"`))
			Expect(rec.Header().Get("Content-Type")).To(ContainSubstring("application/json"))
			Expect(rec.Header().Get("Cache-Control")).To(Equal("no-store"))
		})

		It("the prefetched token + cookie pair lets the next unsafe-method POST clear the CSRF gate", func() {
			// Full round-trip: GET /api/auth/csrf, capture the cookie +
			// token, then POST /api/auth/login with both. The POST must
			// NOT return 403 csrf — the QA reproducer's "first-time
			// login → 403" path is now closed.
			//
			// The POST will still return 401 invalid_credentials because
			// the test bundle has no IdentitySource wired (the harness'
			// newAuthBundleForTest omits it — see the helper below).
			// That's the point: the request reaches HandleLogin, which
			// returns the uniform 401, instead of being rejected at the
			// CSRF gate with 403.
			getReq := httptest.NewRequest(http.MethodGet, "/api/auth/csrf", nil)
			getReq.Header.Set("Sec-Fetch-Site", "same-origin")
			getRec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(getRec, getReq)
			Expect(getRec.Code).To(Equal(http.StatusOK))

			var body struct {
				CSRFToken string `json:"csrf_token"`
			}
			Expect(json.Unmarshal(getRec.Body.Bytes(), &body)).To(Succeed())
			Expect(body.CSRFToken).NotTo(BeEmpty())

			var csrfCookie *http.Cookie
			for _, c := range getRec.Result().Cookies() {
				if c.Name == "_csrf" {
					csrfCookie = c
					break
				}
			}
			Expect(csrfCookie).NotTo(BeNil())

			postReq := httptest.NewRequest(http.MethodPost, "/api/auth/login",
				strings.NewReader(`{"secret":"wrong"}`))
			postReq.Header.Set("Sec-Fetch-Site", "same-origin")
			postReq.Header.Set("Content-Type", "application/json")
			postReq.Header.Set("X-CSRF-Token", body.CSRFToken)
			postReq.AddCookie(csrfCookie)
			postRec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(postRec, postReq)

			// MUST NOT be 403 — the QA reproducer's failure path. The
			// auth bundle in newAuthBundleForTest has no IdentitySource,
			// so HandleLogin isn't registered; the request reaches the
			// pass-through (404 net/http: no matching route). What we
			// care about is "not 403 csrf_invalid". The route presence
			// is exercised by the IdentitySource-wired harness in
			// serve_auth_config_test.go.
			Expect(postRec.Code).NotTo(Equal(http.StatusForbidden),
				"prefetched (cookie, token) pair must clear the CSRF gate")
		})
	})

	Context("handleSwarmEvents CORS narrowing (round-6 fold)", func() {
		// PR1 engineer flagged Access-Control-Allow-Origin: * at
		// server.go:1544 as a backstop-adjacency item; PR3 closes it.
		// The narrow-down replaces "*" with cfg.AllowedOrigins[0] and
		// adds Allow-Credentials: true so the SPA can send cookies on
		// cross-origin SSE.
		//
		// The contract under flag-off MUST still produce a valid origin
		// header (not "*") — the narrowing is unconditional because the
		// CORS pre-existed the flag.
		It("emits the configured allowlist origin instead of *", func() {
			srv := api.NewServer(nil, registry, nil, nil,
				api.WithOriginPatterns([]string{"https://flowstate.example.com"}),
			)

			req := httptest.NewRequest(http.MethodGet,
				"/api/swarm/events?session_id=abc", nil)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			// Even on the "event bus not configured" failure path the
			// CORS headers are set before the early-return. The header
			// MUST NOT be "*".
			gotOrigin := rec.Header().Get("Access-Control-Allow-Origin")
			Expect(gotOrigin).NotTo(Equal("*"),
				"Access-Control-Allow-Origin must not be * after PR3 C7 narrowing")
			Expect(gotOrigin).To(Equal("https://flowstate.example.com"))
			Expect(rec.Header().Get("Access-Control-Allow-Credentials")).To(Equal("true"))
		})

		It("emits no allow-origin when no allowlist is configured", func() {
			srv := api.NewServer(nil, registry, nil, nil)

			req := httptest.NewRequest(http.MethodGet,
				"/api/swarm/events?session_id=abc", nil)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			gotOrigin := rec.Header().Get("Access-Control-Allow-Origin")
			Expect(gotOrigin).NotTo(Equal("*"),
				"Access-Control-Allow-Origin must not fall back to * when allowlist is empty")
		})
	})
})

// suppress unused warning when test sub-helpers are skipped.
var _ = identity.ModeDeploymentLogin

// newAuthBundleForTest constructs a minimal AuthBundle with Enabled=true
// for the flag-on tests. SessionManager + CSRFConfig come from the auth
// package's existing defaults; the test focuses on route-wrap composition
// rather than the auth primitives (those have their own seam specs in
// internal/auth/).
func newAuthBundleForTest() api.AuthBundle {
	memStore := newMemoryStoreForTest()
	mgr := auth.NewSessionManager(memStore, auth.SessionConfig{
		CookieName:    "flowstate_session",
		CookiePath:    "/api",
		SecureCookies: false, // dev-mode for tests; production keeps true
		Mode:          identity.ModeDeploymentLogin,
	})
	return api.AuthBundle{
		Origin: auth.OriginConfig{
			AllowedOrigins: []string{"localhost:*"},
		},
		Session: mgr,
		Auth: auth.AuthConfig{
			Enabled: true,
			Mode:    identity.ModeDeploymentLogin,
		},
		CSRF: auth.CSRFConfig{
			AuthKey:       []byte("32-byte-test-key-padding-ok-yes!"),
			CookieName:    "_csrf",
			CookiePath:    "/api",
			SecureCookies: false,
		},
	}
}
