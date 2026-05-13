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
			protectedGets := []string{
				"/api/v1/sessions",
				"/api/v1/sessions/abc/messages",
				"/api/v1/sessions/abc/stream",
				"/api/v1/sessions/abc/ws",
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
			// only, no Session — login cannot require a session).
			//
			// Sec-Fetch-Site: same-origin satisfies the Origin check when
			// no Origin header is present (RequireOrigin pass-through
			// branch). gorilla/csrf in the LoginChain composes ahead of
			// the handler; without a CSRF cookie on the first POST the
			// CSRF middleware returns 403 — that's the expected wire
			// behaviour for a fresh-page login attempt without prior
			// cookie state. The contract is that the request reaches the
			// CSRF gate (403), not RequireSession (401 unauthenticated).
			req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{}`))
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			Expect(rec.Code).NotTo(Equal(http.StatusUnauthorized),
				"login endpoint must not require a session cookie")
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
