package auth

import (
	"log/slog"
	"net/http"

	"github.com/gorilla/csrf"
)

// CSRFConfig holds the CSRF middleware's static configuration. v1 sources
// this from cfg.Auth in the config layer (PR3 wires it through); Defaults
// match the plan §"Wire Protocol" §"CSRF token" lines 422-431.
type CSRFConfig struct {
	// AuthKey is the 32-byte HMAC signing key gorilla/csrf uses to sign
	// the _csrf cookie. MUST be 32 bytes. v1 derives this from the
	// configured shared secret or generates it on first boot (PR3
	// wiring). Empty AuthKey disables CSRF — Wrap panics defensively to
	// catch misconfig.
	AuthKey []byte

	// CookieName is the _csrf cookie's Name attribute. Plan default "_csrf".
	CookieName string

	// CookiePath is the cookie's Path attribute. Plan default "/api".
	CookiePath string

	// SecureCookies controls the _csrf cookie's Secure attribute. Inherits
	// from cfg.Auth.SecureCookies (matches the session cookie).
	SecureCookies bool

	// TrustedOrigins is the list of cross-origin sources gorilla/csrf will
	// accept a request from. Distinct from Origin allowlist (RequireOrigin
	// handles the broader check); gorilla/csrf's own Origin check is
	// stricter (exact-match Hosts). Empty means "same-origin only".
	TrustedOrigins []string
}

// DefaultCSRFConfig returns the production defaults. Callers MUST stamp
// AuthKey (32 bytes) — there is no safe default.
func DefaultCSRFConfig() CSRFConfig {
	return CSRFConfig{
		CookieName:    "_csrf",
		CookiePath:    "/api",
		SecureCookies: true,
	}
}

// Protect returns the gorilla/csrf middleware factory configured per cfg.
// This is the FIRST layer of the CSRF composition — it validates the
// signed _csrf cookie ↔ X-CSRF-Token header pair. The RECORD-BOUND layer
// (RequireCSRFRecordBound below) wraps the inner handler and checks
// X-CSRF-Token against Record.CSRFToken.
//
// Composition (plan §"CSRF" lines 287-296):
//
//	handler = RequireOrigin(originCfg,                                // PR1
//	          RequireSession(sessionMgr,                              // C6
//	          Protect(csrfCfg)(                                       // C5 layer 1
//	          RequireCSRFRecordBound(sessionMgr)(                     // C5 layer 2
//	          innerHandler))))
//
// Order matters: Origin → Session → gorilla/csrf → Record-bound CSRF →
// inner. gorilla/csrf BEFORE Record-bound because gorilla rejects forged
// cookies first (cheap signed-cookie check); the Record-bound layer
// closes the token-substitution-within-active-session vector (plan
// §"CSRF" line 295) ONLY for requests gorilla already accepted.
//
// Panics on empty AuthKey — misconfig should fail at boot, not silently
// disable CSRF.
func Protect(cfg CSRFConfig) func(http.Handler) http.Handler {
	if len(cfg.AuthKey) == 0 {
		panic("auth: CSRFConfig.AuthKey is empty — CSRF would be disabled")
	}
	opts := []csrf.Option{
		csrf.CookieName(cfg.CookieName),
		csrf.Path(cfg.CookiePath),
		csrf.HttpOnly(false), // Vue must read it for the X-CSRF-Token header
		csrf.Secure(cfg.SecureCookies),
		csrf.SameSite(csrf.SameSiteLaxMode),
		csrf.RequestHeader("X-CSRF-Token"),
	}
	if len(cfg.TrustedOrigins) > 0 {
		opts = append(opts, csrf.TrustedOrigins(cfg.TrustedOrigins))
	}
	return csrf.Protect(cfg.AuthKey, opts...)
}

// RequireCSRFRecordBound is the SECOND CSRF layer — runs after gorilla/csrf
// has validated the signed cookie ↔ header pair. Compares the
// X-CSRF-Token header against the current session's Record.CSRFToken.
//
// Pre-condition: RequireSession has run, so requestRecord(r) returns a
// non-nil Record. Unsafe methods only — safe methods pass through (plan
// §"CSRF" line 294).
//
// Why this layer exists (plan §"CSRF" line 295):
//
//	"defence-in-depth against token-substitution-within-active-session
//	 — an attacker who somehow obtained a valid (_csrf cookie, header)
//	 pair that doesn't match the current session's tokens."
//
// On mismatch: 403 csrf_invalid + slog.Warn. The structured log fields
// (session principal id, request path) help the operator triage forged-
// token attempts vs legitimate browser quirks.
func RequireCSRFRecordBound(sessionMgr *SessionManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if IsSafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			rec := requestRecord(r)
			if rec == nil {
				// Pre-condition failed — RequireSession should have
				// gated this. Fail closed.
				slog.Warn("csrf_invalid",
					"reason", "no_session_record",
					"method", r.Method,
					"path", r.URL.Path,
				)
				http.Error(w, "csrf_invalid", http.StatusForbidden)
				return
			}

			supplied := r.Header.Get("X-CSRF-Token")
			if supplied == "" {
				// gorilla/csrf would normally catch this with ErrNoToken,
				// but we double-pin here because callers may compose
				// RequireCSRFRecordBound without gorilla (e.g. an
				// integration test).
				slog.Warn("csrf_invalid",
					"reason", "missing_header",
					"principal", rec.PrincipalID,
					"method", r.Method,
					"path", r.URL.Path,
				)
				http.Error(w, "csrf_invalid", http.StatusForbidden)
				return
			}

			if !constantTimeStringEqual(supplied, rec.CSRFToken) {
				slog.Warn("csrf_invalid",
					"reason", "record_bound_mismatch",
					"principal", rec.PrincipalID,
					"method", r.Method,
					"path", r.URL.Path,
				)
				http.Error(w, "csrf_invalid", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// constantTimeStringEqual mirrors the identity package's helper. Lifted
// locally here so internal/auth doesn't depend on the identity package
// for what is, fundamentally, a string-compare primitive.
//
// The length-mismatch fast-path leaks token length; both inputs are
// 256-bit base64 tokens (43 chars), so length-mismatch is anomalous
// (forged-or-malformed) and the leak is informational only.
func constantTimeStringEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	// crypto/subtle indirect — see identity.constantTimeEqual. Inlining
	// the byte compare here so the auth package does not import
	// crypto/subtle in two places.
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
