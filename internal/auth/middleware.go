package auth

import (
	"errors"
	"log/slog"
	"net/http"
)

// AuthConfig holds the auth track's feature flag + composition knobs.
// PR3 wires this into the global config layer (`features.auth_v1`);
// PR2 shipped the surface so tests can construct it directly;
// PR5/C10 flipped the default-on (see config.DefaultAuthConfig).
//
// Distinct from config.AuthConfig (the YAML-deserialised shape): the
// boot wiring in internal/cli/serve.go projects the relevant
// config-layer fields (Enabled, Mode) into this runtime feature-flag
// struct. Direct construction is preserved for tests that drive the
// middleware composition without the full boot wiring.
type AuthConfig struct {
	// Enabled is the features.auth_v1 flag. PR5/C10 ships default-on
	// via config.DefaultAuthConfig (deployments inherit Enabled=true
	// unless they explicitly set `auth.enabled: false`). When false,
	// RequireSession passes through unconditionally (the route stays
	// public) — same compositional contract as the pre-PR5 default-off.
	Enabled bool

	// Mode is the active deployment mode. Stamped onto the Record at mint;
	// checked at RequireSession (round-5 B3 fold — mode-mismatch returns
	// 401 + slog.Warn). One of identity.Mode* constants.
	Mode string
}

// RequireSession is the load-bearing PR2 milestone. Returns the middleware
// that reads the session cookie, looks up the Record, validates expiry +
// mode, attaches the Record to the request context for downstream handlers,
// and rejects with 401 on any failure.
//
// Composition (plan §"What this plan delivers" line 124 + §"CSRF" lines
// 287-296 — fixed order):
//
//	handler = RequireOrigin(originCfg,                              // PR1
//	          RequireSession(sessionMgr, cfg)(                      // C6 (this)
//	          Protect(csrfCfg)(                                     // C5
//	          RequireCSRFRecordBound(sessionMgr)(                   // C5
//	          innerHandler))))
//
// Order discipline:
//   - Origin runs FIRST so cross-origin probes get 403 origin_rejected
//     before any session lookup (no timing leak on cookie presence).
//   - Session runs SECOND so RequireCSRFRecordBound's Record-bound check
//     has a stamped Record on ctx. The Record-bound check is the second
//     CSRF layer; gorilla/csrf is the first.
//   - CSRF runs LAST among the middleware layers (gorilla then
//     Record-bound) — by the time CSRF fires, the session is known
//     authentic, so a CSRF failure on a valid session points to a
//     forged-token-within-active-session (defence-in-depth).
//
// When cfg.Enabled is false, the middleware passes through
// unconditionally. PR5/C10 flipped the default-on (deployments inherit
// Enabled=true via config.DefaultAuthConfig); operators opt out by
// setting `auth.enabled: false` in config.yaml or
// FLOWSTATE_AUTH_ENABLED=false at boot.
//
// Error handling:
//   - ErrSessionInvalid       → 401 unauthenticated (no log — high volume).
//   - ErrSessionExpired       → 401 unauthenticated + slog.Debug.
//   - ErrSessionModeMismatch  → 401 unauthenticated + slog.Warn (B3 fold:
//     operator-visible signal that auth.mode changed between deployments).
//   - Other errors            → 401 unauthenticated + slog.Error.
//
// All failure paths return body "unauthenticated" so the on-wire response
// shape is uniform — no information leakage about why the session was
// rejected (matches B8's mode-fingerprint discipline applied to the
// session check).
func RequireSession(sessionMgr *SessionManager, cfg AuthConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !cfg.Enabled {
				next.ServeHTTP(w, r)
				return
			}

			rec, err := sessionMgr.Authenticate(r)
			if err != nil {
				switch {
				case errors.Is(err, ErrSessionInvalid):
					// High-volume — no log.
				case errors.Is(err, ErrSessionExpired):
					slog.Debug("auth: session expired",
						"path", r.URL.Path,
					)
				case errors.Is(err, ErrSessionModeMismatch):
					slog.Warn("auth mode changed; session invalidated",
						"path", r.URL.Path,
						"expected_mode", cfg.Mode,
					)
				default:
					slog.Error("auth: session check failed",
						"path", r.URL.Path,
						"err", err.Error(),
					)
				}
				http.Error(w, "unauthenticated", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r.WithContext(withRecord(r.Context(), rec)))
		})
	}
}

// Protected returns the fully-composed middleware chain for a protected
// route per plan §"What this plan delivers" line 124. Convenience helper
// for callers (PR3 setupRoutes wiring) that want the standard chain
// without re-writing the composition each time.
//
// Order is FIXED: RequireOrigin → RequireSession → Protect (gorilla/csrf)
// → RequireCSRFRecordBound → inner. The fixed order matches the plan's
// §"CSRF" line 296 composition contract; passing custom middleware
// alongside Protected is supported (wrap the result), but the four
// security layers MUST run in this order.
//
// Callers that need a different shape (e.g. login.go's "Origin + CSRF
// only, no Session" path) construct the composition manually — see
// LoginChain below.
func Protected(
	originCfg OriginConfig,
	sessionMgr *SessionManager,
	authCfg AuthConfig,
	csrfCfg CSRFConfig,
	next http.Handler,
) http.Handler {
	return RequireOrigin(originCfg,
		RequireSession(sessionMgr, authCfg)(
			Protect(csrfCfg)(
				RequireCSRFRecordBound(sessionMgr)(next),
			),
		),
	)
}

// LoginChain returns the middleware chain for POST /api/auth/login per
// plan line 445 + §"Endpoint Inventory" line 398. The login endpoint
// requires Origin allowlist + gorilla/csrf (for CSRF token rotation on
// login) but NOT RequireSession or the Record-bound CSRF check (no
// session exists yet).
//
// SameSite=Lax on the session cookie + Origin allowlist close the
// cross-origin attack vector on login.
func LoginChain(
	originCfg OriginConfig,
	csrfCfg CSRFConfig,
	next http.Handler,
) http.Handler {
	return RequireOrigin(originCfg,
		Protect(csrfCfg)(next),
	)
}
