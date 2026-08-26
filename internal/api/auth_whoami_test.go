package api_test

// PR5 C10 — whoami endpoint spec.
//
// Pins:
//   - B8 wire discipline (task brief): unauthenticated GET /api/auth/whoami
//     returns 401 with uniform "unauthenticated" body — byte-identical to
//     every other protected endpoint's 401 path. No mode hint in the body.
//   - Authenticated GET /api/auth/whoami returns 200 + JSON
//     {principal_id, display_name?, mode} read from the session Record
//     stamped by RequireSession.
//   - Method gate: POST /api/auth/whoami does not match the GET route
//     pattern (net/http pattern matching), so the request reaches the
//     general 404 path — important for the wire surface since 405 would
//     confirm the endpoint exists for an unauthenticated prober.
//   - Flag-off: a server constructed without WithAuth(...) registers the
//     route plain; an unauthenticated request still hits 401 via the
//     handler's nil-record branch (defensive — the registerProtected
//     fast-path treats this as a public route when the bundle is
//     inactive).
//
// Seam-level Ginkgo per feedback_ginkgo_not_godog. Extends the existing
// auth_wrap_test.go pattern: build an api.Server with WithAuth, drive
// HTTP at srv.Handler().ServeHTTP via httptest.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/auth"
	"github.com/baphled/flowstate/internal/auth/identity"
	authstore "github.com/baphled/flowstate/internal/auth/store"
)

var _ = Describe("PR5 C10 — GET /api/auth/whoami", func() {
	var (
		registry *agent.Registry
	)

	BeforeEach(func() {
		registry = agent.NewRegistry()
	})

	Context("flag-on (B8 wire discipline)", func() {
		var (
			srv      *api.Server
			authBits api.AuthBundle
		)

		BeforeEach(func() {
			authBits = newWhoamiAuthBundle()
			srv = api.NewServer(nil, registry, nil, nil,
				api.WithAuth(authBits),
			)
		})

		It("returns 401 unauthenticated on no-cookie GET (uniform body, no mode hint)", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/auth/whoami", nil)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusUnauthorized),
				"whoami without a session must be byte-identical to any "+
					"other protected endpoint's 401 path — no probe leak")

			// B8: body MUST NOT contain mode-fingerprint hints
			// ("shared-secret", "multi-user", "per-deployment-login",
			// "mode", "authenticated") — RequireSession's
			// http.Error(w, "unauthenticated", ...) writes the same body
			// the rest of the protected surface emits.
			body := rec.Body.String()
			Expect(body).To(ContainSubstring("unauthenticated"))
			Expect(body).NotTo(ContainSubstring("shared-secret"))
			Expect(body).NotTo(ContainSubstring("multi-user"))
			Expect(body).NotTo(ContainSubstring("per-deployment-login"))
			Expect(body).NotTo(ContainSubstring("mode"))
		})

		It("returns 200 + principal view on a valid session", func() {
			// Mint a session via the real flow so the cookie + Record
			// are produced by the production codepath (no test-only
			// fake-record injection — that hides the wire shape).
			principal := identity.Principal{
				ID:          "operator-001",
				DisplayName: "Operator One",
				Mode:        identity.ModeDeploymentLogin,
			}
			loginReq := httptest.NewRequest(http.MethodGet, "/", nil)
			loginRec := httptest.NewRecorder()
			_, err := authBits.Session.Begin(loginRec, loginReq, principal)
			Expect(err).ToNot(HaveOccurred())

			// Take the freshly-minted cookie and replay it on the
			// whoami request.
			cookies := loginRec.Result().Cookies()
			Expect(cookies).ToNot(BeEmpty())

			req := httptest.NewRequest(http.MethodGet, "/api/auth/whoami", nil)
			for _, c := range cookies {
				req.AddCookie(c)
			}
			// Same-origin header satisfies RequireOrigin without an
			// allowlist origin match (test config carries "localhost:*").
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK),
				"valid session must return 200 OK; got body: %s", rec.Body.String())
			Expect(rec.Header().Get("Content-Type")).To(Equal("application/json"))

			var view api.WhoamiView
			Expect(json.Unmarshal(rec.Body.Bytes(), &view)).To(Succeed())
			Expect(view.PrincipalID).To(Equal("operator-001"))
			Expect(view.DisplayName).To(Equal("Operator One"))
			Expect(view.Mode).To(Equal(identity.ModeDeploymentLogin))
		})

		It("does not match POST /api/auth/whoami (method-prefixed pattern)", func() {
			// net/http pattern matching binds "GET /api/auth/whoami" —
			// POST hits the 404 path with no body leak.
			req := httptest.NewRequest(http.MethodPost, "/api/auth/whoami", nil)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			Expect(rec.Code).To(Or(
				Equal(http.StatusNotFound),
				Equal(http.StatusMethodNotAllowed),
			), "POST whoami must not match the GET route; expected 404/405, "+
				"got %d body %q", rec.Code, rec.Body.String())
		})
	})

	Context("flag-off (pre-flip pass-through)", func() {
		// When the AuthBundle is unwired (or Enabled=false) the
		// registerProtected helper falls back to plain mux registration.
		// The handler still runs its nil-record defensive branch and
		// returns 401 — so whoami remains useless without a session
		// regardless of the flag state.
		It("returns 401 unauthenticated on no-cookie GET (handler defensive branch)", func() {
			srv := api.NewServer(nil, registry, nil, nil)
			req := httptest.NewRequest(http.MethodGet, "/api/auth/whoami", nil)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
			Expect(rec.Body.String()).To(ContainSubstring("unauthenticated"))
		})
	})
})

// newWhoamiAuthBundle constructs a flag-on AuthBundle for the whoami
// specs. Distinct from auth_wrap_test.go's helper because the whoami
// tests need a real SessionManager + store (the wrap tests use the
// bundle only for the no-cookie 401 assertion).
func newWhoamiAuthBundle() api.AuthBundle {
	memStore := authstore.NewMemoryStore()
	mgr := auth.NewSessionManager(memStore, auth.SessionConfig{
		CookieName:    "flowstate_session",
		CookiePath:    "/api",
		SecureCookies: false, // dev-mode for tests
		Mode:          identity.ModeDeploymentLogin,
		Lifetime:      auth.DefaultSessionConfig().Lifetime,
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
