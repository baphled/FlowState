// Package auth provides authentication, session management, and origin
// validation for the FlowState HTTP/WebSocket surface.
package auth

import (
	"log/slog"
	"net/http"
	"path"
	"strings"
)

// OriginConfig holds the cluster-wide Origin / Sec-Fetch-Site validation
// configuration. v1 sources this from cfg.Auth.AllowedOrigins (TOML) — see
// the rollout plan §"Rollout Plan" PR1/C1.
//
// AllowedOrigins is a list of glob-style patterns matched against the request
// Origin header. Each pattern follows path.Match semantics, so "localhost:*"
// matches "localhost:8080" but not "evil.com". Empty AllowedOrigins is valid
// and means "no cross-origin requests permitted" — only same-origin requests
// (no Origin header, or Sec-Fetch-Site: same-origin) pass.
type OriginConfig struct {
	// AllowedOrigins is the glob allowlist used by both the HTTP middleware
	// (RequireOrigin) and the WebSocket handler (via OriginPatterns on
	// websocket.AcceptOptions). Centralising the list here is the load-bearing
	// PR1 lift: PR3 wraps every HTTP route with RequireOrigin reading the
	// same allowlist the WS handler already enforces.
	AllowedOrigins []string
}

// IsSafeMethod returns true for HTTP methods that are read-only and idempotent
// per RFC 7231 §4.2.1. Browsers do not require Origin validation on safe
// methods because they cannot be used to mutate state.
//
// Exported so the WebSocket handler and HTTP middleware share the same
// definition.
func IsSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

// MatchOrigin returns true when origin matches any pattern in allowed using
// path.Match glob semantics. An empty origin never matches (callers handle
// the missing-Origin case via Sec-Fetch-Site).
//
// path.Match returns an error only on malformed patterns; we treat that as
// a non-match (defensive: a malformed config should reject, not panic).
func MatchOrigin(origin string, allowed []string) bool {
	if origin == "" {
		return false
	}
	// Strip scheme so patterns like "localhost:*" match "http://localhost:8080".
	host := origin
	if idx := strings.Index(host, "://"); idx >= 0 {
		host = host[idx+3:]
	}
	for _, pattern := range allowed {
		ok, err := path.Match(pattern, host)
		if err == nil && ok {
			return true
		}
	}
	return false
}

// RequireOrigin returns an http.Handler that validates the request Origin
// and Sec-Fetch-Site headers against cfg.AllowedOrigins before delegating
// to next.
//
// Validation rules (mirrors the WebSocket handler's OriginPatterns
// semantics, lifted from internal/api/websocket.go:106 (handleSessionWebSocket)
// per the API Auth Track plan §"Rollout Plan" C1):
//
//   - Safe methods (GET/HEAD/OPTIONS) pass through unchecked.
//   - Origin header present AND matches AllowedOrigins → pass.
//   - Origin header absent AND Sec-Fetch-Site is "same-origin" or "none" → pass.
//     (Browsers omit Origin on same-origin requests; "none" means a direct
//     user-initiated navigation, e.g. typing in the address bar.)
//   - Otherwise → 403 origin_rejected with slog.Warn.
//
// PR3 wraps every protected HTTP route with this middleware (see plan
// §"Endpoint Inventory"). The WebSocket handler continues to enforce
// origin via the same AllowedOrigins list passed through to
// websocket.AcceptOptions.OriginPatterns; that path is exercised by the
// existing websocket_test.go suite.
func RequireOrigin(cfg OriginConfig, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if IsSafeMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		site := r.Header.Get("Sec-Fetch-Site")

		if origin == "" {
			// No Origin: rely on Sec-Fetch-Site. Browsers set this on
			// every fetch since Chrome 76 / Firefox 90. "same-origin" and
			// "none" (direct navigation) are safe; "cross-site" or
			// "same-site" are not. Absent Sec-Fetch-Site is treated as
			// safe to preserve compatibility with non-browser clients
			// (curl, server-to-server) that omit both headers.
			if site == "" || site == "same-origin" || site == "none" {
				next.ServeHTTP(w, r)
				return
			}
			slog.Warn("origin_rejected",
				"reason", "no_origin_unsafe_fetch_site",
				"sec_fetch_site", site,
				"method", r.Method,
				"path", r.URL.Path,
			)
			http.Error(w, "origin_rejected", http.StatusForbidden)
			return
		}

		if MatchOrigin(origin, cfg.AllowedOrigins) {
			next.ServeHTTP(w, r)
			return
		}
		slog.Warn("origin_rejected",
			"reason", "origin_not_allowlisted",
			"origin", origin,
			"method", r.Method,
			"path", r.URL.Path,
		)
		http.Error(w, "origin_rejected", http.StatusForbidden)
	})
}
