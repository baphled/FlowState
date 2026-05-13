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
	"encoding/json"
	"net/http"

	"github.com/baphled/flowstate/internal/auth"
	"github.com/baphled/flowstate/internal/auth/identity"
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

	// IdentitySource is the per-mode credential validator wired by the
	// cmd/serve layer (PR4/C9 — deferred from PR3 ship-state). When set
	// AND Auth.Enabled is true, setupRoutes registers POST /api/auth/login
	// and POST /api/auth/logout via registerLogin. When nil the auth
	// endpoints are not registered, so the route map stays consistent with
	// the pre-PR4 behaviour for callers that wire AuthBundle ahead of the
	// flag flip without also constructing a Source.
	IdentitySource identity.Source
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
// POST /api/auth/login + POST /api/auth/logout — endpoints that cannot
// require a session because they predate / postdate it.
//
// When AuthBundle is inactive, falls back to plain registration so the
// flag-off behaviour matches the rest of the server.
//
// Note: GET /api/auth/whoami is deliberately NOT registered here despite
// the plan §"Endpoint Inventory" line 398 mention. PR5/C10 B8 fold:
// whoami goes through registerProtected so the 401 wire shape is
// byte-identical to every other protected endpoint and an unauthenticated
// caller cannot fingerprint the active auth.mode by probing /whoami
// (which the plan's line 511 "unauth returns {mode, authenticated:false}"
// shape would have leaked).
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

// WhoamiView is the JSON projection returned by GET /api/auth/whoami on
// success. Mirrors auth.LoginPrincipalView (login response) so the SPA
// can consume both shapes uniformly. Plan §"Wire Protocol" lines 514-522
// (authenticated branch).
type WhoamiView struct {
	PrincipalID string `json:"principal_id"`
	DisplayName string `json:"display_name,omitempty"`
	Mode        string `json:"mode"`
}

// handleWhoami returns the GET /api/auth/whoami handler.
//
// PR5/C10 B8 fold (task brief): the handler is wired via
// registerProtected so the 401 path is owned by the auth middleware
// chain (uniform "unauthenticated" body — same shape as any other
// protected endpoint). On success it returns the principal's id +
// display name + mode read straight off the session Record stamped by
// RequireSession.
//
// The handler runs ONLY when:
//
//   - The auth feature is active (s.auth.active()).
//   - Origin check passed (RequireOrigin).
//   - Session cookie validated against the store (RequireSession).
//   - Record.Mode matches cfg.Mode (the round-5 B3 mode-mismatch check
//     short-circuits with 401 before we get here).
//
// Why GET-only: whoami is a read of session state, not a mutation. The
// composition chain runs RequireOrigin + RequireSession; CSRF runs only
// on unsafe methods (the gorilla/csrf wrapper's behaviour), so GET
// requests do not need the CSRF token. The Record-bound CSRF layer
// (PR2/C5) is wrapped around handlers via Protected, but is a no-op for
// safe methods.
//
// Returns 405 method_not_allowed if a non-GET method reaches the handler
// (defensive — net/http's pattern matching already filters by method
// prefix in the route registration, so this branch is hit only when a
// future route restructure drops the method prefix).
func handleWhoami(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method_not_allowed", http.StatusMethodNotAllowed)
		return
	}

	rec := auth.RecordFrom(r)
	if rec == nil {
		// Defensive — RequireSession's 401 short-circuit should mean we
		// never see a nil Record here. If we do, fall back to the same
		// wire shape (no body hint, uniform with the middleware's 401).
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}

	view := WhoamiView{
		PrincipalID: rec.PrincipalID,
		Mode:        rec.Mode,
	}
	if rec.Data != nil {
		view.DisplayName = rec.Data["display_name"]
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(view)
}
