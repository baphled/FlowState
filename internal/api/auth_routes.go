package api

// PR3 C7 — route-wrapping helpers (registerProtected / registerPublic /
// registerLogin) + AuthBundle option for wiring the auth track into the
// HTTP server.
//
// Plan reference: FlowState API Auth Track (May 2026) §"Rollout Plan"
// PR3/C7 + §"Endpoint Inventory" + line 124 (composition order:
// RequireOrigin → RequireSession → RequireCSRF, last only on unsafe).
//
// Design notes:
//
//   - The helpers route registration through `auth.Protected` /
//     `auth.LoginChain` from the auth package. The composition order is
//     fixed there — this file does not re-implement it.
//   - When AuthBundle is unset OR AuthBundle.Auth.Enabled is false (the
//     PR2 / PR3 default), helpers fall back to a plain
//     `s.mux.HandleFunc` so existing call sites keep working
//     untouched. PR5 will flip the flag default.
//   - Authentication wiring lives in the api package, NOT the engine —
//     per project_flowstate_engine_boundary, the engine stays
//     consumer-agnostic.

import (
	"net/http"

	"github.com/baphled/flowstate/internal/auth"
)

// AuthBundle is the optional auth wiring passed via WithAuth. When set,
// the server's register* helpers compose the full middleware chain on
// protected routes and the Origin+CSRF chain on login routes. When
// Auth.Enabled is false (the PR2/PR3 default), the helpers no-op even
// when a bundle is supplied — so callers can wire the bundle ahead of
// the flag flip without changing behaviour.
//
// Fields are the minimal cross-section the helpers need to compose the
// chains; further auth surface (e.g. identity.Source for login route
// wiring) lives outside this struct so the seam stays narrow.
type AuthBundle struct {
	Origin  auth.OriginConfig
	Session *auth.SessionManager
	Auth    auth.AuthConfig
	CSRF    auth.CSRFConfig
}

// active reports whether the bundle is wired AND the feature flag is on.
// Helpers use this to choose between the wrapped path and the
// pass-through path; centralising the predicate avoids a
// per-call-site nil check drift.
func (b AuthBundle) active() bool {
	return b.Session != nil && b.Auth.Enabled
}

// WithAuth installs the AuthBundle so setupRoutes wraps protected /
// login routes through the auth chain. When omitted, the server keeps
// the pre-PR3 behaviour (all routes pass through unwrapped).
//
// Production wires this from cfg.Auth (TOML) after constructing the
// SessionManager, OriginConfig, AuthConfig, and CSRFConfig from the
// same config layer.
func WithAuth(bundle AuthBundle) ServerOption {
	return func(s *Server) { s.auth = bundle }
}

// registerProtected wraps the handler with the full auth chain when
// AuthBundle.Auth.Enabled is true; otherwise registers the handler
// directly on the mux (the pre-PR3 behaviour). The chain order is
// RequireOrigin → RequireSession → CSRF (gorilla then Record-bound) per
// plan line 124.
//
// Used by setupRoutes for every endpoint in §"Endpoint Inventory"
// Protected list (16 routes).
func (s *Server) registerProtected(pattern string, h http.HandlerFunc) {
	if !s.auth.active() {
		s.mux.HandleFunc(pattern, h)
		return
	}
	wrapped := auth.Protected(
		s.auth.Origin,
		s.auth.Session,
		s.auth.Auth,
		s.auth.CSRF,
		http.HandlerFunc(h),
	)
	s.mux.Handle(pattern, wrapped)
}

// registerPublic registers the handler directly on the mux — no auth
// wrap. Used for read-only catalog / probe endpoints
// (§"Endpoint Inventory" Public list, 10 routes).
//
// The helper exists as an explicit marker at the call site so a reader
// can audit "is this endpoint public?" at registration time rather than
// inferring from absence-of-wrap.
func (s *Server) registerPublic(pattern string, h http.HandlerFunc) {
	s.mux.HandleFunc(pattern, h)
}

// registerLogin wraps the handler with the login-chain (Origin + CSRF,
// no Session) per plan §"Endpoint Inventory" line 398. Used for
// POST /api/auth/login + GET /api/auth/whoami — endpoints that cannot
// require a session because they predate it.
//
// When AuthBundle is inactive, falls back to plain registration so the
// flag-off behaviour matches the rest of the server.
func (s *Server) registerLogin(pattern string, h http.HandlerFunc) {
	if !s.auth.active() {
		s.mux.HandleFunc(pattern, h)
		return
	}
	wrapped := auth.LoginChain(
		s.auth.Origin,
		s.auth.CSRF,
		http.HandlerFunc(h),
	)
	s.mux.Handle(pattern, wrapped)
}
