package auth

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/csrf"

	"github.com/baphled/flowstate/internal/auth/identity"
	"github.com/baphled/flowstate/internal/auth/store"
)

// LoginResponse is the JSON payload returned by HandleLogin on success.
// Plan §"Wire Protocol" lines 486-499.
type LoginResponse struct {
	CSRFToken string             `json:"csrf_token"`
	ExpiresAt time.Time          `json:"expires_at"`
	Principal LoginPrincipalView `json:"principal"`
}

// LoginPrincipalView is the public projection of identity.Principal in
// the login response body. Mirrors the Principal fields the SPA needs to
// render the post-login state.
type LoginPrincipalView struct {
	ID          string `json:"id"`
	DisplayName string `json:"display,omitempty"`
	Mode        string `json:"mode"`
}

// HandleLogin returns the POST /api/auth/login handler. Routes through
// the configured identity.Source; on success, mints a session via
// sessionMgr.Begin and writes the cookie + JSON body.
//
// B8 (plan §"Wire Protocol" line 484 — mode-fingerprint defence):
//
//	ANY login-shape failure returns uniform 401 {"error":"invalid_credentials"}.
//	This includes:
//	  - bad JSON (parse failure)
//	  - unknown fields (DisallowUnknownFields=false; extra fields silently dropped)
//	  - wrong shape (multi-user body to shared-secret server, etc.)
//	  - wrong credentials (Source returns ErrInvalidCredentials)
//	  - absent username in multi-user mode (PR4/C9 — same sentinel as wrong password)
//	  - ErrNotImplemented from any future Source stub (forward-compat)
//
//	The on-wire response is byte-identical across all failure paths so
//	probers cannot fingerprint the server's auth.mode by trying each body
//	shape. Detailed reason is logged via slog.Warn for the operator.
//
// Per the plan §"Login request / response" line 445: login POST does NOT
// require an inbound X-CSRF-Token. SameSite=Lax + Origin allowlist close
// the cross-origin attack vector at the perimeter (RequireOrigin runs
// before HandleLogin in the route wiring).
func HandleLogin(source identity.Source, sessionMgr *SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method_not_allowed", http.StatusMethodNotAllowed)
			return
		}

		creds, parseErr := parseCredentials(r, source.Mode())
		if parseErr != nil {
			// B8 — collapse to uniform 401.
			slog.Warn("auth login: shape mismatch",
				"mode", source.Mode(),
				"err", parseErr.Error(),
				"remote", r.RemoteAddr,
			)
			writeLoginError(w)
			return
		}

		principal, err := source.Authenticate(r.Context(), creds)
		if err != nil {
			// B8 — every Authenticate error (ErrInvalidCredentials,
			// any future ErrNotImplemented, ctx errors) collapses to
			// the same 401 shape.
			slog.Warn("auth login: authenticate failed",
				"mode", source.Mode(),
				"err", err.Error(),
				"remote", r.RemoteAddr,
			)
			writeLoginError(w)
			return
		}

		token, err := sessionMgr.Begin(w, r, principal)
		if err != nil {
			slog.Error("auth login: session mint failed",
				"mode", source.Mode(),
				"err", err.Error(),
			)
			http.Error(w, "session_mint_failed", http.StatusInternalServerError)
			return
		}

		// Read the freshly-minted Record back to surface CSRFToken +
		// ExpiresAt in the response body. The store is the source of
		// truth — never recompute these values from cookie state.
		rec, err := sessionMgr.lookupForLogin(r, token)
		if err != nil || rec == nil {
			slog.Error("auth login: post-mint lookup failed",
				"mode", source.Mode(),
				"err", errString(err),
			)
			http.Error(w, "session_mint_failed", http.StatusInternalServerError)
			return
		}

		resp := LoginResponse{
			CSRFToken: rec.CSRFToken,
			ExpiresAt: rec.ExpiresAt,
			Principal: LoginPrincipalView{
				ID:          principal.ID,
				DisplayName: principal.DisplayName,
				Mode:        principal.Mode,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// CSRFPrefetchResponse is the JSON payload returned by HandleCSRFPrefetch.
// The masked token is gorilla/csrf's session-bound XOR mask over the real
// token cached in the _csrf cookie — the SPA echoes it back via the
// X-CSRF-Token header on the next unsafe-method request.
type CSRFPrefetchResponse struct {
	CSRFToken string `json:"csrf_token"`
}

// HandleCSRFPrefetch returns the GET /api/auth/csrf handler.
//
// Why this endpoint exists (QA-reported showstopper, May 2026): on the
// very first SPA visit the browser has no _csrf cookie, gorilla/csrf
// can't mask a token the SPA never received, and the next POST /api/auth/
// login is rejected with 403 before credentials are even evaluated.
// Three different token values flowed through the system —
//
//   - the securecookie blob in the _csrf cookie (gorilla-private),
//   - the masked token surfaced by csrf.Token(r), and
//   - the unmasked Record-bound CSRFToken,
//
// — but the SPA could only read the cookie, so it sent the wrong value
// and gorilla rejected. The prefetch endpoint hands the SPA the masked
// token directly so the SPA's first POST has a valid (cookie, header)
// pair. The endpoint MUST be wrapped via auth.LoginChain (Origin +
// gorilla/csrf, no Session) so the wrap's ServeHTTP issues the _csrf
// cookie on the GET and csrf.Token(r) returns a non-empty masked token.
//
// Method gate: GET only. The token doesn't change state, and exposing
// POST here would invite confused-deputy variants.
//
// No session required — the gorilla mask is what protects, not a session
// (an attacker who can fetch this endpoint cannot read the response from
// a cross-origin context because Origin gates the request and SameSite=
// Lax + credentials:include scopes the cookie). Subsequent CSRF
// validation pairs the response token with the cookie the same browser
// holds.
//
// Wire shape (plan-aligned with LoginResponse.csrf_token):
//
//	{"csrf_token": "<masked-token>"}
//
// Single field by design — the unmasked Record-bound token only exists
// post-login (login response surfaces it). Prefetch is the pre-login
// path so there is nothing else to return.
func HandleCSRFPrefetch() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method_not_allowed", http.StatusMethodNotAllowed)
			return
		}
		// csrf.Token(r) reads the masked token gorilla/csrf stashed in
		// the request context during ServeHTTP. Returns "" only if
		// Protect did not run — fail closed in that defensive branch
		// rather than ship an empty token the SPA would echo back.
		token := csrf.Token(r)
		if token == "" {
			slog.Warn("csrf prefetch: empty masked token "+
				"(Protect middleware did not run on this route)",
				"path", r.URL.Path,
			)
			http.Error(w, "csrf_prefetch_misconfigured", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(CSRFPrefetchResponse{CSRFToken: token})
	}
}

// HandleLogout returns the POST /api/auth/logout handler. Calls
// sessionMgr.End to drop the Record + clear the cookie. Idempotent —
// returns 200 even when no session was attached (matches the SPA's
// best-effort logout semantics).
func HandleLogout(sessionMgr *SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method_not_allowed", http.StatusMethodNotAllowed)
			return
		}
		_ = sessionMgr.End(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}
}

// parseCredentials unmarshals the request body into Credentials according
// to the active mode's shape. Per plan §"Wire Protocol" line 482:
//
//	"Unknown fields are detected via dec.DisallowUnknownFields() server-
//	 side, but any decode failure or shape mismatch returns a uniform
//	 401 invalid_credentials on the wire."
//
// B8 fix: we use DisallowUnknownFields=false (the default) — extra fields
// are silently ignored. This is the correct shape for the wire-uniform
// B8 invariant: an unknown field with mode=shared-secret returns 401
// "invalid_credentials" via the Authenticate path (Source returns
// ErrInvalidCredentials when Credentials.Secret is empty after dropping
// the bogus field), not 400 "unknown_field:secret" which would leak the
// server's active mode.
//
// Genuine JSON parse errors are caught here and surfaced via the parseErr
// return; HandleLogin collapses them to the same 401 wire shape.
//
// Mode-shape mapping:
//   - shared-secret / per-deployment-login → reads "secret" string
//   - multi-user                            → reads "username" + "password"
//
// Unrecognised modes return an error (defensive — should never happen at
// runtime because Source.Mode() is one of three constants).
func parseCredentials(r *http.Request, mode string) (identity.Credentials, error) {
	dec := json.NewDecoder(r.Body)
	// DisallowUnknownFields is DELIBERATELY NOT set — see plan B8 (line 482).
	// Returning 400-with-field-name on extra fields leaks the active mode.
	var raw struct {
		Secret   string `json:"secret,omitempty"`
		Username string `json:"username,omitempty"`
		Password string `json:"password,omitempty"`
	}
	if err := dec.Decode(&raw); err != nil {
		return identity.Credentials{}, err
	}
	switch mode {
	case identity.ModeSharedSecret, identity.ModeDeploymentLogin:
		return identity.Credentials{Secret: raw.Secret}, nil
	case identity.ModeMultiUser:
		return identity.Credentials{Username: raw.Username, Password: raw.Password}, nil
	default:
		return identity.Credentials{}, errors.New("unknown mode")
	}
}

// writeLoginError writes the uniform B8 401 invalid_credentials response.
// Centralised so every login failure path produces a byte-identical wire
// shape — probers cannot fingerprint the mode by response variation.
func writeLoginError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"invalid_credentials"}`))
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// lookupForLogin is a thin wrapper around the SessionManager's store
// access used by HandleLogin to retrieve the freshly-minted Record for
// response composition. Internal — not part of the SessionManager's
// public API because callers outside HandleLogin should never need
// post-mint store lookup.
func (m *SessionManager) lookupForLogin(r *http.Request, token string) (*store.Record, error) {
	return m.store.Get(r.Context(), token)
}
